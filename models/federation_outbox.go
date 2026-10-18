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
	if s.LocalOnly || s.Id == 0 || s.AccountId == 0 || !s.IsLocal() || s.ScheduledAt != nil || !hasFederationOutbox(tx) {
		return nil
	}
	if s.ReblogId != nil {
		if *s.ReblogId == 0 {
			return nil
		}
		return queueFederationReblog(tx, kind, s)
	}
	var snapshot Status
	if err := StatusLoadAllQuery(tx.Session(&gorm.Session{NewDB: true})).First(&snapshot, s.Id).Error; err != nil {
		return err
	}
	if snapshot.LocalOnly || snapshot.Account == nil || snapshot.Account.Domain != nil {
		return nil
	}
	recipients := make(map[uint64]bool)
	for _, account := range snapshot.ToAccounts {
		if account.Domain != nil {
			recipients[account.Id] = true
		}
	}
	if snapshot.Visibility != StatusVisibilityDirect {
		if err := collectFederationFollowers(tx, snapshot.AccountId, recipients); err != nil {
			return err
		}
		if (snapshot.Visibility == StatusVisibilityPublic || snapshot.Visibility == StatusVisibilityUnlisted) && snapshot.ReplyAccount != nil && snapshot.ReplyAccount.Domain != nil {
			recipients[snapshot.ReplyAccount.Id] = true
		}
	}
	if snapshot.Poll != nil && snapshot.Poll.Id != 0 && kind != "Delete" {
		// Remote voters need tally/close Updates even when they do not follow.
		var voterIDs []uint64
		if err := tx.Session(&gorm.Session{NewDB: true}).Table("poll_option_votes").
			Joins("JOIN accounts ON accounts.id = poll_option_votes.account_id AND accounts.domain IS NOT NULL").
			Where("poll_option_votes.poll_id = ?", snapshot.Poll.Id).Distinct().Pluck("poll_option_votes.account_id", &voterIDs).Error; err != nil {
			return err
		}
		for _, id := range voterIDs {
			recipients[id] = true
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
	if kind == "Delete" && snapshot.Account.DeletionRequestedAt != nil {
		// Erasure tombstones only need identity and the original audience.
		// Do not retain a second copy of deleted private content in the queue.
		snapshot.Content = StatusContent{}
		snapshot.Attachments = nil
		snapshot.Emojis = nil
		snapshot.Poll = nil
		snapshot.Properties = nil
		snapshot.Account.Note = ""
		snapshot.Account.ImportedNoteHTML = ""
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

// queueFederationReblog records an Announce (or its Undo) for a local boost
// of a public or unlisted post. Private boosts are never federated.
func queueFederationReblog(tx *gorm.DB, kind string, s *Status) error {
	switch kind {
	case "Create":
		kind = "Announce"
	case "Delete":
		kind = "AnnounceUndo"
	default:
		return nil
	}
	var snapshot Status
	if err := StatusLoadAllQuery(tx.Session(&gorm.Session{NewDB: true})).First(&snapshot, s.Id).Error; err != nil {
		return err
	}
	original := snapshot.Reblog
	if snapshot.Account == nil || snapshot.Account.Domain != nil || original == nil || original.Account == nil || original.LocalOnly {
		return nil
	}
	federated := func(v uint8) bool { return v == StatusVisibilityPublic || v == StatusVisibilityUnlisted }
	if !federated(snapshot.Visibility) || !federated(original.Visibility) {
		return nil
	}
	recipients := make(map[uint64]bool)
	if original.Account.Domain != nil {
		recipients[original.AccountId] = true
	}
	if err := collectFederationFollowers(tx, snapshot.AccountId, recipients); err != nil {
		return err
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

// Filter in SQL before loading recipients: local followers need no federation
// delivery, and hydrating every account creates an unbounded IN parameter list.
func collectFederationFollowers(tx *gorm.DB, actorID uint64, recipients map[uint64]bool) error {
	var cursor uint64
	for {
		var rows []struct {
			ID        uint64
			AccountID uint64
		}
		err := tx.Session(&gorm.Session{NewDB: true}).Table("follows").
			Select("follows.id, follows.account_id").
			Joins("JOIN accounts ON accounts.id = follows.account_id AND accounts.deleted_at IS NULL AND accounts.domain IS NOT NULL").
			Where("follows.follow_id = ? AND follows.status = ? AND follows.id > ?", actorID, FollowStatusFollowed, cursor).
			Order("follows.id ASC").Limit(512).Scan(&rows).Error
		if err != nil {
			return err
		}
		for _, row := range rows {
			recipients[row.AccountID] = true
			cursor = row.ID
		}
		if len(rows) < 512 {
			return nil
		}
	}
}

// QueuePollUpdate records an Update of a local poll status so peers refresh
// its tallies or learn that it closed (Mastodon sends Update(Question) for
// both). Pending Updates for the same status are coalesced: the worker
// serializes the current snapshot only once per batch of votes.
func QueuePollUpdate(tx *gorm.DB, statusID uint64, force bool) error {
	if statusID == 0 || !hasFederationOutbox(tx) {
		return nil
	}
	if !force {
		var pending int64
		if err := tx.Session(&gorm.Session{NewDB: true}).Model(&FederationOutbox{}).
			Where("object_id = ? AND kind = ? AND state = ?", statusID, "Update", "pending").Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return nil
		}
	}
	var s Status
	if err := tx.Session(&gorm.Session{NewDB: true}).First(&s, statusID).Error; err != nil {
		return err
	}
	return queueFederationStatus(tx, "Update", &s)
}

// QueuePollVote records the votes of a local account on a remote poll. The
// worker sends one Create(Note{name, inReplyTo}) per choice to the author.
func QueuePollVote(tx *gorm.DB, voter *Account, pollStatus *Status, choices []string) error {
	if voter == nil || voter.Domain != nil || pollStatus == nil || pollStatus.Account == nil || pollStatus.Account.Domain == nil || len(choices) == 0 || !hasFederationOutbox(tx) {
		return nil
	}
	payload, err := json.Marshal(PollVotePayload{StatusID: pollStatus.Id, Choices: choices})
	if err != nil {
		return err
	}
	return tx.Session(&gorm.Session{NewDB: true}).Create(&FederationOutbox{ObjectID: pollStatus.Id, AccountID: voter.Id, Kind: "PollVote", Payload: string(payload), RecipientIDs: []uint64{pollStatus.AccountId}}).Error
}

// PollVotePayload is the outbox payload of a "PollVote" event.
type PollVotePayload struct {
	StatusID uint64   `json:"status_id"`
	Choices  []string `json:"choices"`
}
