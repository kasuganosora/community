// Package userwebhook delivers committed personal events with bounded retries.
package userwebhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/adminwebhook"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"io"
	"net/http"
	"sync"
	"time"
)

func deliveryClient() *http.Client {
	client := activitypub.NewAuthenticatedMediaHTTPClient(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}
func Process(ctx context.Context, db *gorm.DB, id string) error {
	return process(ctx, db, id, deliveryClient())
}
func process(ctx context.Context, db *gorm.DB, id string, client *http.Client) error {
	db = db.WithContext(ctx)
	now := time.Now()
	token := uuid.NewString()
	r := db.WithContext(ctx).Model(&models.UserWebhookDelivery{}).Where("id = ? AND state IN ? AND next_attempt_at <= ?", id, []string{"pending", "processing"}, now).Updates(map[string]any{"state": "processing", "lease_token": token, "next_attempt_at": now.Add(time.Minute)})
	if r.Error != nil || r.RowsAffected == 0 {
		return r.Error
	}
	var d models.UserWebhookDelivery
	var s models.UserWebhook
	// Materialize the lease and destination together. Configuration changes
	// cancel deliveries before updating the hook; locking this row prevents
	// a cancelled old event from being redirected to a new destination.
	var hookErr error
	err := db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&d, "id = ? AND lease_token = ? AND state = ?", id, token, "processing").Error; e != nil {
			return e
		}
		hookErr = tx.First(&s, "id = ?", d.WebhookID).Error
		if hookErr != nil && !errors.Is(hookErr, gorm.ErrRecordNotFound) {
			return hookErr
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // Removed or cancelled before delivery began.
		}
		return err // Keep the durable lease retryable after a database outage.
	}
	err = hookErr
	message := ""
	success := false
	if err != nil || !s.Enabled {
		message = "subscription unavailable or disabled"
	} else if adminwebhook.ValidateURL(s.URL) != nil {
		message = "destination URL is invalid"
	} else {
		requestCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		req, e := http.NewRequestWithContext(requestCtx, http.MethodPost, s.URL, bytes.NewBufferString(d.Payload))
		if e != nil {
			message = "could not construct request"
		} else {
			stamp := fmt.Sprint(time.Now().Unix())
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Community-Event", d.Event)
			req.Header.Set("X-Community-Delivery", d.ID)
			req.Header.Set("X-Community-Timestamp", stamp)
			req.Header.Set("X-Community-Signature", adminwebhook.Sign(s.Secret, stamp, d.ID, d.Payload))
			req.Header.Set("Idempotency-Key", d.ID)
			response, e := client.Do(req)
			if e != nil {
				message = "connection, TLS or destination policy rejected request"
			} else {
				io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
				response.Body.Close()
				success = response.StatusCode >= 200 && response.StatusCode < 300
				if !success {
					message = fmt.Sprintf("receiver returned HTTP %d", response.StatusCode)
				}
			}
		}
	}
	state := "pending"
	attempts := d.Attempts + 1
	if success {
		state = "delivered"
	} else if attempts >= 5 {
		state = "failed"
	}
	next := time.Now().Add(time.Duration(attempts*attempts) * time.Minute)
	updates := map[string]any{"state": state, "attempts": attempts, "last_error": message, "next_attempt_at": next, "lease_token": ""}
	if success || state == "failed" {
		updates["payload"] = ""
	}
	if err != nil || !s.Enabled {
		updates["state"] = "cancelled"
		updates["payload"] = ""
	}
	return db.Model(&models.UserWebhookDelivery{}).Where("id = ? AND lease_token = ?", id, token).Updates(updates).Error
}

// Start is cancellable and processes four destinations concurrently, so a slow
// receiver does not block unrelated hooks. Leases survive process restarts.
func Start(ctx context.Context, db *gorm.DB) {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		nextCleanup := time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if time.Now().After(nextCleanup) {
				db.WithContext(ctx).Where("state IN ? AND created_at < ?", []string{"delivered", "failed", "cancelled"}, time.Now().AddDate(0, 0, -30)).Delete(&models.UserWebhookDelivery{})
				nextCleanup = time.Now().Add(time.Hour)
			}
			var rows []models.UserWebhookDelivery
			if db.WithContext(ctx).Select("id").Where("state IN ? AND next_attempt_at <= ?", []string{"pending", "processing"}, time.Now()).Order("created_at").Limit(4).Find(&rows).Error != nil {
				continue
			}
			var group sync.WaitGroup
			for _, row := range rows {
				group.Add(1)
				go func(id string) {
					defer group.Done()
					if err := Process(ctx, db, id); err != nil && ctx.Err() == nil && !errors.Is(err, gorm.ErrRecordNotFound) {
						log.Warn("Personal webhook delivery %s: %v", id, err)
					}
				}(row.ID)
			}
			group.Wait()
		}
	}()
}
