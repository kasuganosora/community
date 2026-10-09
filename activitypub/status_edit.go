package activitypub

import (
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
)

// recordStatusEdit stores the version being replaced in status_histories.
// A status counts as edited (edited_at) only once such a row exists.
func recordStatusEdit(tx *gorm.DB, status *models.Status) error {
	// Edits are rare; skip history on minimal schemas without the table.
	if !tx.Migrator().HasTable(&models.StatusHistory{}) {
		return nil
	}
	var previous int64
	if err := tx.Model(&models.StatusHistory{}).Where("status_id = ?", status.Id).Count(&previous).Error; err != nil {
		return err
	}
	// The replaced version was published at creation or at the last edit.
	versionAt := status.CreatedAt
	if previous > 0 && status.UpdatedAt != nil {
		versionAt = status.UpdatedAt
	}
	if versionAt == nil {
		now := time.Now()
		versionAt = &now
	}
	return tx.Create(&models.StatusHistory{
		StatusId:            status.Id,
		AccountId:           status.AccountId,
		Content:             status.Content.Content,
		SpoilerText:         status.Content.SpoilerText,
		Sensitive:           status.Sensitive,
		MediaAttachmentsIds: []uint64{},
		EmojisIds:           []uint64{},
		CreatedAt:           versionAt,
	}).Error
}

// editTime is the object's "updated" time when it is plausible, else fallback.
func editTime(object map[string]any, fallback time.Time) time.Time {
	raw, _ := object["updated"].(string)
	updated := utils.ParseRFC3339(raw)
	if updated == nil || updated.After(fallback.Add(5*time.Minute)) {
		return fallback
	}
	if published := utils.ParseRFC3339(stringValue(object["published"])); published != nil && !updated.After(*published) {
		return fallback
	}
	return *updated
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}
