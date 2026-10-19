package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// PersonalTokenScopeBitsScript adds personal_access_tokens.scope_bits and
// fills it from each row's scope name list, marked "migration": the auth
// middleware compares it with the token blob and keeps using the blob (raw
// tokens are not stored, so only auth time can see the blob). Rows whose list
// cannot be parsed stay NULL and are filled from the blob on first use.
type PersonalTokenScopeBitsScript struct{}

func (PersonalTokenScopeBitsScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(&models.PersonalAccessToken{}) {
		return nil
	}
	for _, field := range []string{"ScopeBits", "ScopeSource"} {
		if !m.HasColumn(&models.PersonalAccessToken{}, field) {
			if err := m.AddColumn(&models.PersonalAccessToken{}, field); err != nil {
				return err
			}
		}
	}
	var rows []models.PersonalAccessToken
	if err := db.Select("id", "scopes").Where("scope_bits IS NULL").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		bits, _, ok := models.ParsePersonalTokenScopes(row.Scopes)
		if !ok {
			continue
		}
		if err := db.Model(&models.PersonalAccessToken{}).Where("id = ? AND scope_bits IS NULL", row.ID).
			Select("scope_bits", "scope_source").Updates(&models.PersonalAccessToken{ScopeBits: (*models.ScopeBits)(&bits), ScopeSource: models.ScopeSourceMigration}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (PersonalTokenScopeBitsScript) Down(*gorm.DB) error { return nil }
