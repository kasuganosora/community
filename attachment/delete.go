package attachment

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func Delete(fileId uint64) (err error, found bool) {
	attachment := &models.Attachment{}

	err = db.GetDB().Where("id = ?", fileId).First(attachment).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, true
		}

		err = errors.WithMessagef(err, "Get attachment %d have error: %v", fileId, err)

		return err, false
	}

	if attachment.SavePath != "" {
		err = storage.Delete(attachment.SavePath)
		if err != nil {
			return err, false
		}
	}

	if attachment.PreviewSavePath != "" {
		err = storage.Delete(attachment.PreviewSavePath)
		if err != nil {
			return err, false
		}
	}

	err = db.GetDB().Delete(attachment).Error

	return err, true
}
