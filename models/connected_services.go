package models

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const PersonalTokenClient = "community:personal-access-token"

type PersonalAccessToken struct {
	ID        string   `gorm:"primaryKey;size:36" json:"id"`
	UserID    uint64   `gorm:"index;not null" json:"-"`
	Name      string   `gorm:"size:100;not null" json:"name"`
	TokenHash string   `gorm:"size:128;uniqueIndex;not null" json:"-"`
	Scopes    []string `gorm:"type:json;serializer:json" json:"scopes"`
	// ScopeBits are the authoritative scope bits checked at request time.
	// NULL only for rows created before editing existed; the auth middleware
	// fills them once from the token blob (never treated as none or all).
	ScopeBits *ScopeBits `gorm:"type:text;serializer:json" json:"-"`
	// ScopeSource records where ScopeBits came from: "migration" (derived
	// from Scopes by the migration), "blob" (copied from the token on first
	// use) or "edit" (set by the owner). Only "blob" and "edit" are
	// authoritative over the token blob; see PersonalTokenEffectiveScopes.
	ScopeSource string     `gorm:"size:16;not null;default:''" json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	// LastUsedAt is read from the matching auth_tokens row for display.
	LastUsedAt *time.Time `gorm:"-" json:"last_used_at"`
	// RevokedReason is set when the token was revoked automatically.
	RevokedReason string `gorm:"-" json:"revoked_reason,omitempty"`
}

type UserWebhook struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	AccountID uint64    `gorm:"index;not null" json:"-"`
	Name      string    `gorm:"size:100;not null" json:"name"`
	URL       string    `gorm:"size:2048;not null" json:"url"`
	Secret    string    `gorm:"size:256;not null" json:"-"`
	Events    []string  `gorm:"type:json;serializer:json" json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UserWebhookDelivery struct {
	ID            string    `gorm:"primaryKey;size:36" json:"id"`
	WebhookID     string    `gorm:"size:36;index;uniqueIndex:idx_user_hook_source,priority:1" json:"webhook_id"`
	Source        string    `gorm:"size:100;uniqueIndex:idx_user_hook_source,priority:2" json:"-"`
	Event         string    `gorm:"size:32" json:"event"`
	Payload       string    `gorm:"type:text" json:"-"`
	State         string    `gorm:"size:16;index" json:"state"`
	Attempts      int       `json:"attempts"`
	LastError     string    `gorm:"size:256" json:"last_error"`
	NextAttemptAt time.Time `gorm:"index" json:"next_attempt_at"`
	LeaseToken    string    `gorm:"size:36" json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

var UserWebhookEvents = []string{"follow", "followed", "note", "noteUpdated", "noteDeleted", "reply", "renote", "reaction", "mention"}

// QueueUserWebhook stores a bounded, owner-scoped event in the same transaction
// as its source mutation. Historical imports do not replay user integrations.
func QueueUserWebhook(tx *gorm.DB, owner uint64, source, name string, body map[string]any) error {
	if owner == 0 || tx.Statement.Context.Value(feedImportBoundaryKey{}) == true || !tx.Migrator().HasTable(&UserWebhook{}) {
		return nil
	}
	conn := tx.Session(&gorm.Session{NewDB: true})
	var hooks []UserWebhook
	if err := conn.Where("account_id = ? AND enabled = ?", owner, true).Limit(10).Find(&hooks).Error; err != nil {
		return err
	}
	for _, hook := range hooks {
		if !slices.Contains(hook.Events, name) {
			continue
		}
		id := uuid.NewString()
		payload, err := json.Marshal(map[string]any{"id": id, "hookId": hook.ID, "type": name, "createdAt": time.Now().UTC(), "body": body})
		if err != nil {
			return err
		}
		row := UserWebhookDelivery{ID: id, WebhookID: hook.ID, Source: source + ":" + name, Event: name, Payload: string(payload), State: "pending", NextAttemptAt: time.Now()}
		if err = conn.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (e *FeedEvent) AfterCreate(tx *gorm.DB) error {
	if !tx.Migrator().HasTable(&UserWebhook{}) {
		return nil
	}
	conn := tx.Session(&gorm.Session{NewDB: true})
	source := fmt.Sprintf("feed:%d", e.ID)
	name := ""
	switch e.Kind {
	case FeedStatusCreate:
		name = "note"
	case FeedStatusRefresh:
		name = "noteUpdated"
	case FeedStatusDelete:
		name = "noteDeleted"
	case FeedNotification:
		var n Notification
		if err := conn.First(&n, e.ObjectID).Error; err != nil {
			return err
		}
		switch n.Type {
		case NotificationTypeFollow:
			name = "followed"
		case NotificationTypeReblog:
			name = "renote"
		case NotificationTypeFavourite, NotificationTypeEmojiReaction:
			name = "reaction"
		case NotificationTypeMention:
			name = "mention"
		default:
			return nil
		}
		body := map[string]any{"notificationId": fmt.Sprint(n.Id), "userId": fmt.Sprint(n.FromAccountId), "noteId": fmt.Sprint(n.StatusId)}
		if err := QueueUserWebhook(conn, n.AccountId, source, name, body); err != nil {
			return err
		}
		if n.Type == NotificationTypeMention && n.StatusId > 0 {
			var status Status
			if err := conn.Select("id", "in_reply_to_account_id").First(&status, n.StatusId).Error; err != nil {
				return err
			}
			if status.InReplyToAccountId != nil && *status.InReplyToAccountId == n.AccountId {
				return QueueUserWebhook(conn, n.AccountId, source, "reply", body)
			}
		}
		return nil
	default:
		return nil
	}
	// Only the author receives post-change events, including private/local posts.
	return QueueUserWebhook(conn, e.ActorID, source, name, map[string]any{"noteId": fmt.Sprint(e.ObjectID), "userId": fmt.Sprint(e.ActorID)})
}

// ScopeBits is ApplicationScope stored as JSON (without its SQL Scanner,
// which rejects NULL).
type ScopeBits ApplicationScope

// Scope returns the bits as an ApplicationScope (nil when unset).
func (b *ScopeBits) Scope() *ApplicationScope { return (*ApplicationScope)(b) }
