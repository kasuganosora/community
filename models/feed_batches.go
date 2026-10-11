package models

import (
	"encoding/json"
	"fmt"
	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type FeedRecipient struct {
	TimelineID string `json:"timeline_id,omitempty"`
	AccountID  uint64 `json:"account_id,omitempty"`
	ListID     uint64 `json:"list_id,omitempty"`
	Version    uint64 `json:"version,omitempty"`
}
type FeedBatch struct {
	ID         uint64          `gorm:"primaryKey;autoIncrement"`
	EventID    uint64          `gorm:"not null;uniqueIndex:idx_feed_batch_identity,priority:1"`
	TargetKind string          `gorm:"size:24;not null;uniqueIndex:idx_feed_batch_identity,priority:2"`
	BatchNo    uint64          `gorm:"not null;uniqueIndex:idx_feed_batch_identity,priority:3"`
	Recipients []FeedRecipient `gorm:"type:json;serializer:json;not null"`
	FeedLease  `gorm:"embedded"`
}

func (FeedBatch) TableName() string { return "feed_batches" }
func (b FeedBatch) LeaseRef() FeedLeaseRef {
	return FeedLeaseRef{b.ID, b.LeaseToken, b.LeaseGeneration}
}

// feedClaimable is the claim predicate. The state list is a literal, not a bind
// parameter, so PostgreSQL can prove the partial claim index (migration 37)
// applies in generic plans too.
const feedClaimable = "state IN ('pending','processing') AND (next_attempt_at IS NULL OR next_attempt_at <= ?) AND (lease_expires_at IS NULL OR lease_expires_at <= ?)"

// feedClaimRange is added on PostgreSQL only. It is implied by feedClaimable for
// rows with next_attempt_at (an exact restatement) and merely tolerates up to
// feedClaimSkew of app/DB clock skew for fresh rows (created_at is written by
// the application). Its value is that it turns "retry-scheduled in the future"
// rows into an index range stop instead of rows scanned and filtered on every
// idle poll. SQLite compares timestamps as text, so it is not used there.
const feedClaimRange = " AND COALESCE(next_attempt_at, created_at) <= ?"
const feedClaimSkew = 5 * time.Minute

// feedClaimOrder matches the expression index created by migration 37.
const feedClaimOrder = "COALESCE(next_attempt_at, created_at) ASC, id ASC"

const feedCASGuard = "id = ? AND lease_generation = ? AND state IN ('pending','processing') AND (lease_expires_at IS NULL OR lease_expires_at <= ?)"

func claimFeed(conn *gorm.DB, model any, limit int, ttl time.Duration, events bool) ([]FeedEvent, []FeedBatch, error) {
	if limit < 1 || limit > 256 || ttl < time.Second || ttl > 5*time.Minute {
		return nil, nil, fmt.Errorf("invalid feed claim limits")
	}
	now, err := FeedDatabaseTime(conn)
	if err != nil {
		return nil, nil, err
	}
	postgres := dialect.IsPostgres(conn)
	claimable := func(db *gorm.DB) *gorm.DB {
		if postgres {
			return db.Where(feedClaimable+feedClaimRange, now, now, now.Add(feedClaimSkew))
		}
		return db.Where(feedClaimable, now, now)
	}
	if postgres {
		// "No work" is the common case. Detect it with one cheap index probe
		// outside a transaction: no BEGIN/COMMIT, no row locks, no UPDATE.
		var probe []uint64
		if err := claimable(conn.Model(model)).Order(feedClaimOrder).Limit(1).Pluck("id", &probe).Error; err != nil || len(probe) == 0 {
			return nil, nil, err
		}
	}
	var eventRows []FeedEvent
	var batchRows []FeedBatch
	err = conn.Transaction(func(tx *gorm.DB) error {
		// Candidate discovery never reads the recipient/metadata JSON.
		discover := claimable(tx.Model(model).Select("id, lease_generation")).Order(feedClaimOrder)
		if postgres {
			// Lock exactly the rows we are about to claim; rows another
			// claimer holds are skipped, so concurrent workers never queue on
			// (or race for) the same head-of-queue row.
			discover = discover.Limit(limit).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		} else {
			// MySQL's filesort would lock every scanned row if the ordered
			// query carried FOR UPDATE, and SQLite has no row locks: read a
			// small candidate window and lock/CAS each row by primary key.
			discover = discover.Limit(min(256, max(8, limit*4)))
		}
		if events {
			err = discover.Find(&eventRows).Error
		} else {
			err = discover.Find(&batchRows).Error
		}
		if err != nil {
			return err
		}
		n := len(eventRows)
		if !events {
			n = len(batchRows)
		}
		claimedEvents := []FeedEvent{}
		claimedBatches := []FeedBatch{}
		for i := 0; i < n; i++ {
			var id, generation uint64
			if events {
				id, generation = eventRows[i].ID, eventRows[i].LeaseGeneration
			} else {
				id, generation = batchRows[i].ID, batchRows[i].LeaseGeneration
			}
			if !postgres && tx.Dialector.Name() != "sqlite" {
				// Losing the race is normal: Find+RowsAffected, never Take/First,
				// so GORM does not log "record not found".
				var locked []uint64
				result := tx.Model(model).Where(feedCASGuard, id, generation, now).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Limit(1).Pluck("id", &locked)
				if result.Error != nil {
					return result.Error
				}
				if len(locked) == 0 {
					continue
				}
			}
			token, err := newFeedToken()
			if err != nil {
				return err
			}
			result := tx.Model(model).Where(feedCASGuard, id, generation, now).Updates(map[string]any{"state": "processing", "lease_generation": generation + 1, "lease_token": token, "lease_expires_at": now.Add(ttl), "next_attempt_at": nil})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				continue
			}
			// Read the complete payload only after winning the row.
			if events {
				var full []FeedEvent
				if err := tx.Where("id = ?", id).Limit(1).Find(&full).Error; err != nil {
					return err
				}
				claimedEvents = append(claimedEvents, full...)
			} else {
				var full []FeedBatch
				if err := tx.Where("id = ?", id).Limit(1).Find(&full).Error; err != nil {
					return err
				}
				claimedBatches = append(claimedBatches, full...)
			}
			if len(claimedEvents)+len(claimedBatches) >= limit {
				break
			}
		}
		eventRows = claimedEvents
		batchRows = claimedBatches
		return nil
	})
	return eventRows, batchRows, err
}
func ClaimFeedEvents(conn *gorm.DB, limit int, ttl time.Duration) ([]FeedEvent, error) {
	events, _, err := claimFeed(conn, &FeedEvent{}, limit, ttl, true)
	return events, err
}
func ClaimFeedBatches(conn *gorm.DB, limit int, ttl time.Duration) ([]FeedBatch, error) {
	_, batches, err := claimFeed(conn, &FeedBatch{}, limit, ttl, false)
	return batches, err
}
func feedLeaseQuery(conn *gorm.DB, model any, lease FeedLeaseRef, now time.Time) *gorm.DB {
	return conn.Model(model).Where("id = ? AND state = ? AND lease_token = ? AND lease_generation = ? AND lease_expires_at > "+feedClockSQL(conn), lease.ID, "processing", lease.Token, lease.Generation)
}
func feedLeaseUpdate(conn *gorm.DB, model any, lease FeedLeaseRef, values map[string]any) error {
	now, err := FeedDatabaseTime(conn)
	if err != nil {
		return err
	}
	result := feedLeaseQuery(conn, model, lease, now).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrFeedLeaseLost
	}
	return nil
}
func renewFeed(conn *gorm.DB, model any, lease FeedLeaseRef, ttl time.Duration) error {
	if ttl < time.Second || ttl > 5*time.Minute {
		return fmt.Errorf("invalid feed lease duration")
	}
	now, err := FeedDatabaseTime(conn)
	if err != nil {
		return err
	}
	return feedLeaseUpdate(conn, model, lease, map[string]any{"lease_expires_at": now.Add(ttl)})
}
func RenewFeedEvent(conn *gorm.DB, lease FeedLeaseRef, ttl time.Duration) error {
	return renewFeed(conn, &FeedEvent{}, lease, ttl)
}
func RenewFeedBatch(conn *gorm.DB, lease FeedLeaseRef, ttl time.Duration) error {
	return renewFeed(conn, &FeedBatch{}, lease, ttl)
}
func CompleteFeedBatch(conn *gorm.DB, lease FeedLeaseRef) error {
	return feedLeaseUpdate(conn, &FeedBatch{}, lease, map[string]any{"state": "complete", "lease_expires_at": nil, "lease_token": "", "last_error_code": ""})
}

// PersistFeedBatches atomically registers immutable recipient batches and moves
// the parent's per-audience scan cursor. A stale scanner cannot add child work.
func PersistFeedBatches(conn *gorm.DB, lease FeedLeaseRef, batches []FeedBatch, cursor, upper map[string]uint64, complete bool) error {
	return conn.Transaction(func(tx *gorm.DB) error {
		now, err := FeedDatabaseTime(tx)
		if err != nil {
			return err
		}
		var parent FeedEvent
		if err := feedLeaseQuery(tx, &FeedEvent{}, lease, now).Clauses(clause.Locking{Strength: "UPDATE"}).First(&parent).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return ErrFeedLeaseLost
			}
			return err
		}
		if parent.ScanComplete {
			return ErrFeedNotComplete
		}
		for kind, old := range parent.ScanCursor {
			if cursor[kind] < old {
				return fmt.Errorf("feed cursor cannot move backwards")
			}
		}
		for kind, old := range parent.ScanUpperBound {
			if upper[kind] != old {
				return fmt.Errorf("feed scan upper bound is immutable")
			}
		}
		for i := range batches {
			b := &batches[i]
			if b.EventID != 0 && b.EventID != lease.ID {
				return fmt.Errorf("incorrect feed batch parent")
			}
			if len(b.Recipients) > 1024 || b.TargetKind == "" {
				return fmt.Errorf("invalid feed batch")
			}
			b.EventID = lease.ID
			b.ID = 0
			b.FeedLease = FeedLease{}
			if err := tx.Create(b).Error; err != nil {
				return err
			}
		}
		cursorJSON, err := json.Marshal(cursor)
		if err != nil {
			return err
		}
		upperJSON, err := json.Marshal(upper)
		if err != nil {
			return err
		}
		return feedLeaseUpdate(tx, &FeedEvent{}, lease, map[string]any{"scan_cursor": string(cursorJSON), "scan_upper_bound": string(upperJSON), "scan_complete": complete})
	})
}
func CompleteFeedEvent(conn *gorm.DB, lease FeedLeaseRef) error {
	return conn.Transaction(func(tx *gorm.DB) error {
		now, err := FeedDatabaseTime(tx)
		if err != nil {
			return err
		}
		var event FeedEvent
		if err = feedLeaseQuery(tx, &FeedEvent{}, lease, now).Clauses(clause.Locking{Strength: "UPDATE"}).First(&event).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return ErrFeedLeaseLost
			}
			return err
		}
		if !event.ScanComplete {
			return ErrFeedNotComplete
		}
		var terminal int64
		if err = tx.Model(&FeedBatch{}).Where("event_id = ? AND state IN ?", event.ID, []string{"failed", "cancelled"}).Count(&terminal).Error; err != nil {
			return err
		}
		if terminal > 0 {
			return ErrFeedDependencyFailed
		}
		var remaining int64
		if err = tx.Model(&FeedBatch{}).Where("event_id = ? AND state <> ?", lease.ID, "complete").Count(&remaining).Error; err != nil {
			return err
		}
		if remaining != 0 {
			return ErrFeedNotComplete
		}
		return feedLeaseUpdate(tx, &FeedEvent{}, lease, map[string]any{"state": "complete", "lease_expires_at": nil, "lease_token": "", "last_error_code": ""})
	})
}
func retryFeed(conn *gorm.DB, model any, lease FeedLeaseRef, code string, delay time.Duration, terminal bool) error {
	if len(code) > 64 || delay < 0 {
		return fmt.Errorf("invalid feed retry")
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return fmt.Errorf("feed errors must be safe fixed codes")
		}
	}
	now, err := FeedDatabaseTime(conn)
	if err != nil {
		return err
	}
	state := "pending"
	if terminal {
		state = "failed"
	}
	return feedLeaseUpdate(conn, model, lease, map[string]any{"state": state, "attempts": gorm.Expr("attempts + 1"), "next_attempt_at": now.Add(delay), "lease_expires_at": nil, "lease_token": "", "last_error_code": code})
}
func RetryFeedEvent(conn *gorm.DB, lease FeedLeaseRef, code string, delay time.Duration, terminal bool) error {
	return retryFeed(conn, &FeedEvent{}, lease, code, delay, terminal)
}
func RetryFeedBatch(conn *gorm.DB, lease FeedLeaseRef, code string, delay time.Duration, terminal bool) error {
	return retryFeed(conn, &FeedBatch{}, lease, code, delay, terminal)
}

// YieldFeedEvent releases a normal scan slice without charging a failure attempt.
func YieldFeedEvent(conn *gorm.DB, lease FeedLeaseRef, delay time.Duration) error {
	if delay < 0 {
		return fmt.Errorf("invalid yield delay")
	}
	now, err := FeedDatabaseTime(conn)
	if err != nil {
		return err
	}
	return feedLeaseUpdate(conn, &FeedEvent{}, lease, map[string]any{"state": "pending", "lease_token": "", "lease_expires_at": nil, "next_attempt_at": now.Add(delay)})
}
