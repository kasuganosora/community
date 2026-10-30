package newacctmod

import (
	"context"
	"fmt"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SuspensionKeyFor builds the stable key for one auto-suspension (one appeal).
func SuspensionKeyFor(accountID, statusID uint64) string {
	return fmt.Sprintf("auto-%d-%d", accountID, statusID)
}

// ApplyPass clears local_only and enqueues the outbound Create.
func ApplyPass(ctx context.Context, conn *gorm.DB, statusID uint64, reason string) error {
	return conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var s models.Status
		if err := models.StatusLoadAllQuery(tx.Clauses(clause.Locking{Strength: "UPDATE"})).First(&s, statusID).Error; err != nil {
			return err
		}
		if !s.LocalOnly {
			return finishJob(tx, statusID, "pass", reason)
		}
		if err := tx.Model(&s).Update("local_only", false).Error; err != nil {
			return err
		}
		s.LocalOnly = false
		if err := models.QueueFederationReplacement(tx, &s); err != nil {
			return err
		}
		return finishJob(tx, statusID, "pass", reason)
	})
}

// ApplyFlag files a spam Report and leaves the status local_only.
func ApplyFlag(ctx context.Context, conn *gorm.DB, statusID, accountID uint64, reason string) error {
	return conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		reporterID := uint64(0)
		if ia, _, err := activitypub.EnsureInstanceActor(ctx); err == nil && ia != nil {
			reporterID = ia.Id
		}
		if reporterID == 0 {
			reporterID = accountID
		}
		report := &models.Report{
			AccountId:       reporterID,
			TargetAccountId: accountID,
			Comment:         "new-account LLM moderation: " + reason,
			StatusIds:       []uint64{statusID},
		}
		report.SetCategory("Spam")
		if err := tx.Omit("ActionTakenByAccountId", "AssignedAccountId").Create(report).Error; err != nil {
			log.Warn("newacctmod: report status %d: %v", statusID, err)
		}
		return finishJob(tx, statusID, "flag", reason)
	})
}

// ApplySuspend removes the status and marks the local account unavailable.
func ApplySuspend(ctx context.Context, conn *gorm.DB, statusID, accountID uint64, reason string) error {
	return conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var s models.Status
		if err := models.StatusLoadAllQuery(tx.Clauses(clause.Locking{Strength: "UPDATE"})).First(&s, statusID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return finishJob(tx, statusID, "suspend", reason)
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
		return finishJob(tx, statusID, "suspend", reason)
	})
}

func finishJob(tx *gorm.DB, statusID uint64, verdict, reason string) error {
	if !tx.Migrator().HasTable(&ReviewJob{}) {
		return nil
	}
	return tx.Model(&ReviewJob{}).Where("status_id = ?", statusID).Updates(map[string]any{
		"state": "done", "verdict": verdict, "reason": reason, "lease_token": "", "updated_at": time.Now(),
	}).Error
}
