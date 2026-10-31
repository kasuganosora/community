package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// MediaDetachedAtScript adds media_attachments.detached_at. Status deletion
// sets it when unlinking media so a later purge can tell a deleted post's
// files from uploads that were never posted and from imported library files.
// Nullable, so existing rows stay unmarked. Idempotent.
type MediaDetachedAtScript struct{}

func (MediaDetachedAtScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(&models.MediaAttachment{}) {
		return nil
	}

	if !m.HasColumn(&models.MediaAttachment{}, "detached_at") {
		if err := m.AddColumn(&models.MediaAttachment{}, "DetachedAt"); err != nil {
			return err
		}
	}

	if m.HasIndex(&models.MediaAttachment{}, "DetachedAt") || m.HasIndex(&models.MediaAttachment{}, "idx_media_attachments_detached_at") {
		return nil
	}

	return m.CreateIndex(&models.MediaAttachment{}, "DetachedAt")
}

func (MediaDetachedAtScript) Down(*gorm.DB) error { return nil }
