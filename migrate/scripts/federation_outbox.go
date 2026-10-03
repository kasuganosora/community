package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// AddFederationOutboxScript adds durable transactional local federation events.
type AddFederationOutboxScript struct{}

func (AddFederationOutboxScript) Up(tx *gorm.DB) error {
	return tx.AutoMigrate(&models.FederationOutbox{})
}
func (AddFederationOutboxScript) Down(tx *gorm.DB) error {
	return tx.Migrator().DropTable(&models.FederationOutbox{})
}
