package scripts

import (
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// CreateMissingModelTablesScript creates tables for GORM models that are used
// by the application but were never created by any earlier migration:
//
//   - media_attachments (used by /api/v1/media and /api/v2/media uploads, and
//     required by MigrateMediaTablesScript / CreatePerformanceIndexesScript)
//   - announcements     (used by /api/v1/announcements)
//   - domain_blocks     (used by tootctl domains)
//
// Without it a fresh install could not upload media at all ("Table
// 'media_attachments' doesn't exist"). AutoMigrate is idempotent, so this is
// safe on installations that created the tables manually.
type CreateMissingModelTablesScript struct{}

func (CreateMissingModelTablesScript) Up(db *gorm.DB) error {
	log.Info("Creating missing model tables...")

	return db.AutoMigrate(
		&models.MediaAttachment{},
		&models.Announcement{},
		&models.DomainBlock{},
	)
}

func (CreateMissingModelTablesScript) Down(db *gorm.DB) error {
	return db.Migrator().DropTable(
		&models.DomainBlock{},
		&models.Announcement{},
		&models.MediaAttachment{},
	)
}
