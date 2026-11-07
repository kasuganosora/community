package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MediaProcessingScript adds the processing columns and the durable task
// table. Existing attachments are marked ready. Running it twice is a no-op.
type MediaProcessingScript struct{}

func (MediaProcessingScript) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&models.MediaAttachment{}, &models.MediaProcessTask{}, &models.MediaQueueGate{}); err != nil {
		return err
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.MediaQueueGate{Name: "video"}).Error; err != nil {
		return err
	}
	return db.Exec(`UPDATE media_attachments SET process_state = 'ready' WHERE process_state IS NULL OR process_state = ''`).Error
}

func (MediaProcessingScript) Down(*gorm.DB) error { return nil }
