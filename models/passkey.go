package models

import "time"

// PasskeyCredential is a WebAuthn discoverable credential for a local user.
// CredentialID is base64url (no padding). CredentialJSON is the library
// credential, including the updated sign count.
type PasskeyCredential struct {
	Id             uint64     `gorm:"primaryKey;autoIncrement"`
	UserId         uint64     `gorm:"not null;index"`
	CredentialID   string     `gorm:"type:varchar(512);uniqueIndex;not null"`
	CredentialJSON []byte     `gorm:"type:blob;not null"`
	Name           string     `gorm:"type:varchar(64);not null"`
	CreatedAt      *time.Time `gorm:"autoCreateTime"`
	LastUsedAt     *time.Time
}

func (PasskeyCredential) TableName() string {
	return "passkey_credentials"
}

// WebAuthnSession holds the challenge between a passkey begin and finish call.
// The row id is an HttpOnly cookie value, not the challenge itself.
type WebAuthnSession struct {
	ID        string    `gorm:"primaryKey;type:varchar(64)"`
	UserId    uint64    `gorm:"index"`
	Kind      string    `gorm:"type:varchar(16);not null"`
	Data      []byte    `gorm:"type:blob;not null"`
	ExpiresAt time.Time `gorm:"not null;index"`
}

func (WebAuthnSession) TableName() string {
	return "webauthn_sessions"
}
