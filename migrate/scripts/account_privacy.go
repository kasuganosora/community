package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// AccountPrivacyScript adds the Misskey account settings (per-list
// collection visibility, autoAcceptFollowed, hideOnlineStatus,
// lastActiveDate, speakAsCat). Accounts that hid their collections keep
// both lists private.
type AccountPrivacyScript struct{}

func (AccountPrivacyScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(&models.Account{}) {
		return nil
	}
	for _, field := range []string{"FollowingVisibility", "FollowersVisibility", "AutoAcceptFollowed", "HideOnlineStatus", "LastActiveAt", "SpeakAsCat"} {
		if !m.HasColumn(&models.Account{}, field) {
			if err := m.AddColumn(&models.Account{}, field); err != nil {
				return err
			}
		}
	}
	return db.Model(&models.Account{}).Unscoped().
		Where("hide_collections = ? AND following_visibility = ? AND followers_visibility = ?", true, models.CollectionPublic, models.CollectionPublic).
		UpdateColumns(map[string]any{"following_visibility": models.CollectionPrivate, "followers_visibility": models.CollectionPrivate}).Error
}

func (AccountPrivacyScript) Down(*gorm.DB) error { return nil }
