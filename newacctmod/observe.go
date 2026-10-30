package newacctmod

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// InObservation reports whether a local account still has fewer than N
// original (non-reblog) statuses and should stay under LLM watch.
func InObservation(conn *gorm.DB, accountID uint64) bool {
	if !Enabled() || accountID == 0 {
		return false
	}
	var account models.Account
	if err := conn.Select("id", "domain").First(&account, accountID).Error; err != nil || !account.IsLocal() {
		return false
	}
	var n int64
	err := conn.Model(&models.Status{}).
		Where("account_id = ? AND deleted_at IS NULL AND (reblog_id IS NULL OR reblog_id = 0)", accountID).
		Count(&n).Error
	if err != nil {
		return false
	}
	return int(n) < ObservationN()
}

// ShouldHoldFederation is true when the next create for this account must
// stay local_only until a review passes.
func ShouldHoldFederation(accountID uint64) bool {
	if !Enabled() || !HoldFederation() {
		return false
	}
	return InObservation(db.GetDB(), accountID)
}
