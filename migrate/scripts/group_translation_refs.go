package scripts

import (
	"github.com/kasuganosora/community/llmsvc"
	"gorm.io/gorm"
)

// GroupTranslationRefsScript adds translation_cache_group_refs, linking group
// chat events to cached translations so edits and redactions can purge them.
type GroupTranslationRefsScript struct{}

func (GroupTranslationRefsScript) Up(db *gorm.DB) error {
	return db.AutoMigrate(&llmsvc.GroupTranslationCacheRef{})
}

func (GroupTranslationRefsScript) Down(db *gorm.DB) error {
	return db.Migrator().DropTable(&llmsvc.GroupTranslationCacheRef{})
}
