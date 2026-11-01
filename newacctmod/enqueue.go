package newacctmod

import (
	"time"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PrepareCreate decides once whether this post needs review and returns the
// transaction callback that persists its job. The status and job must commit
// together: recounting after commit can miss held posts at the observation
// boundary, and a failed queue write must not leave an unreviewable local post.
func PrepareCreate(conn *gorm.DB, status *models.Status, account *models.Account) func(*gorm.DB) error {
	if status == nil || account == nil || !Enabled() || !account.IsLocal() || account.IsBan ||
		!conn.Migrator().HasTable(&ReviewJob{}) || !InObservation(conn, account.Id) {
		return nil
	}
	if status.ReblogId != nil && *status.ReblogId != 0 {
		return nil
	}
	if HoldFederation() {
		status.LocalOnly = true
	}
	return func(tx *gorm.DB) error {
		job := ReviewJob{
			StatusID: status.Id, AccountID: status.AccountId,
			State: "pending", NextAttemptAt: time.Now(),
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "status_id"}}, DoNothing: true,
		}).Create(&job).Error
	}
}
