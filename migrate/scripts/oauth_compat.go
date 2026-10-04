package scripts

import (
	"github.com/kasuganosora/community/controllers/api/oauth"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// OAuthCompatScript widens OAuth columns and creates MiAuth, PKCE and passkey tables.
type OAuthCompatScript struct{}

func (OAuthCompatScript) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&oauth.OAuthAuthorizationCode{},
		&models.AuthToken{},
		&models.MisskeyAuthSession{},
		&models.PasskeyCredential{},
		&models.WebAuthnSession{},
	); err != nil {
		return err
	}

	if db.Migrator().HasColumn(&models.Application{}, "Name") {
		if err := db.Migrator().AlterColumn(&models.Application{}, "Name"); err != nil {
			return err
		}
	}

	if db.Migrator().HasColumn(&models.Application{}, "RedirectUri") {
		if err := db.Migrator().AlterColumn(&models.Application{}, "RedirectUri"); err != nil {
			return err
		}
	}

	return nil
}

func (OAuthCompatScript) Down(db *gorm.DB) error {
	return db.Migrator().DropTable(&models.WebAuthnSession{}, &models.PasskeyCredential{}, &models.MisskeyAuthSession{})
}
