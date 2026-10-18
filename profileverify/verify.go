// Package profileverify verifies public profile links outside the save request.
package profileverify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/cache"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"golang.org/x/net/html"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func Enqueue(tx *gorm.DB, accountID uint64) error {
	if !tx.Migrator().HasTable(&models.ProfileFieldVerification{}) {
		return nil
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "account_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"version": gorm.Expr("profile_field_verifications.version + 1"),
			"state":   "pending", "attempts": 0, "lease_token": "", "next_attempt_at": time.Now(), "updated_at": time.Now(),
		}),
	}).Create(&models.ProfileFieldVerification{AccountID: accountID, Version: 1, State: "pending", NextAttemptAt: time.Now()}).Error
}

var verificationClient = activitypub.NewMediaFetchHTTPClient(4 * time.Second)

func Process(ctx context.Context, conn *gorm.DB, id uint64) error {
	return process(ctx, conn, id, verificationClient)
}

func process(ctx context.Context, conn *gorm.DB, id uint64, client *http.Client) error {
	token := uuid.NewString()
	claimed := conn.WithContext(ctx).Model(&models.ProfileFieldVerification{}).
		Where("account_id = ? AND next_attempt_at <= ?", id, time.Now()).
		Updates(map[string]any{"state": "processing", "lease_token": token, "next_attempt_at": time.Now().Add(time.Minute)})
	if claimed.Error != nil || claimed.RowsAffected == 0 {
		return claimed.Error
	}
	var job models.ProfileFieldVerification
	var account models.Account
	if err := conn.WithContext(ctx).First(&job, "account_id = ? AND lease_token = ?", id, token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if err := conn.WithContext(ctx).Preload("Extra").First(&account, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return conn.WithContext(ctx).Where("account_id = ? AND lease_token = ?", id, token).Delete(&models.ProfileFieldVerification{}).Error
		}
		return err
	}
	if !account.IsLocal() || account.IsBan || account.DeletionRequestedAt != nil || account.Extra == nil {
		return conn.WithContext(ctx).Where("account_id = ? AND lease_token = ?", id, token).Delete(&models.ProfileFieldVerification{}).Error
	}
	work, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	proofs := map[string]bool{}
	failed := false
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	for _, field := range account.Extra.Fields {
		if !webURL(field.Val) {
			continue
		}
		if _, done := proofs[field.Val]; done {
			continue
		}
		requestCtx, stop := context.WithTimeout(work, 4*time.Second)
		valid, err := verifyURL(requestCtx, client, field.Val, []string{base + "/@" + account.Username, account.ActorURI(base)})
		stop()
		if err != nil {
			failed = true
			continue
		}
		proofs[field.Val] = valid
	}
	finish, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	err := conn.WithContext(finish).Transaction(func(tx *gorm.DB) error {
		var current models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, id).Error; err != nil {
			return err
		}
		var currentJob models.ProfileFieldVerification
		if err := tx.First(&currentJob, "account_id = ? AND version = ? AND lease_token = ?", id, job.Version, token).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if !current.IsBan && current.DeletionRequestedAt == nil {
			var extra models.AccountExtra
			if err := tx.First(&extra, "account_id = ?", id).Error; err != nil {
				return err
			}
			now := time.Now()
			for i := range extra.Fields {
				if valid, checked := proofs[extra.Fields[i].Val]; checked {
					extra.Fields[i].VerifiedAt = nil
					if valid {
						extra.Fields[i].VerifiedAt = &now
					}
				}
			}
			if err := tx.Model(&extra).Select("Fields").Updates(&extra).Error; err != nil {
				return err
			}
		}
		query := tx.Where("account_id = ? AND version = ? AND lease_token = ?", id, job.Version, token)
		if failed && job.Attempts < 4 && !current.IsBan {
			attempts := job.Attempts + 1
			return query.Model(&models.ProfileFieldVerification{}).Updates(map[string]any{
				"state": "pending", "lease_token": "", "attempts": attempts,
				"next_attempt_at": time.Now().Add(time.Duration(attempts*attempts) * time.Minute),
			}).Error
		}
		return query.Delete(&models.ProfileFieldVerification{}).Error
	})
	if err == nil && len(cRedis.GetRedises()) > 0 {
		_ = cache.InvalidateAccount(finish, id)
	}
	return err
}

func webURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

func verifyURL(ctx context.Context, client *http.Client, raw string, identities []string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "text/html, application/xhtml+xml")
	res, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 500 || res.StatusCode == http.StatusTooManyRequests {
		return false, errors.New("profile link is temporarily unavailable")
	}
	if res.StatusCode != http.StatusOK {
		return false, nil
	}
	contentType := strings.ToLower(res.Header.Get("Content-Type"))
	if contentType != "" && !strings.HasPrefix(contentType, "text/html") && !strings.HasPrefix(contentType, "application/xhtml+xml") {
		return false, nil
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if err != nil {
		return false, err
	}
	if len(data) > 1024*1024 {
		return false, errors.New("profile link page exceeds limit")
	}
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return false, err
	}
	page := req.URL
	if res.Request != nil && res.Request.URL != nil {
		page = res.Request.URL
	}
	var walk func(*html.Node) bool
	walk = func(node *html.Node) bool {
		if node.Type == html.ElementNode && (node.Data == "a" || node.Data == "link") {
			href, rel := "", ""
			for _, a := range node.Attr {
				if a.Key == "href" {
					href = a.Val
				}
				if a.Key == "rel" {
					rel = a.Val
				}
			}
			for _, token := range strings.Fields(strings.ToLower(rel)) {
				if token != "me" {
					continue
				}
				if link, err := url.Parse(href); err == nil && href != "" {
					link = page.ResolveReference(link)
					link.Fragment = ""
					for _, identity := range identities {
						if link.String() == strings.TrimRight(identity, "/") || strings.TrimRight(link.String(), "/") == identity {
							return true
						}
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if walk(child) {
				return true
			}
		}
		return false
	}
	return walk(doc), nil
}

func Start(ctx context.Context, conn *gorm.DB) {
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			var rows []models.ProfileFieldVerification
			if conn.WithContext(ctx).Select("account_id").Where("next_attempt_at <= ?", time.Now()).Order("next_attempt_at").Limit(2).Find(&rows).Error != nil {
				continue
			}
			for _, row := range rows {
				if ctx.Err() != nil {
					return
				}
				if err := Process(ctx, conn, row.AccountID); err != nil && ctx.Err() == nil {
					log.Warn("Profile link verification for account %d: %v", row.AccountID, err)
				}
			}
		}
	}()
}
