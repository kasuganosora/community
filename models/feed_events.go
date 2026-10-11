package models

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

const (
	FeedStatusCreate  = "status_create"
	FeedStatusRefresh = "status_refresh"
	FeedStatusDelete  = "status_delete"
	FeedInvalidate    = "feed_invalidate"
	FeedNotification  = "notification"
)

var ErrFeedLeaseLost = errors.New("feed lease is stale or expired")
var ErrFeedDependencyFailed = errors.New("feed dependent batch failed or was cancelled")
var ErrFeedNotComplete = errors.New("feed scan or batches are incomplete")
var feedOutboxRequired atomic.Bool

func RequireFeedOutbox()       { feedOutboxRequired.Store(true) }
func FeedOutboxRequired() bool { return feedOutboxRequired.Load() }
func HasFeedOutbox(tx *gorm.DB) bool {
	return feedOutboxRequired.Load() || (tx.Migrator().HasTable(&FeedEvent{}) && tx.Migrator().HasTable(&FeedBatch{}))
}

type FeedEventMetadata struct {
	PreviousRecipientIDs []uint64 `json:"previous_recipient_ids,omitempty"`
	PreviousTagNames     []string `json:"previous_tag_names,omitempty"`
	ActorID              uint64   `json:"actor_id,omitempty"`
	RecipientIDs         []uint64 `json:"recipient_ids,omitempty"`
	Visibility           uint8    `json:"visibility"`
	ReblogID             uint64   `json:"reblog_id,omitempty"`
	ReplyID              uint64   `json:"reply_id,omitempty"`
	ConversationID       uint64   `json:"conversation_id,omitempty"`
	TagIDs               []uint64 `json:"tag_ids,omitempty"`
	TagNames             []string `json:"tag_names,omitempty"`
	HasMedia             bool     `json:"has_media"`
	ListIDs              []uint64 `json:"list_ids,omitempty"`
	NotificationID       uint64   `json:"notification_id,omitempty"`
	RelationshipID       uint64   `json:"relationship_id,omitempty"`
	TargetAccountID      uint64   `json:"target_account_id,omitempty"`
	TargetDomain         string   `json:"target_domain,omitempty"`
	Reason               string   `json:"reason,omitempty"`
	LocalOnly            bool     `json:"local_only,omitempty"`
	Local                bool     `json:"local"`
}

// MaxFeedMetadataBytes bounds the actual JSON representation, including UTF-8
// and JSON escaping. Never truncate recipients: failure must roll back the
// business transaction that queued the event.
const MaxFeedMetadataBytes = 256 * 1024

var ErrFeedMetadataTooLarge = errors.New("feed event metadata exceeds 256 KiB JSON limit")

// MarshalJSON is shared by every typed application write through GORM's JSON
// serializer (Create, Save and struct Updates). Queue metadata is immutable
// afterward; lease/cursor updates never rewrite it. Raw SQL or manually supplied
// JSON strings are not application metadata persistence entry points.
func (metadata FeedEventMetadata) MarshalJSON() ([]byte, error) {
	type plain FeedEventMetadata
	encoded, err := json.Marshal(plain(metadata))
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxFeedMetadataBytes {
		return nil, ErrFeedMetadataTooLarge
	}
	return encoded, nil
}

type FeedLease struct {
	State           string     `gorm:"size:16;not null;default:pending;index:,composite:feed_claim,priority:1"`
	LeaseGeneration uint64     `gorm:"not null;default:0"`
	LeaseToken      string     `gorm:"size:64;not null;default:''"`
	LeaseExpiresAt  *time.Time `gorm:"index:,composite:feed_claim,priority:3"`
	Attempts        uint32     `gorm:"not null;default:0"`
	NextAttemptAt   *time.Time `gorm:"index:,composite:feed_claim,priority:2"`
	LastErrorCode   string     `gorm:"size:64;not null;default:''"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type FeedEvent struct {
	ID             uint64            `gorm:"primaryKey;autoIncrement"`
	EventKey       string            `gorm:"size:128;uniqueIndex;not null"`
	Kind           string            `gorm:"size:24;not null"`
	ObjectType     string            `gorm:"size:24;not null"`
	ObjectID       uint64            `gorm:"index;not null"`
	ActorID        uint64            `gorm:"index;not null"`
	OperationID    string            `gorm:"size:64;index"`
	ObjectVersion  uint64            `gorm:"not null"`
	Metadata       FeedEventMetadata `gorm:"type:json;serializer:json;not null"`
	ScanCursor     map[string]uint64 `gorm:"type:json;serializer:json"`
	ScanUpperBound map[string]uint64 `gorm:"type:json;serializer:json"`
	ScanComplete   bool              `gorm:"not null;default:false"`
	FeedLease      `gorm:"embedded"`
}

func (FeedEvent) TableName() string { return "feed_events" }

type FeedLeaseRef struct {
	ID         uint64
	Token      string
	Generation uint64
}

func (e FeedEvent) LeaseRef() FeedLeaseRef {
	return FeedLeaseRef{e.ID, e.LeaseToken, e.LeaseGeneration}
}
func newFeedToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

type feedImportBoundaryKey struct{}

// WithFeedImportBoundary is restricted to bulk import transactions whose caller
// schedules a committed full invalidation/rebuild. It never republishes history.
func WithFeedImportBoundary(ctx context.Context) context.Context {
	return context.WithValue(ctx, feedImportBoundaryKey{}, true)
}
func skipFeedHooks(tx *gorm.DB) bool {
	return tx.Statement.Context.Value(feedImportBoundaryKey{}) == true || !HasFeedOutbox(tx)
}

type feedOperationKey struct{}

func WithFeedOperation(ctx context.Context, operationID string) context.Context {
	return context.WithValue(ctx, feedOperationKey{}, operationID)
}
func feedOperation(tx *gorm.DB) string {
	value, _ := tx.Statement.Context.Value(feedOperationKey{}).(string)
	return value
}

// QueueFeedInvalidation persists a current-state reconciliation request, never
// an authorization snapshot. Call inside the transaction changing relationships.
func QueueFeedInvalidation(tx *gorm.DB, metadata FeedEventMetadata) error {
	if skipFeedHooks(tx) {
		return nil
	}
	token, err := newFeedToken()
	if err != nil {
		return err
	}
	return tx.Session(&gorm.Session{NewDB: true}).Create(&FeedEvent{EventKey: token, Kind: FeedInvalidate, ObjectType: "account", ObjectID: metadata.ActorID, ActorID: metadata.ActorID, OperationID: feedOperation(tx), Metadata: metadata}).Error
}

// FeedDatabaseTime uses the database clock for all lease CAS comparisons.
func FeedDatabaseTime(conn *gorm.DB) (time.Time, error) {
	query := "SELECT " + feedClockSQL(conn)
	var value string
	if err := conn.Raw(query).Scan(&value).Error; err != nil {
		return time.Time{}, err
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported database clock format")
}

func feedClockSQL(conn *gorm.DB) string {
	switch conn.Dialector.Name() {
	case "mysql":
		return "CURRENT_TIMESTAMP(6)"
	case "postgres":
		return "clock_timestamp()"
	default:
		return "STRFTIME('%Y-%m-%d %H:%M:%f','now')"
	}
}

func QueueFeedNotification(tx *gorm.DB, n *Notification) error {
	if n.Id == 0 || skipFeedHooks(tx) {
		return nil
	}
	return tx.Session(&gorm.Session{NewDB: true}).Create(&FeedEvent{EventKey: fmt.Sprintf("notification:%d", n.Id), Kind: FeedNotification, ObjectType: "notification", ObjectID: n.Id, ActorID: n.FromAccountId, ObjectVersion: 1, Metadata: FeedEventMetadata{ActorID: n.FromAccountId, RecipientIDs: []uint64{n.AccountId}, NotificationID: n.Id, Local: true}}).Error
}
