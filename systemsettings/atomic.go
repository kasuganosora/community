package systemsettings

import (
	"errors"
	"github.com/google/uuid"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SaveChanges publishes new runtime settings only after every database write
// succeeds. Readers never observe a partially saved settings form.
func SaveChanges(values map[string]any) error {
	// Store a new epoch in the same transaction as policy changes. Do not
	// extend caller-owned maps or publish an uncommitted generation.
	changes := make(map[string]any, len(values)+1)
	for key, value := range values {
		changes[key] = value
	}
	for _, key := range []string{KeyRecommendationEnabled, KeyRecommendationMode, KeyRecommendationApprovedVersion} {
		if _, changed := values[key]; changed {
			changes[KeyRecommendationPolicyRevision] = uuid.NewString()
			break
		}
	}
	values = changes
	Load()
	database := db.GetDB()
	if database == nil {
		return errors.New("settings database unavailable")
	}
	mu.Lock()
	defer mu.Unlock()
	if err := database.Transaction(func(tx *gorm.DB) error {
		for key, value := range values {
			row := models.SystemSetting{Key: key, Val: value}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"val"})}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for key, value := range values {
		settings[key] = value
	}
	return nil
}
