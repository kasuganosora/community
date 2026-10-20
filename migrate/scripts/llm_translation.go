package scripts

import (
	"github.com/kasuganosora/community/llmsvc"
	"gorm.io/gorm"
)

// LLMTranslationScript creates the LLM usage tables (daily totals per
// feature/provider/model, per-user daily call counters) and the shared
// translation cache. AutoMigrate is idempotent on PostgreSQL, MySQL and SQLite.
type LLMTranslationScript struct{}

func (LLMTranslationScript) Up(db *gorm.DB) error {
	return db.AutoMigrate(llmsvc.Models()...)
}

func (LLMTranslationScript) Down(*gorm.DB) error { return nil }
