// Package adminwebhook delivers private administrator subscriptions from the
// committed federation outbox. Payloads contain identifiers, never post bodies.
package adminwebhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Subscription struct {
	ID          uint64    `gorm:"primaryKey" json:"id,string"`
	URL         string    `gorm:"size:2048;not null" json:"url"`
	Description string    `gorm:"size:200" json:"description"`
	Events      []string  `gorm:"type:json;serializer:json" json:"events"`
	Enabled     bool      `json:"enabled"`
	Secret      string    `gorm:"size:64;not null" json:"-"`
	Cursor      uint64    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (Subscription) TableName() string { return "admin_webhook_subscriptions" }

type Delivery struct {
	ID             string    `gorm:"primaryKey;size:36" json:"id"`
	SubscriptionID uint64    `gorm:"index;uniqueIndex:idx_hook_source,priority:1" json:"subscription_id,string"`
	EventID        uint64    `gorm:"uniqueIndex:idx_hook_source,priority:2" json:"event_id,string"`
	Event          string    `gorm:"size:64" json:"event"`
	Payload        string    `gorm:"type:text" json:"-"`
	State          string    `gorm:"size:16;index" json:"state"`
	Attempts       int       `json:"attempts"`
	LastError      string    `gorm:"size:256" json:"last_error"`
	NextAttemptAt  time.Time `gorm:"index" json:"next_attempt_at"`
	LeaseToken     string    `gorm:"size:36" json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (Delivery) TableName() string { return "admin_webhook_deliveries" }
func ValidateURL(value string) error {
	u, e := url.Parse(value)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || len(value) > 2048 {
		return errors.New("a credential-free HTTPS URL is required")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return errors.New("private destinations are forbidden")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast()) {
		return errors.New("private destinations are forbidden")
	}
	return nil
}
func ValidateEvents(events []string) error {
	if len(events) == 0 || len(events) > 3 {
		return errors.New("choose one or more supported events")
	}
	seen := map[string]bool{}
	for _, v := range events {
		if (v != "status.created" && v != "status.updated" && v != "status.deleted") || seen[v] {
			return errors.New("invalid or duplicate event")
		}
		seen[v] = true
	}
	return nil
}
func Create(db *gorm.DB, s *Subscription) (string, error) {
	if e := ValidateURL(s.URL); e != nil {
		return "", e
	}
	if e := ValidateEvents(s.Events); e != nil {
		return "", e
	}
	if len(s.Description) > 200 {
		return "", errors.New("description too long")
	}
	var random [32]byte
	if _, e := rand.Read(random[:]); e != nil {
		return "", e
	}
	s.Secret = hex.EncodeToString(random[:])
	e := db.Transaction(func(tx *gorm.DB) error {
		var max uint64
		if e := tx.Model(&models.FederationOutbox{}).Select("COALESCE(MAX(id),0)").Scan(&max).Error; e != nil {
			return e
		}
		s.Cursor = max
		return tx.Create(s).Error
	})
	return s.Secret, e
}
func Sign(secret, timestamp, id, payload string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(timestamp + "." + id + "." + payload))
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}
func eventName(kind string) string {
	switch kind {
	case "Create":
		return "status.created"
	case "Update":
		return "status.updated"
	case "Delete":
		return "status.deleted"
	}
	return ""
}
func subscribed(s Subscription, name string) bool {
	for _, e := range s.Events {
		if e == name {
			return true
		}
	}
	return false
}

// Capture and cursor movement share a transaction. Retrying a scan never creates
// a second delivery for the same subscription/outbox event.
func Capture(db *gorm.DB, id uint64) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var s Subscription
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&s, id).Error; e != nil {
			return e
		}
		if !s.Enabled {
			return nil
		}
		var rows []models.FederationOutbox
		if e := tx.Where("id > ?", s.Cursor).Order("id ASC").Limit(100).Find(&rows).Error; e != nil {
			return e
		}
		for _, row := range rows {
			name := eventName(row.Kind)
			if name != "" && subscribed(s, name) && publicSnapshot(row) {
				payload, e := json.Marshal(map[string]any{"event": name, "event_id": fmt.Sprint(row.ID), "object_id": fmt.Sprint(row.ObjectID), "account_id": fmt.Sprint(row.AccountID), "occurred_at": row.CreatedAt})
				if e != nil {
					return e
				}
				d := Delivery{ID: uuid.NewString(), SubscriptionID: s.ID, EventID: row.ID, Event: name, Payload: string(payload), State: "pending", NextAttemptAt: time.Now()}
				if e = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&d).Error; e != nil {
					return e
				}
			}
			s.Cursor = row.ID
		}
		return tx.Model(&Subscription{}).Where("id = ?", s.ID).Update("cursor", s.Cursor).Error
	})
}
func Process(ctx context.Context, db *gorm.DB, id string) error {
	return process(ctx, db, id, activitypub.NewAuthenticatedMediaHTTPClient(nil))
}
func process(ctx context.Context, db *gorm.DB, id string, client *http.Client) error {
	now := time.Now()
	token := uuid.NewString()
	r := db.WithContext(ctx).Model(&Delivery{}).Where("id = ? AND state IN ? AND next_attempt_at <= ?", id, []string{"pending", "processing"}, now).Updates(map[string]any{"state": "processing", "lease_token": token, "next_attempt_at": now.Add(time.Minute)})
	if r.Error != nil || r.RowsAffected == 0 {
		return r.Error
	}
	var d Delivery
	var s Subscription
	if e := db.First(&d, "id = ?", id).Error; e != nil {
		return e
	}
	err := db.First(&s, d.SubscriptionID).Error
	message := ""
	success := false
	if err != nil || !s.Enabled {
		message = "subscription unavailable or disabled"
	} else if ValidateURL(s.URL) != nil {
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
			req.Header.Set("X-Community-Signature", Sign(s.Secret, stamp, d.ID, d.Payload))
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
	return db.Model(&Delivery{}).Where("id = ? AND lease_token = ?", id, token).Updates(map[string]any{"state": state, "attempts": attempts, "last_error": message, "next_attempt_at": next, "lease_token": ""}).Error
}

// Start returns immediately; cancellation stops polling and in-flight HTTP.
func Start(ctx context.Context, db *gorm.DB) {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var subscriptions []Subscription
				if db.WithContext(ctx).Where("enabled = ?", true).Find(&subscriptions).Error != nil {
					continue
				}
				for _, s := range subscriptions {
					if ctx.Err() != nil {
						return
					}
					_ = Capture(db.WithContext(ctx), s.ID)
				}
				var deliveries []Delivery
				if db.WithContext(ctx).Where("state IN ? AND next_attempt_at <= ?", []string{"pending", "processing"}, time.Now()).Order("created_at").Limit(10).Find(&deliveries).Error == nil {
					for _, d := range deliveries {
						if ctx.Err() != nil {
							return
						}
						_ = Process(ctx, db, d.ID)
					}
				}
			}
		}
	}()
}

func publicSnapshot(row models.FederationOutbox) bool {
	var status models.Status
	var fields struct {
		Visibility *uint8 `json:"visibility"`
	}
	if json.Unmarshal([]byte(row.Payload), &status) != nil || json.Unmarshal([]byte(row.Payload), &fields) != nil {
		return false
	}
	return fields.Visibility != nil && status.Visibility == models.StatusVisibilityPublic && !status.LocalOnly && status.Domain == nil && status.ScheduledAt == nil && status.ReblogId == nil && status.Id == row.ObjectID && status.AccountId == row.AccountID && status.Id > 0
}
func QueueTest(db *gorm.DB, id uint64) (*Delivery, error) {
	var s Subscription
	if e := db.First(&s, id).Error; e != nil {
		return nil, e
	}
	if !s.Enabled {
		return nil, errors.New("enable subscription before sending test")
	}
	d := &Delivery{ID: uuid.NewString(), SubscriptionID: id, EventID: uint64(time.Now().UnixNano()), Event: "webhook.test", Payload: `{"event":"webhook.test","message":"Community webhook connectivity test"}`, State: "pending", NextAttemptAt: time.Now()}
	return d, db.Create(d).Error
}
func Rotate(db *gorm.DB, id uint64) (string, error) {
	var random [32]byte
	if _, e := rand.Read(random[:]); e != nil {
		return "", e
	}
	secret := hex.EncodeToString(random[:])
	r := db.Model(&Subscription{}).Where("id = ?", id).Update("secret", secret)
	if r.Error != nil {
		return "", r.Error
	}
	if r.RowsAffected == 0 {
		return "", gorm.ErrRecordNotFound
	}
	return secret, nil
}
