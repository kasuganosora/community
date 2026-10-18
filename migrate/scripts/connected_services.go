package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type ConnectedServicesScript struct{}

func (ConnectedServicesScript) Up(db *gorm.DB) error {
	return db.AutoMigrate(&models.PersonalAccessToken{}, &models.UserWebhook{}, &models.UserWebhookDelivery{})
}
func (ConnectedServicesScript) Down(db *gorm.DB) error { return nil }
