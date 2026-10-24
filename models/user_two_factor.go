package models

import (
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/utils"
)

// UserTwoFactor holds a user's TOTP state. Secret and PendingSecret are
// base32 TOTP secrets encrypted with the ENCRYPTED_KEY mechanism (per-row
// salt); they are never serialised to JSON.
type UserTwoFactor struct {
	UserId           uint64     `gorm:"primaryKey;autoIncrement:false;column:user_id" json:"-"`
	Salt             string     `gorm:"type:varchar(64);not null;default:''"          json:"-"`
	Secret           string     `gorm:"type:text"                                     json:"-"`
	PendingSecret    string     `gorm:"type:text"                                     json:"-"`
	PendingCreatedAt *time.Time `json:"-"`
	EnabledAt        *time.Time `json:"enabled_at"`
	LastUsedStep     int64      `gorm:"not null;default:0" json:"-"`
	FailedAttempts   int        `gorm:"not null;default:0" json:"-"`
	LockedUntil      *time.Time `json:"-"`
	// PinHash is a bcrypt hash of the optional security PIN. Empty means unset.
	// The PIN is an additional second factor; it never replaces TOTP.
	PinHash   string    `gorm:"type:varchar(255);not null;default:''" json:"-"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

func (UserTwoFactor) TableName() string { return "user_two_factors" }

// Enabled reports whether TOTP is active for the user.
func (t *UserTwoFactor) Enabled() bool { return t != nil && t.EnabledAt != nil && t.Secret != "" }

// UserBackupCode is a single-use 2FA recovery code, stored as a bcrypt hash.
type UserBackupCode struct {
	Id        uint64     `gorm:"primaryKey;autoIncrement"`
	UserId    uint64     `gorm:"not null;index:idx_user_backup_codes_user"`
	CodeHash  string     `gorm:"type:varchar(255);not null"`
	UsedAt    *time.Time `gorm:""`
	CreatedAt time.Time
}

func (UserBackupCode) TableName() string { return "user_backup_codes" }

// DecryptSecretValue reverses EncryptSecretValue.
func DecryptSecretValue(value, salt string) (string, error) {
	if config.C.EncryptedKey == "" || value == "" {
		return value, nil
	}

	plain, err := utils.AesDecrypt(value, utils.GetEncryptKey(config.C.EncryptedKey, salt))
	if err != nil {
		return "", err
	}

	return string(plain), nil
}
