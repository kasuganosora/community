package models

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"gorm.io/gorm"
	"time"
)

// AccountFederationKey retains every source key, including expired/revoked
// material. Only valid active RSA public keys are advertised; AccountKey keeps
// the explicitly chosen primary signing identity.
type AccountFederationKey struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement"`
	AccountID  uint64 `gorm:"not null;uniqueIndex:idx_account_federation_key"`
	KeyID      string `gorm:"type:text;not null" json:"-"`
	KeyIDHash  string `gorm:"type:char(64);not null;uniqueIndex:idx_account_federation_key" json:"-"`
	PublicKey  string `gorm:"type:text;not null" json:"-"`
	PrivateKey string `gorm:"type:text" json:"-"`
	Algorithm  string `gorm:"type:varchar(32)"`
	Revoked    bool
	ExpiresAt  *time.Time
}

func (AccountFederationKey) TableName() string { return "account_federation_keys" }
func (k *AccountFederationKey) BeforeSave(*gorm.DB) error {
	if k.AccountID == 0 || k.KeyID == "" {
		return errors.New("federation key requires account and key ID")
	}
	sum := sha256.Sum256([]byte(k.KeyID))
	k.KeyIDHash = hex.EncodeToString(sum[:])
	return nil
}
