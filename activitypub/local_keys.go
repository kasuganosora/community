package activitypub

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// localAccountKey creates one persistent signing identity for a local actor.
// Locking the owning account serializes concurrent first actor requests, so
// clients never observe one public key while deliveries use a different key.
func localAccountKey(account *models.Account) (*models.AccountKey, error) {
	return localAccountKeyWith(db.GetDB(), account)
}

// localAccountKeyWith is localAccountKey on an explicit connection.
func localAccountKeyWith(conn *gorm.DB, account *models.Account) (*models.AccountKey, error) {
	if account == nil || account.Id == 0 || account.Domain != nil {
		return nil, errors.New("signing keys require a local account")
	}
	var stored models.AccountKey
	err := conn.Transaction(func(tx *gorm.DB) error {
		var owner models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND domain IS NULL", account.Id).First(&owner).Error; err != nil {
			return err
		}
		err := tx.Where("account_id = ?", account.Id).First(&stored).Error
		if err == nil {
			if owner.LocalActorURI == "" {
				return nil
			}
			var keys []models.AccountFederationKey
			if err := tx.Where("account_id = ?", owner.Id).Order("id ASC").Find(&keys).Error; err != nil {
				return err
			}
			now := time.Now()
			usable := func(k models.AccountFederationKey) bool {
				return !k.Revoked && (k.ExpiresAt == nil || k.ExpiresAt.After(now)) && supportedRSAAlgorithm(k.Algorithm) && k.PrivateKey != ""
			}
			found := false
			for _, k := range keys {
				if k.KeyID == stored.SigningKeyID(owner.LocalActorURI) {
					found = true
					if usable(k) {
						return nil
					}
					break
				}
			}
			// Older imports without lifecycle metadata retain their explicitly stored key.
			if !found {
				return nil
			}
			for _, k := range keys {
				if usable(k) {
					replacement := models.AccountKey{ID: stored.ID, AccountID: owner.Id, KeyID: k.KeyID, PublicKey: k.PublicKey, PrivateKey: k.PrivateKey}
					if replacement.ValidateKeyPair() != nil {
						continue
					}
					if err := tx.Model(&stored).Updates(map[string]any{"key_id": k.KeyID, "public_key": k.PublicKey, "private_key": k.PrivateKey}).Error; err != nil {
						return err
					}
					stored = replacement
					return nil
				}
			}
			return errors.New("imported actor has no active signing key")
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if owner.LocalActorURI != "" {
			return errors.New("imported actor has no preserved signing key")
		}
		generated, err := newAccountKey(account.Id)
		if err != nil {
			return err
		}
		stored = *generated
		return tx.Create(&stored).Error
	})
	return &stored, err
}

// newAccountKey generates a fresh RSA signing keypair for a local account.
func newAccountKey(accountID uint64) (*models.AccountKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &models.AccountKey{AccountID: accountID, PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public})), PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))}, nil
}
func localAccountClient(account *models.Account, actorURL string) (*Client, error) {
	key, err := localAccountKey(account)
	if err != nil {
		return nil, err
	}

	private, err := models.ParseAccountPrivateKey(key.PrivateKey)
	if err != nil {
		return nil, err
	}
	client := NewClient(private, actorURL)
	client.keyID = key.SigningKeyID(actorURL)
	return client, nil
}

func supportedRSAAlgorithm(algorithm string) bool {
	switch strings.ToLower(algorithm) {
	case "", "rsa", "rsa-sha256":
		return true
	}
	return false
}
