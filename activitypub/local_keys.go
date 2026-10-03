package activitypub

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// localAccountKey creates one persistent signing identity for a local actor.
// Locking the owning account serializes concurrent first actor requests, so
// clients never observe one public key while deliveries use a different key.
func localAccountKey(account *models.Account) (*models.AccountKey, error) {
	if account == nil || account.Id == 0 || account.Domain != nil {
		return nil, errors.New("signing keys require a local account")
	}
	var stored models.AccountKey
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		var owner models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND domain IS NULL", account.Id).First(&owner).Error; err != nil {
			return err
		}
		err := tx.Where("account_id = ?", account.Id).First(&stored).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return err
		}
		public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err != nil {
			return err
		}
		stored = models.AccountKey{AccountID: account.Id, PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public})), PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))}
		return tx.Create(&stored).Error
	})
	return &stored, err
}
func localAccountClient(account *models.Account, actorURL string) (*Client, error) {
	key, err := localAccountKey(account)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(key.PrivateKey))
	if block == nil {
		return nil, errors.New("invalid local signing key PEM")
	}
	private, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse local signing key: %w", err)
	}
	return NewClient(private, actorURL), nil
}
