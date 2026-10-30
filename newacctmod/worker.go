package newacctmod

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
)

const (
	maxAttempts       = 5
	claimLease        = 5 * time.Minute
	unconfiguredDelay = 5 * time.Minute
)

// claimableStates includes expired processing leases so a crashed worker
// does not leave the post local_only forever.
var claimableStates = []string{"pending", "failed", "processing"}

func Process(ctx context.Context, conn *gorm.DB, statusID uint64) error {
	if !Enabled() || !conn.Migrator().HasTable(&ReviewJob{}) {
		return nil
	}
	token := uuid.NewString()
	now := time.Now()
	claimed := conn.WithContext(ctx).Model(&ReviewJob{}).
		Where("status_id = ? AND state IN ? AND next_attempt_at <= ?", statusID, claimableStates, now).
		Updates(map[string]any{
			"state": "processing", "lease_token": token,
			"next_attempt_at": now.Add(claimLease),
			"attempts":        gorm.Expr("attempts + 1"),
		})
	if claimed.Error != nil || claimed.RowsAffected == 0 {
		return claimed.Error
	}
	var job ReviewJob
	if err := conn.WithContext(ctx).First(&job, "status_id = ? AND lease_token = ?", statusID, token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	current, prior, err := gatherContext(conn, job.AccountID, job.StatusID, ContextX())
	if err != nil {
		return requeue(conn, &job, token, err)
	}
	if h := RunHeuristic(conn, job.AccountID, job.StatusID, current); h.Reject {
		return finishOrRequeue(ctx, conn, &job, token, ApplySuspend(ctx, conn, job.StatusID, job.AccountID, token, "heuristic: "+h.Reason))
	}
	v, err := judgeLLM(ctx, current, prior)
	if err != nil {
		if errors.Is(err, llmsvc.ErrNotConfigured) {
			// Keep the post held and try again after a provider is configured.
			// Do not file a spam report or burn the attempt budget.
			return releasePending(conn, &job, token, "llm not configured", unconfiguredDelay)
		}
		return requeue(conn, &job, token, err)
	}
	switch v.Verdict {
	case "pass":
		return finishOrRequeue(ctx, conn, &job, token, ApplyPass(ctx, conn, job.StatusID, token, v.Reason))
	case "suspend":
		if v.Confidence >= 0.75 {
			return finishOrRequeue(ctx, conn, &job, token, ApplySuspend(ctx, conn, job.StatusID, job.AccountID, token, v.Reason))
		}
		return finishOrRequeue(ctx, conn, &job, token, ApplyFlag(ctx, conn, job.StatusID, job.AccountID, token, "low-confidence: "+v.Reason))
	default:
		return finishOrRequeue(ctx, conn, &job, token, ApplyFlag(ctx, conn, job.StatusID, job.AccountID, token, v.Reason))
	}
}

func finishOrRequeue(ctx context.Context, conn *gorm.DB, job *ReviewJob, token string, err error) error {
	if err == nil || errors.Is(err, errLostLease) {
		return nil
	}
	if ctx.Err() != nil {
		return err
	}
	return requeue(conn, job, token, err)
}

func requeue(conn *gorm.DB, job *ReviewJob, token string, cause error) error {
	attempts := job.Attempts
	if attempts >= maxAttempts {
		if err := ApplyFlag(context.Background(), conn, job.StatusID, job.AccountID, token, "llm retries exhausted: "+cause.Error()); err != nil && !errors.Is(err, errLostLease) {
			log.Warn("newacctmod: flag status %d after retries: %v", job.StatusID, err)
		}
		return cause
	}
	backoff := time.Duration(attempts) * 30 * time.Second
	if backoff < 30*time.Second {
		backoff = 30 * time.Second
	}
	return conn.Model(&ReviewJob{}).Where("status_id = ? AND lease_token = ?", job.StatusID, token).Updates(map[string]any{
		"state": "failed", "lease_token": "", "last_error": cause.Error(),
		"next_attempt_at": time.Now().Add(backoff),
	}).Error
}

// releasePending returns a claimed job to the queue without counting the
// attempt. Used when the LLM provider is not configured yet.
func releasePending(conn *gorm.DB, job *ReviewJob, token, reason string, delay time.Duration) error {
	attempts := job.Attempts - 1
	if attempts < 0 {
		attempts = 0
	}
	return conn.Model(&ReviewJob{}).Where("status_id = ? AND lease_token = ?", job.StatusID, token).Updates(map[string]any{
		"state": "pending", "lease_token": "", "attempts": attempts, "last_error": reason,
		"next_attempt_at": time.Now().Add(delay),
	}).Error
}

func Start(ctx context.Context, conn *gorm.DB) {
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if !Enabled() || !conn.Migrator().HasTable(&ReviewJob{}) {
				continue
			}
			var rows []ReviewJob
			if conn.WithContext(ctx).Select("status_id").
				Where("state IN ? AND next_attempt_at <= ?", claimableStates, time.Now()).
				Order("next_attempt_at").Limit(2).Find(&rows).Error != nil {
				continue
			}
			for _, row := range rows {
				if ctx.Err() != nil {
					return
				}
				if err := Process(ctx, conn, row.StatusID); err != nil && ctx.Err() == nil {
					log.Warn("newacctmod: review status %d: %v", row.StatusID, err)
				}
			}
		}
	}()
}
