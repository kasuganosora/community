package newacctmod

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrReviewClosed = errors.New("review is no longer actionable")

// ReviewManually lets moderators release or retry a held post. Changing the
// lease under the job lock fences an in-flight automatic verdict out.
func ReviewManually(ctx context.Context, conn *gorm.DB, id, actorID uint64, action string) error {
	return conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job ReviewJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, id).Error; err != nil {
			return err
		}
		if job.State == "done" && job.Verdict != "flag" {
			return ErrReviewClosed
		}
		var account models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, job.AccountID).Error; err != nil {
			return err
		}
		if !account.IsLocal() || account.IsBan || account.DeletionRequestedAt != nil {
			return ErrReviewClosed
		}
		var status models.Status
		if err := tx.First(&status, job.StatusID).Error; err != nil {
			return err
		}
		if action == "retry" {
			return tx.Model(&job).Updates(map[string]any{
				"state": "pending", "lease_token": "", "attempts": 0, "verdict": "", "last_error": "",
				"reason": fmt.Sprintf("retry requested by account %d", actorID), "next_attempt_at": time.Now(),
			}).Error
		}
		if action != "approve" {
			return errors.New("invalid review action")
		}
		token := uuid.NewString()
		if err := tx.Model(&job).Updates(map[string]any{"state": "processing", "lease_token": token}).Error; err != nil {
			return err
		}
		return ApplyPass(ctx, tx, job.StatusID, token, fmt.Sprintf("approved by account %d", actorID))
	})
}
