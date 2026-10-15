package scripts

import "gorm.io/gorm"

// PasskeyUserHandleScript adds passkey_credentials.user_handle, the WebAuthn
// user handle of passkeys imported from another server (Misskey uses its own
// user id). NOT NULL DEFAULT ” so it is safe on a populated table. Idempotent.
type PasskeyUserHandleScript struct{}

type passkeyUserHandleRow struct {
	UserHandle string `gorm:"type:varchar(255);not null;default:''"`
}

func (passkeyUserHandleRow) TableName() string { return "passkey_credentials" }

func (PasskeyUserHandleScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("passkey_credentials") || m.HasColumn(&passkeyUserHandleRow{}, "user_handle") {
		return nil
	}

	return m.AddColumn(&passkeyUserHandleRow{}, "user_handle")
}

func (PasskeyUserHandleScript) Down(*gorm.DB) error { return nil }
