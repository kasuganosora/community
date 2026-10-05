package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// FilterActionDefaultScript turns unset filter actions into warn. Older
// creates left the column at 0, and 0 never matches.
type FilterActionDefaultScript struct{}

func (FilterActionDefaultScript) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("filters") {
		return nil
	}
	return db.Exec("UPDATE filters SET filter_action = ? WHERE filter_action = 0", models.FilterActionWarn).Error
}

func (FilterActionDefaultScript) Down(*gorm.DB) error { return nil }
