package scripts

import (
	"fmt"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// UserSignInScript adds Mastodon-style web sign-in columns. Existing web
// sessions backfill the timestamp from auth_tokens; IP stays empty because
// it was never stored.
type UserSignInScript struct{}

func (UserSignInScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable(&models.User{}) {
		return nil
	}
	for _, field := range []string{"CurrentSignInAt", "LastSignInAt", "CurrentSignInIP", "LastSignInIP", "SignInCount"} {
		if !m.HasColumn(&models.User{}, field) {
			if err := m.AddColumn(&models.User{}, field); err != nil {
				return err
			}
		}
	}
	if !m.HasTable(&models.AuthToken{}) {
		return nil
	}
	tokenType := quoteIdent(db, "type")

	return db.Exec(fmt.Sprintf(`UPDATE users
SET current_sign_in_at = (
  SELECT MAX(created_at) FROM auth_tokens
  WHERE auth_tokens.user_id = users.id AND auth_tokens.%s = 0
),
last_sign_in_at = (
  SELECT MAX(created_at) FROM auth_tokens
  WHERE auth_tokens.user_id = users.id AND auth_tokens.%s = 0
)
WHERE current_sign_in_at IS NULL
AND EXISTS (
  SELECT 1 FROM auth_tokens
  WHERE auth_tokens.user_id = users.id AND auth_tokens.%s = 0
)`, tokenType, tokenType, tokenType)).Error
}

func (UserSignInScript) Down(*gorm.DB) error { return nil }
