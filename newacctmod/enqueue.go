package newacctmod

import (
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PrepareCreate marks a status local_only when the author is in observation
// and federation hold is on. Call before CreateStatusCommitted.
func PrepareCreate(status *models.Status, account *models.Account) {
	if status == nil || account == nil || !Enabled() || !HoldFederation() {
		return
	}
	if !account.IsLocal() || account.IsBan {
		return
	}
	if !InObservation(db.GetDB(), account.Id) {
		return
	}
	status.LocalOnly = true
}

// Enqueue schedules an async review for a newly created local status that is
// still in the observation window (including when local_only was set).
func Enqueue(conn *gorm.DB, status *models.Status) {
	if !Enabled() || status == nil || status.Id == 0 || status.AccountId == 0 {
		return
	}
	if status.ReblogId != nil && *status.ReblogId != 0 {
		return
	}
	if !conn.Migrator().HasTable(&ReviewJob{}) {
		return
	}
	var account models.Account
	if err := conn.Select("id", "domain", "is_ban").First(&account, status.AccountId).Error; err != nil || !account.IsLocal() || account.IsBan {
		return
	}
	// Count includes this status; still observe while count <= N.
	var n int64
	if err := conn.Model(&models.Status{}).
		Where("account_id = ? AND deleted_at IS NULL AND (reblog_id IS NULL OR reblog_id = 0)", status.AccountId).
		Count(&n).Error; err != nil {
		return
	}
	if int(n) > ObservationN() {
		return
	}
	job := ReviewJob{
		StatusID:      status.Id,
		AccountID:     status.AccountId,
		State:         "pending",
		NextAttemptAt: time.Now(),
	}
	if err := conn.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "status_id"}},
		DoNothing: true,
	}).Create(&job).Error; err != nil {
		log.Warn("newacctmod: enqueue status %d: %v", status.Id, err)
	}
}
