package scripts

import (
	"strings"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"gorm.io/gorm"
)

// NormalizeStorageKeysScript rewrites file_path values that older S3 uploads
// stored as full bucket URLs (e.g. https://<endpoint>/<bucket>/media/...)
// back to storage keys, the form every other row uses. Rows are also
// normalised on read, so this only tidies the data; it never touches the
// stored objects. Idempotent.
type NormalizeStorageKeysScript struct{}

func (NormalizeStorageKeysScript) Up(db *gorm.DB) error {
	if db.Migrator().HasTable("media_attachments") {
		var rows []struct {
			Id       uint64
			FilePath string
		}
		if err := db.Table("media_attachments").Select("id, file_path").
			Where("file_path LIKE ? OR file_path LIKE ?", "http://%", "https://%").Scan(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			key := models.MediaStorageKey(r.FilePath)
			if !strings.HasPrefix(key, models.MediaKeyPrefix) {
				continue // not one of ours; leave it alone
			}
			if err := db.Table("media_attachments").Where("id = ?", r.Id).
				Update("file_path", strings.TrimPrefix(key, models.MediaKeyPrefix)).Error; err != nil {
				return err
			}
		}
	}

	if db.Migrator().HasTable("matrix_media") {
		var rows []struct {
			Id       uint64
			FilePath string
		}
		if err := db.Table("matrix_media").Select("id, file_path").
			Where("file_path LIKE ? OR file_path LIKE ?", "http://%", "https://%").Scan(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			key, _ := storage.NormalizeKey(r.FilePath)
			if !strings.HasPrefix(key, "matrix/") {
				continue
			}
			if err := db.Table("matrix_media").Where("id = ?", r.Id).Update("file_path", key).Error; err != nil {
				return err
			}
		}
	}

	return nil
}

func (NormalizeStorageKeysScript) Down(*gorm.DB) error { return nil }
