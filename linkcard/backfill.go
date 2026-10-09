package linkcard

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kasuganosora/community/models"
	"golang.org/x/time/rate"
)

// BackfillStats summarizes a Backfill run.
type BackfillStats struct {
	Candidates, Created, Reused, Failed, Skipped, Errors int64
}

// Backfill creates cards for recent statuses that have none yet: at most
// limit statuses created within the last days days (days <= 0: no age
// limit), newest first, using the given concurrency and at most perSecond
// fetches per second (0 = unlimited). progress, when set, is called after
// every status.
func (s *Service) Backfill(ctx context.Context, days, limit, concurrency int, perSecond float64, progress func(id uint64, o Outcome, err error)) (BackfillStats, error) {
	var stats BackfillStats
	if concurrency < 1 {
		concurrency = 1
	}
	q := s.DB().WithContext(ctx).Model(&models.Status{}).
		Joins("JOIN status_contents ON status_contents.status_id = statuses.id").
		Where("statuses.deleted_at IS NULL AND statuses.reblog_id IS NULL AND statuses.visibility <> ?", models.StatusVisibilityDirect).
		Where("status_contents.content LIKE '%http%'").
		Where("NOT EXISTS (SELECT 1 FROM status_cards WHERE status_cards.status_id = statuses.id)").
		Order("statuses.id DESC")
	if days > 0 {
		q = q.Where("statuses.created_at >= ?", time.Now().AddDate(0, 0, -days))
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	var ids []uint64
	if err := q.Select("statuses.id").Scan(&ids).Error; err != nil {
		return stats, err
	}
	stats.Candidates = int64(len(ids))

	var limiter *rate.Limiter
	if perSecond > 0 {
		limiter = rate.NewLimiter(rate.Limit(perSecond), 1)
	}
	jobs := make(chan uint64)
	var wg sync.WaitGroup
	var pmu sync.Mutex
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				out, err := s.Process(ctx, id)
				switch {
				case err != nil:
					atomic.AddInt64(&stats.Errors, 1)
				case out == Created:
					atomic.AddInt64(&stats.Created, 1)
				case out == Reused:
					atomic.AddInt64(&stats.Reused, 1)
				case out == Failed:
					atomic.AddInt64(&stats.Failed, 1)
				default:
					atomic.AddInt64(&stats.Skipped, 1)
				}
				if progress != nil {
					pmu.Lock()
					progress(id, out, err)
					pmu.Unlock()
				}
			}
		}()
	}
feed:
	for _, id := range ids {
		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				break feed
			}
		}
		select {
		case jobs <- id:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	return stats, ctx.Err()
}
