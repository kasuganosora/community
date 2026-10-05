package scripts

import "gorm.io/gorm"

// FilterExpiresNullableScript lets a filter omit an end time. The column was
// created NOT NULL, so a permanent filter could not be saved.
type FilterExpiresNullableScript struct{}

func (FilterExpiresNullableScript) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("filters") {
		return nil
	}
	if db.Dialector.Name() == "sqlite" {
		return nil
	}
	return db.Exec("ALTER TABLE filters MODIFY expires_at DATETIME(3) NULL").Error
}

func (FilterExpiresNullableScript) Down(*gorm.DB) error { return nil }
