package scripts

import (
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// UnifyMediaAttachmentsScript makes media_attachments the single attachment
// table. Uploads always wrote media_attachments while statuses referenced a
// never-migrated "attachments" table through status_media_attachments, so
// posting with media_ids failed. Statuses now link media through
// media_attachments.status_id.
//
// Up:
//  1. AutoMigrate MediaAttachment (adds remote_url).
//  2. Copy links from the legacy status_media_attachments(status_id, media_id)
//     table into media_attachments.status_id for rows that are not linked yet.
//  3. Drop the legacy status_media_attachments and attachments tables.
type UnifyMediaAttachmentsScript struct{}

func (UnifyMediaAttachmentsScript) Up(db *gorm.DB) error {
	log.Info("Unifying media attachments into media_attachments...")

	if err := db.AutoMigrate(&models.MediaAttachment{}); err != nil {
		return errors.Wrap(err, "auto-migrate media_attachments")
	}

	m := db.Migrator()

	if m.HasTable("status_media_attachments") && m.HasColumn(&models.StatusMediaAttachment{}, "media_id") {
		err := db.Exec(`UPDATE media_attachments
SET status_id = (
	SELECT sma.status_id FROM status_media_attachments sma
	WHERE sma.media_id = media_attachments.id
	ORDER BY sma.id LIMIT 1
)
WHERE status_id IS NULL
  AND id IN (SELECT media_id FROM status_media_attachments)
  AND EXISTS (
	SELECT 1 FROM status_media_attachments sma2
	JOIN statuses s ON s.id = sma2.status_id
	WHERE sma2.media_id = media_attachments.id
  )`).Error
		if err != nil {
			return errors.Wrap(err, "copy status_media_attachments links")
		}
	}

	// Plain DROP on the migration's own connection: Migrator().DropTable runs
	// on a separate pooled connection on MySQL and would wait forever for the
	// metadata lock the UPDATE above holds inside the migration transaction.
	for _, table := range []string{"status_media_attachments", "attachments"} {
		if m.HasTable(table) {
			if err := db.Exec("DROP TABLE IF EXISTS " + dialect.Quote(db, table)).Error; err != nil {
				return errors.Wrapf(err, "drop legacy table %s", table)
			}
		}
	}

	return nil
}

func (UnifyMediaAttachmentsScript) Down(db *gorm.DB) error {
	// Recreate the (empty) legacy join table; links stay in
	// media_attachments.status_id.
	return db.AutoMigrate(&models.StatusMediaAttachment{})
}
