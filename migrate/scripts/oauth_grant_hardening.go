package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// OAuthGrantHardeningScript records the scopes a user actually granted and
// links each access token to its refresh token so rotation revokes both.
type OAuthGrantHardeningScript struct{}

func (OAuthGrantHardeningScript) Up(db *gorm.DB) error {
	return db.AutoMigrate(&models.ApplicationUserAuth{}, &models.AuthToken{})
}

func (OAuthGrantHardeningScript) Down(db *gorm.DB) error {
	return nil
}
