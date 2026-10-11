// Package worker projects committed Feed events with bounded, independently
// leased scans and deliveries. The same worker runs in API or worker processes.
package worker

import (
	"context"
	"errors"
	"github.com/kasuganosora/community/feed"
	"gorm.io/gorm/clause"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"gorm.io/gorm"
)

const leaseDuration = 30 * time.Second

var (
	taskCount       = promauto.NewCounterVec(prometheus.CounterOpts{Name: "community_feed_tasks_total", Help: "Feed task outcomes without recipient labels."}, []string{"stage", "outcome"})
	deliveryLatency = promauto.NewHistogram(prometheus.HistogramOpts{Name: "community_feed_delivery_seconds", Help: "Commit to durable batch completion, including queue time.", Buckets: []float64{.05, .1, .25, .5, 1, 3, 5, 10, 30, 60, 300}})
	queueDepth      = promauto.NewGaugeVec(prometheus.GaugeOpts{Name: "community_feed_queue_depth", Help: "Durable queue rows by bounded stage and state."}, []string{"stage", "state"})
	pendingAge      = promauto.NewGauge(prometheus.GaugeOpts{Name: "community_feed_oldest_pending_seconds", Help: "Age of the oldest unfinished Feed event."})
)

type Options struct {
	SQLReserve          int
	ScanConcurrency     int
	DeliveryConcurrency int
	BatchSize           int
	// PollInterval is the first sleep after an empty poll; consecutive empty
	// polls double it up to IdlePollMax, and finding work resets it.
	PollInterval time.Duration
	IdlePollMax  time.Duration
	ActiveWindow time.Duration
	// Shadow builds the candidate namespace without client-visible hints.
	Shadow bool
}

func DefaultOptions() Options {
	return Options{SQLReserve: 2, ScanConcurrency: 2, DeliveryConcurrency: 4, BatchSize: 256, PollInterval: 100 * time.Millisecond, IdlePollMax: 2 * time.Second, ActiveWindow: 10 * 24 * time.Hour}
}

type Worker struct {
	DB                 *gorm.DB
	Options            Options
	group              sync.WaitGroup
	sqlPaused          atomic.Bool
	dependencyFailures atomic.Uint32
	dependencyUntil    atomic.Int64
}

func New(conn *gorm.DB, options Options) *Worker {
	defaults := DefaultOptions()
	if options.SQLReserve <= 0 {
		options.SQLReserve = defaults.SQLReserve
	}
	if options.ScanConcurrency <= 0 {
		options.ScanConcurrency = defaults.ScanConcurrency
	}
	if options.DeliveryConcurrency <= 0 {
		options.DeliveryConcurrency = defaults.DeliveryConcurrency
	}
	options.ScanConcurrency = min(options.ScanConcurrency, 16)
	options.DeliveryConcurrency = min(options.DeliveryConcurrency, 32)
	if options.BatchSize <= 0 {
		options.BatchSize = defaults.BatchSize
	}
	options.BatchSize = max(128, min(options.BatchSize, 1024))
	if options.PollInterval <= 0 {
		options.PollInterval = defaults.PollInterval
	}
	if options.IdlePollMax <= 0 {
		options.IdlePollMax = defaults.IdlePollMax
	}
	options.IdlePollMax = max(options.IdlePollMax, options.PollInterval)
	if options.ActiveWindow <= 0 {
		options.ActiveWindow = defaults.ActiveWindow
	}
	return &Worker{DB: conn, Options: options}
}

// Start owns a fixed set of goroutines. Each slot claims at most one row; no
// unbounded memory queue or goroutine per follower is created.
func (w *Worker) Start(ctx context.Context) {
	for i := 0; i < w.Options.ScanConcurrency; i++ {
		w.startLoop(ctx, "scan", w.ScanOnce)
	}
	for i := 0; i < w.Options.DeliveryConcurrency; i++ {
		w.startLoop(ctx, "deliver", w.DeliverOnce)
	}
	w.group.Add(1)
	go func() {
		defer w.group.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.maintain(ctx)
			}
		}
	}()
}

func (w *Worker) Wait() { w.group.Wait() }

func (w *Worker) startLoop(ctx context.Context, stage string, operation func(context.Context) (bool, error)) {
	w.group.Add(1)
	go func() {
		defer w.group.Done()
		idle := time.Duration(0)
		for ctx.Err() == nil {
			worked := false
			var err error
			if !w.pauseClaims() {
				worked, err = operation(ctx)
			}
			if err != nil {
				code, _ := classifyFailure(err)
				if code == "dependency_unavailable" || code == "dependency_timeout" {
					n := w.dependencyFailures.Add(1)
					if n >= 3 {
						w.dependencyUntil.Store(time.Now().Add(retryDelay(min(n, 8))).UnixNano())
					}
				}
			} else if worked && stage == "deliver" {
				w.dependencyFailures.Store(0)
			}
			if err != nil && ctx.Err() == nil {
				log.Warn("Feed worker operation failed (%T)", err)
			}
			if worked && err == nil {
				idle = 0
			} else {
				idle = nextIdleDelay(idle, w.Options.PollInterval, w.Options.IdlePollMax)
				timer := time.NewTimer(jitterDelay(idle))
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}
	}()
}

// nextIdleDelay grows the sleep after consecutive empty (or failed) polls:
// base, 2*base, 4*base ... capped at max.
func nextIdleDelay(current, base, max time.Duration) time.Duration {
	if current < base {
		return min(base, max)
	}
	return min(current*2, max)
}

// jitterDelay spreads concurrent loops by +-20% so they do not poll in lockstep.
func jitterDelay(d time.Duration) time.Duration {
	return d - d/5 + time.Duration(rand.Int64N(int64(d/5*2)+1))
}

func retryDelay(attempt uint32) time.Duration {
	base := min(30*time.Second, 100*time.Millisecond*time.Duration(1<<min(attempt, 9)))
	return base + time.Duration(rand.Int64N(int64(base/4)+1))
}

func expired(created time.Time, attempts uint32) bool {
	return attempts >= 11 || time.Since(created) >= 7*24*time.Hour
}

func (w *Worker) ScanOnce(ctx context.Context) (bool, error) {
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := models.ClaimFeedEvents(w.DB.WithContext(ctx), 1, leaseDuration)
	if err != nil || len(rows) == 0 {
		return false, err
	}
	event := rows[0]
	if time.Since(event.CreatedAt) >= 7*24*time.Hour {
		return true, models.RetryFeedEvent(w.DB.WithContext(ctx), event.LeaseRef(), "replay_window_expired", 0, true)
	}
	if event.ScanComplete {
		err = models.CompleteFeedEvent(w.DB.WithContext(ctx), event.LeaseRef())
		if errors.Is(err, models.ErrFeedDependencyFailed) {
			return true, models.RetryFeedEvent(w.DB.WithContext(ctx), event.LeaseRef(), "dependent_failed", 0, true)
		}
		if errors.Is(err, models.ErrFeedNotComplete) {
			err = models.YieldFeedEvent(w.DB.WithContext(ctx), event.LeaseRef(), 500*time.Millisecond)
		}
	} else {
		err = w.scan(ctx, &event)
		if err == nil {
			taskCount.WithLabelValues("scan", "success").Inc()
			err = models.YieldFeedEvent(w.DB.WithContext(ctx), event.LeaseRef(), 50*time.Millisecond)
		}
	}
	if err == nil {
		return true, nil
	}
	taskCount.WithLabelValues("scan", "retry").Inc()
	if !errors.Is(err, models.ErrFeedLeaseLost) && caller.Err() == nil {
		retryCtx, retryCancel := context.WithTimeout(context.WithoutCancel(caller), 2*time.Second)
		defer retryCancel()
		code, permanent := classifyFailure(err)
		if retryErr := models.RetryFeedEvent(w.DB.WithContext(retryCtx), event.LeaseRef(), code, retryDelay(event.Attempts), permanent || expired(event.CreatedAt, event.Attempts)); retryErr != nil {
			return true, retryErr
		}
	}
	return true, err
}

func (w *Worker) DeliverOnce(ctx context.Context) (bool, error) {
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := models.ClaimFeedBatches(w.DB.WithContext(ctx), 1, leaseDuration)
	if err != nil || len(rows) == 0 {
		return false, err
	}
	batch := rows[0]
	var event models.FeedEvent
	err = w.DB.WithContext(ctx).First(&event, batch.EventID).Error
	if err == nil && time.Since(event.CreatedAt) >= 7*24*time.Hour {
		return true, models.RetryFeedBatch(w.DB.WithContext(ctx), batch.LeaseRef(), "replay_window_expired", 0, true)
	}
	if err == nil {
		err = w.deliver(ctx, &event, &batch)
	}
	if err == nil {
		err = models.CompleteFeedBatch(w.DB.WithContext(ctx), batch.LeaseRef())
		if err == nil {
			taskCount.WithLabelValues("deliver", "success").Inc()
			deliveryLatency.Observe(time.Since(event.CreatedAt).Seconds())
			return true, nil
		}
	}
	taskCount.WithLabelValues("deliver", "retry").Inc()
	if !errors.Is(err, models.ErrFeedLeaseLost) && caller.Err() == nil {
		retryCtx, retryCancel := context.WithTimeout(context.WithoutCancel(caller), 2*time.Second)
		defer retryCancel()
		code, permanent := classifyFailure(err)
		if retryErr := models.RetryFeedBatch(w.DB.WithContext(retryCtx), batch.LeaseRef(), code, retryDelay(batch.Attempts), permanent || expired(event.CreatedAt, batch.Attempts)); retryErr != nil {
			return true, retryErr
		}
	}
	return true, err
}

func (w *Worker) maintain(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn := w.DB.WithContext(ctx)
	for _, item := range []struct {
		stage string
		model any
	}{{"scan", &models.FeedEvent{}}, {"deliver", &models.FeedBatch{}}} {
		var counts []struct {
			State string
			Count int64
		}
		if err := conn.Model(item.model).Select("state, COUNT(*) AS count").Group("state").Find(&counts).Error; err == nil {
			values := map[string]int64{}
			for _, row := range counts {
				values[row.State] = row.Count
			}
			for _, state := range []string{"pending", "processing", "complete", "failed", "cancelled"} {
				queueDepth.WithLabelValues(item.stage, state).Set(float64(values[state]))
			}
		}
	}
	// An empty queue is normal: Find, not First, so GORM logs no "record not found".
	var oldest []models.FeedEvent
	if err := conn.Select("id, created_at").Where("state IN ?", []string{"pending", "processing"}).Order("created_at").Limit(1).Find(&oldest).Error; err == nil {
		if len(oldest) == 0 {
			pendingAge.Set(0)
		} else {
			pendingAge.Set(max(0, time.Since(oldest[0].CreatedAt).Seconds()))
		}
	}

	now, err := models.FeedDatabaseTime(conn)
	if err != nil {
		return
	}
	// Terminal diagnostics are retained, but not forever. Lock the parent again
	// before pruning so an administrator retry cannot lose its dependent rows.
	_ = conn.Transaction(func(tx *gorm.DB) error {
		cutoff := now.Add(-8 * 24 * time.Hour)
		failedCutoff := now.Add(-30 * 24 * time.Hour)
		q := tx.Model(&models.FeedEvent{}).Where("((state IN ? AND updated_at < ?) OR (state = ? AND updated_at < ?)) AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", []string{"complete", "cancelled"}, cutoff, "failed", failedCutoff, now).Order("id").Limit(128)
		if tx.Dialector.Name() != "sqlite" {
			q = q.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}
		var rows []models.FeedEvent
		if err := q.Find(&rows).Error; err != nil {
			return err
		}
		ids := []uint64{}
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		if len(ids) == 0 {
			return nil
		}
		var children []models.FeedBatch
		cq := tx.Select("id").Where("event_id IN ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)", ids, now).Order("id").Limit(512)
		if tx.Dialector.Name() != "sqlite" {
			cq = cq.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}
		if err := cq.Find(&children).Error; err != nil {
			return err
		}
		childIDs := []uint64{}
		for _, child := range children {
			childIDs = append(childIDs, child.ID)
		}
		if len(childIDs) > 0 {
			if err := tx.Where("id IN ?", childIDs).Delete(&models.FeedBatch{}).Error; err != nil {
				return err
			}
		}
		var remaining []uint64
		if err := tx.Model(&models.FeedBatch{}).Where("event_id IN ?", ids).Distinct("event_id").Pluck("event_id", &remaining).Error; err != nil {
			return err
		}
		keep := map[uint64]bool{}
		for _, id := range remaining {
			keep[id] = true
		}
		remove := []uint64{}
		for _, id := range ids {
			if !keep[id] {
				remove = append(remove, id)
			}
		}
		if len(remove) == 0 {
			return nil
		}
		return tx.Where("id IN ?", remove).Delete(&models.FeedEvent{}).Error
	})
}

var ErrInvalidTask = errors.New("invalid feed task")

func classifyFailure(err error) (string, bool) {
	if errors.Is(err, ErrInvalidTask) || errors.Is(err, gorm.ErrRecordNotFound) {
		return "invalid_task", true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "dependency_timeout", false
	}
	var network net.Error
	if errors.As(err, &network) {
		return "dependency_unavailable", false
	}
	if errors.Is(err, feed.ErrNeedsRebuild) || errors.Is(err, feed.ErrFeedRegenerating) {
		return "epoch_changed", false
	}
	if errors.Is(err, feed.ErrStaleProjection) {
		return "projection_fenced", false
	}
	return "operation_failed", false
}

// Reserve SQL capacity for HTTP. Resume below a lower watermark to avoid
// rapidly toggling claims when API demand hovers near the pool limit.
func (w *Worker) pauseClaims() bool {
	if time.Now().UnixNano() < w.dependencyUntil.Load() {
		return true
	}
	pool, err := w.DB.DB()
	if err != nil {
		return true
	}
	stats := pool.Stats()
	if stats.MaxOpenConnections <= 0 {
		return false
	}
	high := max(1, stats.MaxOpenConnections-w.Options.SQLReserve)
	if w.sqlPaused.Load() {
		if stats.InUse > high/2 {
			return true
		}
		w.sqlPaused.Store(false)
	}
	if stats.InUse >= high {
		w.sqlPaused.Store(true)
		return true
	}
	return false
}
