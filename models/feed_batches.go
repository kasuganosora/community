package models

import (
	"encoding/json"
	"fmt"
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
	EventID    uint64          `gorm:"not null;index;uniqueIndex:idx_feed_batch_identity,priority:1"`
	TargetKind string          `gorm:"size:24;not null;uniqueIndex:idx_feed_batch_identity,priority:2"`
	BatchNo    uint64          `gorm:"not null;uniqueIndex:idx_feed_batch_identity,priority:3"`
	Recipients []FeedRecipient `gorm:"type:json;serializer:json;not null"`
	FeedLease  `gorm:"embedded"`
}

func (FeedBatch) TableName() string { return "feed_batches" }
func (b FeedBatch) LeaseRef() FeedLeaseRef {
	return FeedLeaseRef{b.ID, b.LeaseToken, b.LeaseGeneration}
}

func claimFeed(conn *gorm.DB, model any, limit int, ttl time.Duration, events bool) ([]FeedEvent, []FeedBatch, error) {
	if limit < 1 || limit > 256 || ttl < time.Second || ttl > 5*time.Minute {
		return nil, nil, fmt.Errorf("invalid feed claim limits")
	}
	var eventRows []FeedEvent
	var batchRows []FeedBatch
	err := conn.Transaction(func(tx *gorm.DB) error {
		now, err := FeedDatabaseTime(tx)
		if err != nil {
			return err
		}
		q := tx.Model(model).Select("id, lease_generation").Where("state IN ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?) AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", []string{"pending", "processing"}, now, now).Order("COALESCE(next_attempt_at, created_at) ASC, id ASC").Limit(256)
		if events {
			err = q.Find(&eventRows).Error
		} else {
			err = q.Find(&batchRows).Error
		}
		if err != nil {
			return err
		}
		claimedEvents := []FeedEvent{}
		claimedBatches := []FeedBatch{}
		n := len(eventRows)
		if !events {
			n = len(batchRows)
		}
		for i := 0; i < n; i++ {
			var id uint64
			var lease *FeedLease
			if events {
				id = eventRows[i].ID
				lease = &eventRows[i].FeedLease
			} else {
				id = batchRows[i].ID
				lease = &batchRows[i].FeedLease
			}
			// Lock only this primary-key row. Locking the ordered discovery
			// query makes MySQL filesort lock every scanned row, starving
			// concurrent claimers even with SKIP LOCKED.
			if tx.Dialector.Name() != "sqlite" {
				var locked struct{ ID uint64 }
				result := tx.Model(model).Select("id").Where("id = ? AND lease_generation = ? AND state IN ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", id, lease.LeaseGeneration, []string{"pending", "processing"}, now).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Take(&locked)
				if result.Error == gorm.ErrRecordNotFound {
					continue
				}
				if result.Error != nil {
					return result.Error
				}
			}

			token, err := newFeedToken()
			if err != nil {
				return err
			}
			expires := now.Add(ttl)
			result := tx.Model(model).Where("id = ? AND lease_generation = ? AND state IN ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", id, lease.LeaseGeneration, []string{"pending", "processing"}, now).Updates(map[string]any{"state": "processing", "lease_generation": lease.LeaseGeneration + 1, "lease_token": token, "lease_expires_at": expires, "next_attempt_at": nil})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				continue
			}
			// Candidate discovery intentionally excludes recipient/metadata JSON.
			// Read the complete payload only after winning the row's CAS.
			if events {
				var full FeedEvent
				if err := tx.First(&full, id).Error; err != nil {
					return err
				}
				claimedEvents = append(claimedEvents, full)
			} else {
				var full FeedBatch
				if err := tx.First(&full, id).Error; err != nil {
					return err
				}
				claimedBatches = append(claimedBatches, full)
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
