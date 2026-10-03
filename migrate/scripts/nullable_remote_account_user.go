package scripts

import (
	"fmt"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// Remote accounts have no local login. Keep the FK; nullable ownership is the
// intended representation (a synthetic local user would grant false identity).
type NullableRemoteAccountUserScript struct{}

func (NullableRemoteAccountUserScript) Up(db *gorm.DB) error {
	if err := db.Migrator().AlterColumn(&models.Account{}, "UserId"); err != nil {
		return err
	}
	if err := db.Migrator().AlterColumn(&models.Account{}, "OriginId"); err != nil {
		return err
	}
	return db.AutoMigrate(&models.RemoteActorIdentity{})
}
func (NullableRemoteAccountUserScript) Down(db *gorm.DB) error {
	return fmt.Errorf("remote account ownership migration cannot be reversed without removing remote identities")
}
