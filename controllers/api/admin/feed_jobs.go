package admin

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errFeedJobConflict = errors.New("feed job changed or has running dependencies")
var feedJobErrorCode = regexp.MustCompile(`^[a-z0-9_]{0,64}$`)

func safeFeedCode(code string) string {
	if !feedJobErrorCode.MatchString(code) {
		return "operation_failed"
	}
	return code
}

func feedLeaseJSON(id uint64, kind string, lease models.FeedLease, now time.Time) gin.H {
	return gin.H{"id": fmt.Sprint(id), "kind": kind, "state": lease.State, "attempts": lease.Attempts,
		"last_error": safeFeedCode(lease.LastErrorCode), "next_attempt_at": lease.NextAttemptAt,
		"created_at": lease.CreatedAt, "lease_expires_at": lease.LeaseExpiresAt,
		"can_retry": (lease.State == "pending" || lease.State == "failed") && (lease.LeaseExpiresAt == nil || !lease.LeaseExpiresAt.After(now))}
}

// feedJobs exposes operational metadata only: no recipients, private payload,
// relationship snapshots, lease bearer tokens or raw error strings.
func feedJobs(c *gin.Context, conn *gorm.DB, kind, state string, page, limit int) {
	validState := state == "" || state == "pending" || state == "processing" || state == "failed" || state == "complete" || state == "cancelled"
	if !validState {
		c.JSON(422, gin.H{"error": "invalid feed queue state"})
		return
	}
	now, err := models.FeedDatabaseTime(conn)
	if operationError(c, err) {
		return
	}
	items := []gin.H{}
	var total int64
	if kind == "feed-event" {
		q := conn.Model(&models.FeedEvent{})
		if state != "" {
			q = q.Where("state = ?", state)
		}
		if operationError(c, q.Count(&total).Error) {
			return
		}
		var rows []models.FeedEvent
		if operationError(c, q.Select("id, kind, object_type, object_id, state, attempts, last_error_code, next_attempt_at, created_at, lease_expires_at, scan_complete").Order("id DESC").Offset((page-1)*limit).Limit(limit).Find(&rows).Error) {
			return
		}
		for _, row := range rows {
			item := feedLeaseJSON(row.ID, kind, row.FeedLease, now)
			item["event"] = row.Kind
			item["object_id"] = fmt.Sprint(row.ObjectID)
			item["scan_complete"] = row.ScanComplete
			item["requires_reconciliation"] = !row.CreatedAt.After(now.Add(-7 * 24 * time.Hour))
			items = append(items, item)
		}
	} else {
		q := conn.Model(&models.FeedBatch{}).Joins("JOIN feed_events ON feed_events.id = feed_batches.event_id")
		if state != "" {
			q = q.Where("feed_batches.state = ?", state)
		}
		if operationError(c, q.Count(&total).Error) {
			return
		}
		var rows []struct {
			models.FeedBatch
			EventCreatedAt time.Time
		}
		if operationError(c, q.Select("feed_batches.id, feed_batches.event_id, feed_batches.target_kind, feed_batches.batch_no, feed_batches.state, feed_batches.attempts, feed_batches.last_error_code, feed_batches.next_attempt_at, feed_batches.created_at, feed_batches.lease_expires_at, feed_events.created_at AS event_created_at").Order("feed_batches.id DESC").Offset((page-1)*limit).Limit(limit).Scan(&rows).Error) {
			return
		}
		for _, row := range rows {
			item := feedLeaseJSON(row.ID, kind, row.FeedLease, now)
			item["event"] = row.TargetKind
			item["parent_id"] = fmt.Sprint(row.EventID)
			item["batch_no"] = row.BatchNo
			item["requires_reconciliation"] = !row.EventCreatedAt.After(now.Add(-7 * 24 * time.Hour))
			items = append(items, item)
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "limit": limit})
}

func feedJobMutable(lease models.FeedLease, now time.Time) bool {
	return (lease.State == "pending" || lease.State == "failed") && (lease.LeaseExpiresAt == nil || !lease.LeaseExpiresAt.After(now))
}

func updateFeedJobLease(tx *gorm.DB, model any, id uint64, lease models.FeedLease, state string, now time.Time) error {
	result := tx.Model(model).Where("id = ? AND state = ? AND lease_generation = ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", id, lease.State, lease.LeaseGeneration, now).
		Updates(map[string]any{"state": state, "lease_generation": lease.LeaseGeneration + 1, "lease_token": "", "lease_expires_at": nil, "next_attempt_at": nil, "last_error_code": ""})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errFeedJobConflict
	}
	return nil
}

// Any batch operation acts on the whole unfinished dependency group. Taking
// parent then child locks fences scanners, claimers and competing administrators.
// Complete children remain immutable; no operation can make a partial parent complete.
func mutateFeedJob(conn *gorm.DB, kind string, id uint64, action string) (bool, error) {
	reconciled := false
	err := conn.Transaction(func(tx *gorm.DB) error {
		parentID := id
		if kind == "feed-batch" {
			var batch models.FeedBatch
			if err := tx.Select("id, event_id").First(&batch, id).Error; err != nil {
				return err
			}
			parentID = batch.EventID
		}
		var parent models.FeedEvent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id, state, lease_generation, lease_expires_at, created_at").First(&parent, parentID).Error; err != nil {
			return err
		}
		now, err := models.FeedDatabaseTime(tx)
		if err != nil {
			return err
		}
		if !feedJobMutable(parent.FeedLease, now) {
			return errFeedJobConflict
		}
		var children []models.FeedBatch
		if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id, event_id, state, lease_generation, lease_expires_at").Where("event_id = ?", parentID).Order("id").Find(&children).Error; err != nil {
			return err
		}
		found := kind == "feed-event"
		for _, child := range children {
			if child.ID == id && kind == "feed-batch" {
				found = true
				if !feedJobMutable(child.FeedLease, now) {
					return errFeedJobConflict
				}
			}
			if child.State == "complete" {
				continue
			}
			if !feedJobMutable(child.FeedLease, now) {
				return errFeedJobConflict
			}
		}
		if !found {
			return gorm.ErrRecordNotFound
		}
		state := "pending"
		if action == "cancel" || !parent.CreatedAt.After(now.Add(-7*24*time.Hour)) {
			state = "cancelled"
		}
		for _, child := range children {
			if child.State == "complete" {
				continue
			}
			if err = updateFeedJobLease(tx, &models.FeedBatch{}, child.ID, child.FeedLease, state, now); err != nil {
				return err
			}
		}
		if err = updateFeedJobLease(tx, &models.FeedEvent{}, parentID, parent.FeedLease, state, now); err != nil {
			return err
		}
		if action == "retry" && state == "cancelled" {
			// New immutable request reads current SQL audiences. Never clone old Create,
			// old recipient snapshots or notifications after the replay horizon.
			if err = models.QueueFeedInvalidation(tx, models.FeedEventMetadata{Reason: "admin_reconcile"}); err != nil {
				return err
			}
			reconciled = true
		}
		return nil
	})
	return reconciled, err
}

func feedJobAction(c *gin.Context, conn *gorm.DB, kind string, id uint64, action string) {
	reconciled, err := mutateFeedJob(conn, kind, id, action)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(404, gin.H{"error": "job not found"})
		return
	}
	if errors.Is(err, errFeedJobConflict) {
		c.JSON(409, gin.H{"error": "event or dependent batch is running, completed, cancelled or changed; refresh before retrying"})
		return
	}
	if operationError(c, err) {
		return
	}
	c.JSON(200, gin.H{"updated": true, "reconciled": reconciled})
}
