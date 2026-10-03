package models

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

// FederationOutbox is written in the same transaction as the local action.
// The worker only sees committed rows. Snapshots survive hard status/follow
// deletion and preserve each edit rather than reading a later version.
type FederationOutbox struct {
	ID            uint64   `gorm:"primaryKey;autoIncrement"`
	ObjectID      uint64   `gorm:"index"`
	AccountID     uint64   `gorm:"not null;index"`
	Kind          string   `gorm:"type:varchar(24);not null"`
	Payload       string   `gorm:"type:json;not null"`
	RecipientIDs  []uint64 `gorm:"type:json;serializer:json"`
	State         string   `gorm:"type:varchar(20);not null;default:'pending';index"`
	Attempts      int
	LastError     string     `gorm:"type:text"`
	NextAttemptAt *time.Time `gorm:"index"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (FederationOutbox) TableName() string { return "activitypub_outbox_events" }

var federationOutboxRequired atomic.Bool

// RequireFederationOutbox is enabled after server startup verifies migrations.
// Production mutations must fail if durable event persistence becomes unavailable.
func RequireFederationOutbox() { federationOutboxRequired.Store(true) }
func hasFederationOutbox(tx *gorm.DB) bool {
	return federationOutboxRequired.Load() || tx.Migrator().HasTable(&FederationOutbox{})
}

type deferredFederationCreateKey struct{}

// WithDeferredFederationCreate lets replacement transactions persist the new
// status before deleting the old one, without delivering Create before Delete.
func WithDeferredFederationCreate(ctx context.Context) context.Context {
	return context.WithValue(ctx, deferredFederationCreateKey{}, true)
}

// QueueFederationReplacement must run after the old status is deleted, inside
// the same transaction. Its explicit Create follows the Delete outbox row.
func QueueFederationReplacement(tx *gorm.DB, status *Status) error {
	return queueFederationStatus(tx.WithContext(context.WithValue(tx.Statement.Context, deferredFederationCreateKey{}, false)), "Create", status)
}

// queueFederationStatus runs inside GORM's mutation transaction. Minimal
// schemas used by migrations/tests intentionally omit the federation table.
func queueFederationStatus(tx *gorm.DB, kind string, s *Status) error {
	if kind == "Create" && tx.Statement.Context.Value(deferredFederationCreateKey{}) == true {
		return nil
	}
	if s.Id == 0 || s.AccountId == 0 || !s.IsLocal() || s.ScheduledAt != nil || s.ReblogId != nil || !hasFederationOutbox(tx) {
		return nil
	}
	var snapshot Status
	if err := StatusLoadAllQuery(tx.Session(&gorm.Session{NewDB: true})).First(&snapshot, s.Id).Error; err != nil {
		return err
	}
	if snapshot.Account == nil || snapshot.Account.Domain != nil {
		return nil
	}
	recipients := make(map[uint64]bool)
	for _, account := range snapshot.ToAccounts {
		if account.Domain != nil {
			recipients[account.Id] = true
		}
	}
	if snapshot.Visibility != StatusVisibilityDirect {
		var followers []Follow
		if err := tx.Session(&gorm.Session{NewDB: true}).Where("follow_id = ? AND status = ?", snapshot.AccountId, FollowStatusFollowed).Preload("Account").Find(&followers).Error; err != nil {
			return err
		}
		for _, f := range followers {
			if f.Account != nil && f.Account.Domain != nil {
				recipients[f.AccountId] = true
			}
		}
		if (snapshot.Visibility == StatusVisibilityPublic || snapshot.Visibility == StatusVisibilityUnlisted) && snapshot.ReplyAccount != nil && snapshot.ReplyAccount.Domain != nil {
			recipients[snapshot.ReplyAccount.Id] = true
		}
	}
	if kind == "Delete" {
		// A recipient who unfollowed after receiving a private post still needs
		// its Tombstone; the snapshot contains no content in the sent Delete.
		var previous []FederationOutbox
		if err := tx.Session(&gorm.Session{NewDB: true}).Where("account_id = ? AND object_id = ? AND kind IN ?", snapshot.AccountId, snapshot.Id, []string{"Create", "Update"}).Find(&previous).Error; err != nil {
			return err
		}
		for _, event := range previous {
			for _, id := range event.RecipientIDs {
				recipients[id] = true
			}
		}
	}
	ids := make([]uint64, 0, len(recipients))
	for id := range recipients {
		ids = append(ids, id)
	}
	payload, err := json.Marshal(&snapshot)
	if err != nil {
		return err
	}
	return tx.Session(&gorm.Session{NewDB: true}).Create(&FederationOutbox{ObjectID: snapshot.Id, AccountID: snapshot.AccountId, Kind: kind, Payload: string(payload), RecipientIDs: ids}).Error
}

func queueFederationFollow(tx *gorm.DB, kind string, f *Follow) error {
	if f.Id == 0 || f.AccountId == 0 || f.FollowId == 0 || !hasFederationOutbox(tx) {
		return nil
	}
	var actor, target Account
	query := tx.Session(&gorm.Session{NewDB: true})
	if err := query.First(&actor, f.AccountId).Error; err != nil {
		return err
	}
	if actor.Domain != nil {
		return nil
	}
	if err := query.First(&target, f.FollowId).Error; err != nil {
		return err
	}
	if target.Domain == nil {
		return nil
	}
	payload, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return tx.Session(&gorm.Session{NewDB: true}).Create(&FederationOutbox{ObjectID: f.Id, AccountID: f.AccountId, Kind: kind, Payload: string(payload), RecipientIDs: []uint64{f.FollowId}}).Error
}

// QueueFederationFollowDecision durably records a local owner's decision on
// an incoming remote follow in the controller's approval/rejection transaction.
func QueueFederationFollowDecision(tx *gorm.DB, kind string, f *Follow) error {
	if kind != "Accept" && kind != "Reject" {
		return fmt.Errorf("invalid follow decision %q", kind)
	}
	if !hasFederationOutbox(tx) {
		return nil
	}
	var requester, owner Account
	query := tx.Session(&gorm.Session{NewDB: true})
	if err := query.First(&requester, f.AccountId).Error; err != nil {
		return err
	}
	if requester.Domain == nil {
		return nil
	}
	if err := query.First(&owner, f.FollowId).Error; err != nil {
		return err
	}
	if owner.Domain != nil {
		return nil
	}
	originalID, _ := f.Properties["activitypub_follow_id"].(string)
	if originalID == "" {
		return fmt.Errorf("remote follow has no original activity ID")
	}
	payload, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return tx.Session(&gorm.Session{NewDB: true}).Create(&FederationOutbox{AccountID: owner.Id, ObjectID: f.Id, Kind: kind, Payload: string(payload), RecipientIDs: []uint64{requester.Id}}).Error
}
