package activitypub

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AnnounceRetry is a boost whose original object could not be fetched when
// the Announce arrived. The inbox acknowledges the Announce and the
// AnnounceRetryWorker retries the fetch with backoff.
type AnnounceRetry struct {
	ID            uint64     `gorm:"primaryKey;autoIncrement"`
	ActivityID    string     `gorm:"type:varchar(1024);not null;uniqueIndex:idx_announce_retry_activity,length:191"`
	Actor         string     `gorm:"type:varchar(1024);not null"`
	ObjectURI     string     `gorm:"column:object_uri;type:varchar(2048);not null"`
	Published     string     `gorm:"type:varchar(64);not null;default:''"`
	Attempts      int        `gorm:"not null;default:0"`
	NextAttemptAt time.Time  `gorm:"not null;index"`
	LastError     string     `gorm:"type:text"`
	CreatedAt     *time.Time `gorm:"autoCreateTime"`
}

func (AnnounceRetry) TableName() string { return "activitypub_announce_retries" }

// announceRetryBackoff is the delay before each retry; its length caps the
// attempts (five retries over roughly a day).
var announceRetryBackoff = []time.Duration{5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 15 * time.Hour}

// announceFailureClass names why an announced object could not be resolved,
// for logs: "gone" (410, permanent), "not_found", "unauthorized" (401/403
// even when signed), "server_error" (5xx/429/408), "network" (timeouts,
// connection errors) or "invalid" (malformed or mismatched documents).
func announceFailureClass(err error) string {
	switch status := fetchStatusCode(err); {
	case status == http.StatusGone:
		return "gone"
	case status == http.StatusNotFound:
		return "not_found"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "unauthorized"
	case status != 0 && isTransientFetchError(err):
		return "server_error"
	case status != 0:
		return fmt.Sprintf("http_%d", status)
	case isTransientFetchError(err):
		return "network"
	}
	return "invalid"
}

func truncateError(err error) string {
	msg := err.Error()
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	return msg
}

// enqueueAnnounceRetry records a failed boost unless the origin said the
// object is gone for good (410).
func enqueueAnnounceRetry(ctx context.Context, activityID, actor, object, published string, cause error) {
	if activityID == "" || fetchStatusCode(cause) == http.StatusGone {
		log.Warn("Announce %s by %s of %s dropped (class=%s): %v", activityID, actor, object, announceFailureClass(cause), cause)
		return
	}
	row := AnnounceRetry{
		ActivityID: activityID, Actor: actor, ObjectURI: object, Published: published,
		NextAttemptAt: time.Now().Add(announceRetryBackoff[0]), LastError: truncateError(cause),
	}
	if err := activityDB(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		log.Warn("Could not queue announce %s by %s for retry: %v", activityID, actor, err)
	}
}

// ProcessAnnounceRetries retries up to limit due boosts.
func ProcessAnnounceRetries(ctx context.Context, now time.Time, limit int) error {
	conn := db.GetDB().WithContext(ctx)
	var rows []AnnounceRetry
	if err := conn.Where("next_attempt_at <= ?", now).Order("next_attempt_at ASC").Limit(limit).Find(&rows).Error; err != nil {
		return err
	}
	for i := range rows {
		retryAnnounce(ctx, conn, &rows[i], now)
	}
	return nil
}

func retryAnnounce(ctx context.Context, conn *gorm.DB, row *AnnounceRetry, now time.Time) {
	ctx = context.WithValue(ctx, actorKey, row.Actor)
	err := applyRetriedAnnounce(ctx, row)
	if err == nil {
		conn.Delete(row)
		log.Info("Announce %s by %s applied after %d retries", row.ActivityID, row.Actor, row.Attempts+1)
		return
	}
	row.Attempts++
	if fetchStatusCode(err) == http.StatusGone || row.Attempts >= len(announceRetryBackoff) {
		conn.Delete(row)
		log.Warn("Giving up on announce %s by %s of %s after %d retries (class=%s): %v", row.ActivityID, row.Actor, row.ObjectURI, row.Attempts, announceFailureClass(err), err)
		return
	}
	row.NextAttemptAt = now.Add(announceRetryBackoff[row.Attempts])
	conn.Model(row).Updates(map[string]any{
		"attempts": row.Attempts, "next_attempt_at": row.NextAttemptAt, "last_error": truncateError(err),
	})
	log.Info("Announce %s by %s of %s retry %d failed (class=%s), next at %s: %v", row.ActivityID, row.Actor, row.ObjectURI, row.Attempts, announceFailureClass(err), row.NextAttemptAt.Format(time.RFC3339), err)
}

func applyRetriedAnnounce(ctx context.Context, row *AnnounceRetry) error {
	remoteAccount, err := getOrCreateRemoteAccountContext(ctx, row.Actor)
	if err != nil {
		return err
	}
	original, err := resolveAnnouncedStatus(ctx, row.Actor, row.ObjectURI, nil)
	if err != nil {
		return err
	}
	if original.LocalOnly {
		return nil
	}
	_, err = createRemoteReblog(ctx, remoteAccount, original, row.ActivityID, row.Published)
	return err
}

// AnnounceRetryWorker periodically processes queued boosts.
type AnnounceRetryWorker struct {
	stop chan struct{}
	done chan struct{}
}

func StartAnnounceRetryWorker() *AnnounceRetryWorker {
	w := &AnnounceRetryWorker{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-ticker.C:
			}
			if err := ProcessAnnounceRetries(context.Background(), time.Now(), 50); err != nil {
				log.Warn("Announce retry worker: %v", err)
			}
		}
	}()
	return w
}

func (w *AnnounceRetryWorker) Stop() { close(w.stop); <-w.done }
