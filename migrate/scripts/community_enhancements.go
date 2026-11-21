package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// CommunityEnhancementsScript adds bookmark_folders, bookmark_folder_items, and
// statuses.pinned_reply_id across SQLite, MySQL, and PostgreSQL.
type CommunityEnhancementsScript struct{}

func (CommunityEnhancementsScript) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&models.BookmarkFolder{}) {
		if err := db.Migrator().CreateTable(&models.BookmarkFolder{}); err != nil {
			return err
		}
	}
	if !db.Migrator().HasTable(&models.BookmarkFolderItem{}) {
		if err := db.Migrator().CreateTable(&models.BookmarkFolderItem{}); err != nil {
			return err
		}
	}
	if db.Migrator().HasTable(&models.Status{}) && !db.Migrator().HasColumn(&models.Status{}, "PinnedReplyId") {
		if err := db.Migrator().AddColumn(&models.Status{}, "PinnedReplyId"); err != nil {
			return err
		}
	}
	return nil
}

func (CommunityEnhancementsScript) Down(db *gorm.DB) error {
	if db.Migrator().HasTable(&models.Status{}) && db.Migrator().HasColumn(&models.Status{}, "PinnedReplyId") {
		if err := db.Migrator().DropColumn(&models.Status{}, "PinnedReplyId"); err != nil {
			return err
		}
	}
	if db.Migrator().HasTable(&models.BookmarkFolderItem{}) {
		if err := db.Migrator().DropTable(&models.BookmarkFolderItem{}); err != nil {
			return err
		}
	}
	if db.Migrator().HasTable(&models.BookmarkFolder{}) {
		if err := db.Migrator().DropTable(&models.BookmarkFolder{}); err != nil {
			return err
		}
	}
	return nil
}
