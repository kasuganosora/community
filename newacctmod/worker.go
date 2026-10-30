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

const maxAttempts = 5

func Process(ctx context.Context, conn *gorm.DB, statusID uint64) error {
	if !Enabled() || !conn.Migrator().HasTable(&ReviewJob{}) {
		return nil
	}
	token := uuid.NewString()
	claimed := conn.WithContext(ctx).Model(&ReviewJob{}).
		Where("status_id = ? AND state IN ? AND next_attempt_at <= ?", statusID, []string{"pending", "failed"}, time.Now()).
		Updates(map[string]any{
			"state": "processing", "lease_token": token,
			"next_attempt_at": time.Now().Add(2 * time.Minute),
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
		return ApplySuspend(ctx, conn, job.StatusID, job.AccountID, "heuristic: "+h.Reason)
	}
	v, err := judgeLLM(ctx, current, prior)
	if err != nil {
		if errors.Is(err, llmsvc.ErrNotConfigured) {
			return ApplyFlag(ctx, conn, job.StatusID, job.AccountID, "llm not configured")
		}
		return requeue(conn, &job, token, err)
	}
	switch v.Verdict {
	case "pass":
		return ApplyPass(ctx, conn, job.StatusID, v.Reason)
	case "suspend":
		if v.Confidence >= 0.75 {
			return ApplySuspend(ctx, conn, job.StatusID, job.AccountID, v.Reason)
		}
		return ApplyFlag(ctx, conn, job.StatusID, job.AccountID, "low-confidence: "+v.Reason)
	default:
		return ApplyFlag(ctx, conn, job.StatusID, job.AccountID, v.Reason)
	}
}

func requeue(conn *gorm.DB, job *ReviewJob, token string, cause error) error {
	attempts := job.Attempts
	if attempts >= maxAttempts {
		_ = ApplyFlag(context.Background(), conn, job.StatusID, job.AccountID, "llm retries exhausted: "+cause.Error())
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
				Where("state IN ? AND next_attempt_at <= ?", []string{"pending", "failed"}, time.Now()).
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
