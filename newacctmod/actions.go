package newacctmod

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// errLostLease means another worker took the review job before this one
// applied a verdict. Callers must not retry with the old token.
var errLostLease = errors.New("review lease lost")

// SuspensionKeyFor builds the stable key for one auto-suspension (one appeal).
func SuspensionKeyFor(accountID, statusID uint64) string {
	return fmt.Sprintf("auto-%d-%d", accountID, statusID)
}

// ApplyPass clears local_only and enqueues the outbound Create.
func ApplyPass(ctx context.Context, conn *gorm.DB, statusID uint64, token, reason string) error {
	return conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertLease(tx, statusID, token); err != nil {
			return err
		}
		var s models.Status
		if err := models.StatusLoadAllQuery(tx.Clauses(clause.Locking{Strength: "UPDATE"})).First(&s, statusID).Error; err != nil {
			return err
		}
		var author models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&author, s.AccountId).Error; err != nil {
			return err
		}
		if author.IsBan || author.DeletionRequestedAt != nil {
			return finishJob(tx, statusID, token, "skipped", "author is unavailable")
		}
		if !s.LocalOnly {
			return finishJob(tx, statusID, token, "pass", reason)
		}
		if err := tx.Model(&s).Update("local_only", false).Error; err != nil {
			return err
		}
		s.LocalOnly = false
		if err := models.QueueFederationReplacement(tx, &s); err != nil {
			return err
		}
		return finishJob(tx, statusID, token, "pass", reason)
	})
}

// ApplyFlag files a spam Report and leaves the status local_only.
func ApplyFlag(ctx context.Context, conn *gorm.DB, statusID, accountID uint64, token, reason string) error {
	reporterID := uint64(0)
	if ia, _, err := activitypub.EnsureInstanceActor(ctx); err == nil && ia != nil {
		reporterID = ia.Id
	}
	if reporterID == 0 {
		reporterID = accountID
	}

	return conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertLease(tx, statusID, token); err != nil {
			return err
		}
		report := &models.Report{
			AccountId:       reporterID,
			TargetAccountId: accountID,
			Comment:         "new-account LLM moderation: " + reason,
			StatusIds:       []uint64{statusID},
		}
		report.SetCategory("Spam")
		if err := tx.Omit("ActionTakenByAccountId", "AssignedAccountId").Create(report).Error; err != nil {
			return fmt.Errorf("create review report for status %d: %w", statusID, err)
		}
		return finishJob(tx, statusID, token, "flag", reason)
	})
}

// ApplySuspend removes the status and marks the local account unavailable.
func ApplySuspend(ctx context.Context, conn *gorm.DB, statusID, accountID uint64, token, reason string) error {
	return conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertLease(tx, statusID, token); err != nil {
			return err
		}
		var s models.Status
		if err := models.StatusLoadAllQuery(tx.Clauses(clause.Locking{Strength: "UPDATE"})).First(&s, statusID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return finishJob(tx, statusID, token, "suspend", reason)
			}
			return err
		}
		if err := tx.Delete(&s).Error; err != nil {
			return err
		}
		var account models.Account
		if err := tx.Preload("User").Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, accountID).Error; err != nil {
			return err
		}
		if account.IsLocal() && !account.IsBan {
			if err := tx.Model(&account).UpdateColumn("is_ban", true).Error; err != nil {
				return err
			}
			if account.User != nil {
				if err := tx.Model(account.User).UpdateColumn("is_ban", true).Error; err != nil {
					return err
				}
			}
			_ = models.QueueFeedInvalidation(tx, models.FeedEventMetadata{
				ActorID: account.Id, TargetAccountID: account.Id, Reason: "account_policy_changed",
			})
		}
		return finishJob(tx, statusID, token, "suspend", reason)
	})
}

func assertLease(tx *gorm.DB, statusID uint64, token string) error {
	if token == "" || !tx.Migrator().HasTable(&ReviewJob{}) {
		return nil
	}
	var job ReviewJob
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("status_id = ? AND lease_token = ?", statusID, token).
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errLostLease
	}
	return err
}

func finishJob(tx *gorm.DB, statusID uint64, token, verdict, reason string) error {
	if !tx.Migrator().HasTable(&ReviewJob{}) {
		return nil
	}
	q := tx.Model(&ReviewJob{}).Where("status_id = ?", statusID)
	if token != "" {
		q = q.Where("lease_token = ?", token)
	}
	return q.Updates(map[string]any{
		"state": "done", "verdict": verdict, "reason": reason, "lease_token": "", "updated_at": time.Now(),
	}).Error
}
