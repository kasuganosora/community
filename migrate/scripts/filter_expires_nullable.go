package scripts

import (
	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

// FilterExpiresNullableScript lets a filter omit an end time. The column was
// created NOT NULL, so a permanent filter could not be saved.
type FilterExpiresNullableScript struct{}

func (FilterExpiresNullableScript) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("filters") {
		return nil
	}
	switch dialect.Name(db) {
	case dialect.SQLite:
		return nil
	case dialect.Postgres:
		return db.Exec("ALTER TABLE filters ALTER COLUMN expires_at DROP NOT NULL").Error
	}
	return db.Exec("ALTER TABLE filters MODIFY expires_at DATETIME(3) NULL").Error
}

func (FilterExpiresNullableScript) Down(*gorm.DB) error { return nil }
