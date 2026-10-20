package scripts

import (
	"github.com/kasuganosora/community/llmsvc"
	"gorm.io/gorm"
)

// TranslationCacheRefsScript adds translation_caches.content_hash and the
// translation_cache_refs table used to purge cached translations when a
// status is edited or deleted. AutoMigrate is idempotent on all databases.
type TranslationCacheRefsScript struct{}

func (TranslationCacheRefsScript) Up(db *gorm.DB) error {
	return db.AutoMigrate(&llmsvc.TranslationCache{}, &llmsvc.TranslationCacheRef{})
}

func (TranslationCacheRefsScript) Down(*gorm.DB) error { return nil }
