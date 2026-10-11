package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type StatusContentFormatScript struct{}

func (StatusContentFormatScript) Up(db *gorm.DB) error {
	if !db.Migrator().HasColumn(&models.StatusContent{}, "ContentFormat") {
		return db.Migrator().AddColumn(&models.StatusContent{}, "ContentFormat")
	}
	return nil
}
func (StatusContentFormatScript) Down(*gorm.DB) error { return nil }
