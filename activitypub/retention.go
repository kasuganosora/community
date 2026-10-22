package activitypub

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
)

// Retention sweep for federation bookkeeping tables.
//
// SCOPE: this job only clears remote/log-type system tables:
//   - activitypub_processed_activities (inbox de-duplication records), and
//   - activitypub_deliveries rows whose status is "delivered".
//
// It never touches local user content (statuses, notifications, media,
// favourites, bookmarks, accounts, ...). Pending, processing and failed
// deliveries are never deleted (failed rows can still be retried by
// administrators or the Matrix bridge).
//
// Dedup semantics: a processed-activity record older than the retention can
// be removed, so an activity re-delivered after that long would be processed
// again. Remote servers stop retrying within days, and the handlers are
// idempotent on object URIs (Create/Announce/Like look up existing rows), so
// this is acceptable; keep AP_PROCESSED_ACTIVITY_RETENTION_DAYS well above
// any peer's retry horizon.
//
// Rows are removed in small id batches (select ids, delete by id) with a
// short pause between batches, so no statement holds long locks; the same
// SQL works on PostgreSQL, MySQL and SQLite.

const (
	defaultProcessedActivityRetentionDays = 90
	defaultDeliveryRetentionDays          = 30
	retentionBatchSize                    = 5000
	retentionBatchPause                   = 200 * time.Millisecond
	retentionSweepInterval                = time.Hour
	retentionMaxBatchesPerSweep           = 200
)

type retentionRule struct {
	table string
	where string // extra condition besides the time cutoff; may be empty
	args  []any
	col   string // timestamp column compared with the cutoff
	keep  time.Duration
}

func retentionDays(raw string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return def
	}
	return n
}

// retentionRules returns the configured rules; a retention of 0 days
// disables that rule.
func retentionRules() []retentionRule {
	var rules []retentionRule
	if d := retentionDays(config.C.APProcessedActivityRetentionDays, defaultProcessedActivityRetentionDays); d > 0 {
		rules = append(rules, retentionRule{table: (ProcessedActivity{}).TableName(), col: "created_at", keep: time.Duration(d) * 24 * time.Hour})
	}
	if d := retentionDays(config.C.APDeliveryRetentionDays, defaultDeliveryRetentionDays); d > 0 {
		rules = append(rules, retentionRule{table: (Delivery{}).TableName(), where: "status = ?", args: []any{string(DeliveryStatusDelivered)},
			col: "updated_at", keep: time.Duration(d) * 24 * time.Hour})
	}
	return rules
}

// sweepRetention deletes expired rows for every rule and returns the number
// of rows removed per table.
func sweepRetention(ctx context.Context, conn *gorm.DB, rules []retentionRule, now time.Time, batch int, pause time.Duration) map[string]int64 {
	removed := map[string]int64{}
	for _, r := range rules {
		if !conn.Migrator().HasTable(r.table) {
			continue
		}
		cutoff := now.Add(-r.keep)
		for i := 0; i < retentionMaxBatchesPerSweep; i++ {
			if ctx.Err() != nil {
				return removed
			}
			q := conn.WithContext(ctx).Table(r.table).Where(r.col+" < ?", cutoff)
			if r.where != "" {
				q = q.Where(r.where, r.args...)
			}
			var ids []uint64
			if err := q.Order("id").Limit(batch).Pluck("id", &ids).Error; err != nil {
				log.Warn("Retention: selecting %s: %v", r.table, err)
				break
			}
			if len(ids) == 0 {
				break
			}
			del := conn.WithContext(ctx).Table(r.table).Where("id IN ?", ids).Where(r.col+" < ?", cutoff)
			if r.where != "" {
				del = del.Where(r.where, r.args...)
			}
			res := del.Delete(nil)
			if res.Error != nil {
				log.Warn("Retention: deleting from %s: %v", r.table, res.Error)
				break
			}
			removed[r.table] += res.RowsAffected
			if len(ids) < batch {
				break
			}
			select {
			case <-ctx.Done():
				return removed
			case <-time.After(pause):
			}
		}
	}
	return removed
}

// RetentionWorker runs sweepRetention periodically.
type RetentionWorker struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// StartRetentionWorker starts the hourly federation bookkeeping cleanup.
func StartRetentionWorker() *RetentionWorker {
	ctx, cancel := context.WithCancel(context.Background())
	w := &RetentionWorker{cancel: cancel}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		timer := time.NewTimer(5 * time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			if conn := db.GetDB(); conn != nil {
				for table, n := range sweepRetention(ctx, conn, retentionRules(), time.Now(), retentionBatchSize, retentionBatchPause) {
					if n > 0 {
						log.Info("Retention: removed %d expired rows from %s", n, table)
					}
				}
			}
			timer.Reset(retentionSweepInterval)
		}
	}()
	return w
}

// Stop stops the worker and waits for an in-flight sweep to end.
func (w *RetentionWorker) Stop() {
	w.cancel()
	w.wg.Wait()
}
