package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// TokenLastUsedScript adds auth_tokens.last_used_at and revoke_reason. Existing rows stay NULL
// ("never used"); the inactivity rule then falls back to created_at.
type TokenLastUsedScript struct{}

func (TokenLastUsedScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(&models.AuthToken{}) {
		return nil
	}
	for _, field := range []string{"LastUsedAt", "RevokeReason"} {
		if !m.HasColumn(&models.AuthToken{}, field) {
			if err := m.AddColumn(&models.AuthToken{}, field); err != nil {
				return err
			}
		}
	}
	return nil
}

func (TokenLastUsedScript) Down(*gorm.DB) error { return nil }
