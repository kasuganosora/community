package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// InboxJob is a verified inbound activity waiting for an inbox worker. The
// inbox only checks the HTTP signature, actor and domain policy, stores the
// raw body here and answers 202; workers apply it with the same code the
// inbox used to run inline. Rows are deleted once applied (the dedup record
// is activitypub_processed_activities); failures retry with backoff and end
// in state "dead" for inspection.
type InboxJob struct {
	ID              uint64     `gorm:"primaryKey;autoIncrement"`
	ActivityID      string     `gorm:"type:varchar(1024);not null;uniqueIndex:idx_inbox_jobs_activity,length:191"`
	ActivityType    string     `gorm:"type:varchar(64);not null;default:''"`
	Actor           string     `gorm:"type:varchar(1024);not null"`
	TargetAccountID *uint64    `gorm:""`
	RelayID         *uint64    `gorm:""`
	Body            string     `gorm:"size:1048577;not null"`
	Shard           int        `gorm:"not null;default:0"`
	State           string     `gorm:"type:varchar(16);not null;default:'pending';index:idx_inbox_jobs_due,priority:1"`
	NextAttemptAt   time.Time  `gorm:"not null;index:idx_inbox_jobs_due,priority:2"`
	LeaseUntil      *time.Time `gorm:""`
	Attempts        int        `gorm:"not null;default:0"`
	LastStatus      int        `gorm:"not null;default:0"`
	LastError       string     `gorm:"type:text"`
	CreatedAt       *time.Time `gorm:"autoCreateTime"`
}

func (InboxJob) TableName() string { return "activitypub_inbox_jobs" }

const (
	inboxJobPending    = "pending"
	inboxJobProcessing = "processing"
	inboxJobDead       = "dead"
	inboxShards        = 1024
	inboxLease         = 5 * time.Minute
	inboxClaimBatch    = 20
	inboxDeadRetention = 7 * 24 * time.Hour
)

// inboxRetryBackoff is the delay before each retry; its length caps attempts.
var inboxRetryBackoff = []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour}

// inboxPollInterval is how often an idle worker looks for due jobs (new
// jobs also wake their worker directly).
var inboxPollInterval = time.Second

var activeInboxQueue atomic.Pointer[InboxQueue]

func inboxShard(actor string) int { return int(crc32.ChecksumIEEE([]byte(actor)) % inboxShards) }

// enqueueInboxActivity stores a verified activity for the workers and
// answers 202. It returns false (process inline) when no queue is running
// or the row cannot be stored.
func enqueueInboxActivity(c *gin.Context, body []byte, activityID, actor string, target *models.Account, relay *models.Relay) bool {
	q := activeInboxQueue.Load()
	if q == nil || activityID == "" {
		return false
	}
	ctx := c.Request.Context()
	if alreadyProcessedActivity(ctx, activityID) {
		c.JSON(http.StatusOK, gin.H{"status": "accepted"})
		return true
	}
	var activity struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(body, &activity)
	job := InboxJob{
		ActivityID: activityID, ActivityType: clip(activity.Type, 64), Actor: actor, Body: string(body),
		Shard: inboxShard(actor), State: inboxJobPending, NextAttemptAt: time.Now(),
	}
	if target != nil && target.Id != 0 {
		job.TargetAccountID = &target.Id
	}
	if relay != nil && relay.ID != 0 {
		id := uint64(relay.ID)
		job.RelayID = &id
	}
	// Federation tier: the sender redelivers what a crash loses, so the
	// enqueue does not wait for the WAL flush.
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		dialect.AsyncCommit(tx)
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "activity_id"}}, DoNothing: true}).Create(&job).Error
	})
	if err != nil {
		log.Warn("Inbox queue: storing %s %s by %s failed, processing inline: %v", job.ActivityType, activityID, actor, err)
		return false
	}
	q.wake(job.Shard)
	c.JSON(http.StatusAccepted, gin.H{"status": "queued"})
	return true
}

// InboxQueue runs the inbox workers. Each worker owns the shards
// shard % workers == index and applies their jobs in arrival order, so the
// activities of one actor (Create, then Update/Delete/Undo) stay ordered.
type InboxQueue struct {
	workers int
	wakeups []chan struct{}
	stop    chan struct{}
	wg      sync.WaitGroup
}

func inboxWorkerCount() int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("INBOX_WORKERS"))); err == nil && n > 0 && n <= 64 {
		return n
	}
	return 4
}

// StartInboxQueue starts the workers unless INBOX_QUEUE=off or the table is
// missing; then the inbox keeps processing inline (returns nil).
func StartInboxQueue() *InboxQueue {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("INBOX_QUEUE")), "off") {
		log.Info("Inbox queue disabled (INBOX_QUEUE=off); processing activities inline")
		return nil
	}
	conn := db.GetDB()
	if conn == nil || !conn.Migrator().HasTable(&InboxJob{}) {
		log.Warn("Inbox queue table missing; processing activities inline until migrations run")
		return nil
	}
	return startInboxQueue(inboxWorkerCount())
}

func startInboxQueue(workers int) *InboxQueue {
	q := &InboxQueue{workers: workers, stop: make(chan struct{})}
	for i := 0; i < workers; i++ {
		q.wakeups = append(q.wakeups, make(chan struct{}, 1))
		q.wg.Add(1)
		go q.run(i)
	}
	activeInboxQueue.Store(q)
	return q
}

// Stop stops accepting new jobs (the inbox processes inline again) and
// waits for the workers to finish their current job.
func (q *InboxQueue) Stop() {
	if q == nil {
		return
	}
	activeInboxQueue.CompareAndSwap(q, nil)
	close(q.stop)
	q.wg.Wait()
}

func (q *InboxQueue) wake(shard int) {
	select {
	case q.wakeups[shard%q.workers] <- struct{}{}:
	default:
	}
}

func (q *InboxQueue) run(index int) {
	defer q.wg.Done()
	ticker := time.NewTicker(inboxPollInterval)
	defer ticker.Stop()
	lastPrune := time.Time{}
	for {
		for q.processDue(index) > 0 {
			select {
			case <-q.stop:
				return
			default:
			}
		}
		if index == 0 && time.Since(lastPrune) > time.Hour {
			lastPrune = time.Now()
			pruneDeadInboxJobs()
		}
		select {
		case <-q.stop:
			return
		case <-ticker.C:
		case <-q.wakeups[index]:
		}
	}
}

// processDue claims and applies due jobs of one worker; returns how many.
func (q *InboxQueue) processDue(index int) int {
	return processDueInboxJobs(context.Background(), q.workers, index, time.Now())
}

func processDueInboxJobs(ctx context.Context, workers, index int, now time.Time) int {
	conn := db.GetDB().WithContext(ctx)
	if dialect.IsPostgres(conn) {
		return processDueInboxJobsPG(ctx, conn, workers, index, now)
	}
	var jobs []InboxJob
	err := dueInboxJobs(conn, workers, index, now).Order("id").Limit(inboxClaimBatch).Find(&jobs).Error
	if err != nil {
		log.Warn("Inbox queue: listing jobs: %v", err)
		return 0
	}
	n := 0
	for i := range jobs {
		job := &jobs[i]
		lease := now.Add(inboxLease)
		claim := conn.Model(&InboxJob{}).
			Where("id = ? AND ((state = ? AND next_attempt_at <= ?) OR (state = ? AND lease_until < ?))", job.ID, inboxJobPending, now, inboxJobProcessing, now).
			Updates(map[string]any{"state": inboxJobProcessing, "lease_until": lease})
		if claim.Error != nil || claim.RowsAffected != 1 {
			continue
		}
		n++
		status, perr := applyInboxJob(ctx, job)
		finishInboxJob(conn, job, status, perr, time.Now())
	}
	return n
}

func dueInboxJobs(conn *gorm.DB, workers, index int, now time.Time) *gorm.DB {
	return conn.Model(&InboxJob{}).Where("shard % ? = ?", workers, index).
		Where("(state = ? AND next_attempt_at <= ?) OR (state = ? AND lease_until < ?)", inboxJobPending, now, inboxJobProcessing, now)
}

// processDueInboxJobsPG claims the whole batch in one short transaction with
// FOR UPDATE SKIP LOCKED: one commit (fsync) per batch instead of one per
// job, and concurrent claimers skip rows another worker holds instead of
// queueing on them. The batch shares one lease; a job is only applied while
// that lease is still ours, so an expired lease (another worker may have
// reclaimed the job) stops the batch and the rest are retried later.
func processDueInboxJobsPG(ctx context.Context, conn *gorm.DB, workers, index int, now time.Time) int {
	lease := now.Add(inboxLease)
	var jobs []InboxJob
	err := conn.Transaction(func(tx *gorm.DB) error {
		dialect.AsyncCommit(tx)
		if err := dueInboxJobs(tx, workers, index, now).Order("id").Limit(inboxClaimBatch).
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		ids := make([]uint64, len(jobs))
		for i := range jobs {
			ids[i] = uint64(jobs[i].ID)
		}
		return tx.Model(&InboxJob{}).Where("id IN ?", ids).
			Updates(map[string]any{"state": inboxJobProcessing, "lease_until": lease}).Error
	})
	if err != nil {
		log.Warn("Inbox queue: claiming jobs: %v", err)
		return 0
	}
	n := 0
	for i := range jobs {
		if !time.Now().Before(lease) {
			break
		}
		job := &jobs[i]
		n++
		status, perr := applyInboxJob(ctx, job)
		finishInboxJob(conn, job, status, perr, time.Now())
	}
	return n
}

// applyInboxJob runs the activity through processVerifiedActivity and
// returns the HTTP status the inbox would have answered.
func applyInboxJob(ctx context.Context, job *InboxJob) (status int, err error) {
	defer func() {
		if r := recover(); r != nil {
			status, err = http.StatusInternalServerError, fmt.Errorf("panic: %v", r)
		}
	}()
	var activity map[string]any
	if err := json.Unmarshal([]byte(job.Body), &activity); err != nil {
		return http.StatusBadRequest, err
	}
	conn := db.GetDB().WithContext(ctx)
	var target *models.Account
	if job.TargetAccountID != nil {
		target = &models.Account{}
		if err := conn.First(target, *job.TargetAccountID).Error; err != nil {
			return http.StatusNotFound, fmt.Errorf("target account %d: %w", *job.TargetAccountID, err)
		}
	}
	var relay *models.Relay
	if job.RelayID != nil {
		relay = &models.Relay{}
		if err := conn.First(relay, *job.RelayID).Error; err != nil {
			return http.StatusNotFound, fmt.Errorf("relay %d: %w", *job.RelayID, err)
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/inbox", nil).WithContext(ctx)
	processVerifiedActivity(ctx, c, activity, job.Actor, relay, target)
	status = c.Writer.Status()
	if status >= 400 {
		return status, fmt.Errorf("%s", strings.TrimSpace(w.Body.String()))
	}
	return status, nil
}

func retryableInboxStatus(status int) bool {
	return status >= 500 || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout
}

func finishInboxJob(conn *gorm.DB, job *InboxJob, status int, perr error, now time.Time) {
	write := func(fn func(tx *gorm.DB) error) {
		if err := conn.Transaction(func(tx *gorm.DB) error { dialect.AsyncCommit(tx); return fn(tx) }); err != nil {
			log.Warn("Inbox queue: updating job %d: %v", job.ID, err)
		}
	}
	if perr == nil || !retryableInboxStatus(status) {
		if perr != nil {
			log.Warn("Inbox queue: %s %s by %s refused: HTTP %d: %v", job.ActivityType, job.ActivityID, job.Actor, status, perr)
		}
		write(func(tx *gorm.DB) error { return tx.Delete(&InboxJob{}, job.ID).Error })
		return
	}
	job.Attempts++
	msg := ""
	if perr != nil {
		msg = clip(perr.Error(), 1000)
	}
	if job.Attempts > len(inboxRetryBackoff) {
		log.Warn("Inbox queue: giving up on %s %s by %s after %d attempts (HTTP %d): %s", job.ActivityType, job.ActivityID, job.Actor, job.Attempts, status, msg)
		write(func(tx *gorm.DB) error {
			return tx.Model(&InboxJob{}).Where("id = ?", job.ID).Updates(map[string]any{
				"state": inboxJobDead, "attempts": job.Attempts, "last_status": status, "last_error": msg, "lease_until": nil,
			}).Error
		})
		return
	}
	log.Debug("Inbox queue: %s %s by %s failed (HTTP %d), retry %d: %s", job.ActivityType, job.ActivityID, job.Actor, status, job.Attempts, msg)
	write(func(tx *gorm.DB) error {
		return tx.Model(&InboxJob{}).Where("id = ?", job.ID).Updates(map[string]any{
			"state": inboxJobPending, "attempts": job.Attempts, "last_status": status, "last_error": msg,
			"next_attempt_at": now.Add(inboxRetryBackoff[job.Attempts-1]), "lease_until": nil,
		}).Error
	})
}

func pruneDeadInboxJobs() {
	if err := db.GetDB().Where("state = ? AND created_at < ?", inboxJobDead, time.Now().Add(-inboxDeadRetention)).Delete(&InboxJob{}).Error; err != nil {
		log.Warn("Inbox queue: pruning dead jobs: %v", err)
	}
}
