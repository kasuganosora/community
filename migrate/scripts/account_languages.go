package scripts

import (
	"time"

	"gorm.io/gorm"
)

// AccountLanguagesScript creates the account_languages cache. Snapshot of
// models.AccountLanguage; idempotent. Status language backfill runs in the
// discovery background worker instead, in small batches outside migrations.
type AccountLanguagesScript struct{}

type accountLanguageRow struct {
	AccountId uint64    `gorm:"primaryKey;autoIncrement:false"`
	Language  string    `gorm:"type:varchar(16);not null;default:''"`
	Samples   int       `gorm:"not null;default:0"`
	UpdatedAt time.Time `gorm:"not null;index"`
}

func (accountLanguageRow) TableName() string { return "account_languages" }

func (AccountLanguagesScript) Up(db *gorm.DB) error { return db.AutoMigrate(&accountLanguageRow{}) }

func (AccountLanguagesScript) Down(*gorm.DB) error { return nil }
