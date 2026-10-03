package scripts

import (
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type AddDeliveryAuthorizationScript struct{}

func (AddDeliveryAuthorizationScript) Up(tx *gorm.DB) error {
	return tx.AutoMigrate(&activitypub.Delivery{}, &models.FederationOutbox{})
}
func (AddDeliveryAuthorizationScript) Down(tx *gorm.DB) error {
	return tx.Migrator().DropColumn(&activitypub.Delivery{}, "Authorization")
}
