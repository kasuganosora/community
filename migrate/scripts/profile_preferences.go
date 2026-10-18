package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type ProfilePreferencesScript struct{}
type profilePreferenceColumns struct {
	IsCat              bool   `gorm:"not null;default:false"`
	Location           string `gorm:"size:128;not null;default:''"`
	Birthday           string `gorm:"size:10;not null;default:''"`
	Language           string `gorm:"size:35;not null;default:''"`
	FollowedMessage    string `gorm:"size:200;not null;default:''"`
	ReactionAcceptance string `gorm:"size:64;not null;default:'all'"`
}

func (profilePreferenceColumns) TableName() string { return "accounts" }
func (ProfilePreferencesScript) Up(conn *gorm.DB) error {
	for _, column := range []string{"is_cat", "location", "birthday", "language", "followed_message", "reaction_acceptance"} {
		if !conn.Migrator().HasColumn(&profilePreferenceColumns{}, column) {
			if err := conn.Migrator().AddColumn(&profilePreferenceColumns{}, column); err != nil {
				return err
			}
		}
	}
	return conn.AutoMigrate(&models.ProfileFieldVerification{})
}
func (ProfilePreferencesScript) Down(*gorm.DB) error { return nil }
