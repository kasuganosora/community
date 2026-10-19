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

// localAccountKey returns the one persistent signing identity of a local
// actor, creating it on first use. Concurrent first callers all observe the
// same key, so clients never see one public key while deliveries sign with
// a different one.
func localAccountKey(account *models.Account) (*models.AccountKey, error) {
	return localAccountKeyWith(db.GetDB(), account)
}

// localKeyAttempts bounds how often the slow path re-plans when the key row
// changes between the lock-free read and the locked re-check.
const localKeyAttempts = 3

var errLocalKeyChanged = errors.New("local signing key changed concurrently")

type localKeyAction int

const (
	localKeyReady localKeyAction = iota
	localKeyGenerate
	localKeyReplace
)

// localKeyPlan is what one read of account_keys/account_federation_keys says
// must happen before the actor can sign.
type localKeyPlan struct {
	action      localKeyAction
	stored      models.AccountKey
	replacement models.AccountKey
}

// localAccountKeyWith is localAccountKey on an explicit connection.
//
// Every signed delivery, actor document and relay request goes through here,
// so the common case (the key already exists and is usable) is answered by
// plain reads with no transaction and no row lock. Only a missing key or an
// imported key that must be swapped for its active successor takes the
// account row lock, and that transaction only re-checks and writes: the RSA
// key is generated before it starts.
func localAccountKeyWith(conn *gorm.DB, account *models.Account) (*models.AccountKey, error) {
	if account == nil || account.Id == 0 || account.Domain != nil {
		return nil, errors.New("signing keys require a local account")
	}
	var generated *models.AccountKey
	for attempt := 0; ; attempt++ {
		plan, err := planLocalAccountKey(conn, account.Id, false)
		if err != nil {
			return nil, err
		}
		if plan.action == localKeyReady {
			return &plan.stored, nil
		}
		if plan.action == localKeyGenerate && generated == nil {
			if generated, err = newAccountKey(account.Id); err != nil {
				return nil, err
			}
		}
		stored, err := applyLocalAccountKey(conn, account.Id, generated)
		if errors.Is(err, errLocalKeyChanged) && attempt+1 < localKeyAttempts {
			continue
		}
		return stored, err
	}
}

// applyLocalAccountKey is the slow path: lock the owner, re-plan under the
// lock (another caller may have finished first) and perform the write.
// generated is a pre-generated keypair; it is only inserted when the key is
// still missing, and the unique account_id index plus ON CONFLICT DO NOTHING
// keeps writers that do not take the account lock from producing a second key.
func applyLocalAccountKey(conn *gorm.DB, accountID uint64, generated *models.AccountKey) (*models.AccountKey, error) {
	var stored models.AccountKey
	err := conn.Transaction(func(tx *gorm.DB) error {
		plan, err := planLocalAccountKey(tx, accountID, true)
		if err != nil {
			return err
		}
		switch plan.action {
		case localKeyReady:
			stored = plan.stored
			return nil
		case localKeyReplace:
			result := tx.Model(&models.AccountKey{}).Where("id = ?", plan.stored.ID).
				Updates(map[string]any{"key_id": plan.replacement.KeyID, "public_key": plan.replacement.PublicKey, "private_key": plan.replacement.PrivateKey})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return errLocalKeyChanged
			}
			stored = plan.replacement
			return nil
		}
		if generated == nil {
			return errLocalKeyChanged
		}
		candidate := *generated
		candidate.ID, candidate.CreatedAt = 0, nil
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
			return err
		}
		return tx.Where("account_id = ?", accountID).First(&stored).Error
	})
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

// planLocalAccountKey reads the owner and its keys and decides whether the
// stored key can be used as is. With lock set the owner row is read FOR
// UPDATE; callers must then be inside a transaction.
func planLocalAccountKey(conn *gorm.DB, accountID uint64, lock bool) (localKeyPlan, error) {
	var plan localKeyPlan
	q := conn.Select("id", "local_actor_uri").Where("id = ? AND domain IS NULL", accountID)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var owner models.Account
	if err := q.Take(&owner).Error; err != nil {
		return plan, err
	}
	// Find, not Take: a missing key is expected on first use, not an error to log.
	result := conn.Where("account_id = ?", accountID).Limit(1).Find(&plan.stored)
	if result.Error != nil {
		return plan, result.Error
	}
	if result.RowsAffected == 0 {
		if owner.LocalActorURI != "" {
			return plan, errors.New("imported actor has no preserved signing key")
		}
		plan.action = localKeyGenerate
		return plan, nil
	}
	if owner.LocalActorURI == "" {
		return plan, nil
	}
	var keys []models.AccountFederationKey
	if err := conn.Where("account_id = ?", owner.Id).Order("id ASC").Find(&keys).Error; err != nil {
		return plan, err
	}
	now := time.Now()
	usable := func(k models.AccountFederationKey) bool {
		return !k.Revoked && (k.ExpiresAt == nil || k.ExpiresAt.After(now)) && supportedRSAAlgorithm(k.Algorithm) && k.PrivateKey != ""
	}
	found := false
	for _, k := range keys {
		if k.KeyID == plan.stored.SigningKeyID(owner.LocalActorURI) {
			found = true
			if usable(k) {
				return plan, nil
			}
			break
		}
	}
	// Older imports without lifecycle metadata retain their explicitly stored key.
	if !found {
		return plan, nil
	}
	for _, k := range keys {
		if usable(k) {
			replacement := models.AccountKey{ID: plan.stored.ID, AccountID: owner.Id, KeyID: k.KeyID, PublicKey: k.PublicKey, PrivateKey: k.PrivateKey, CreatedAt: plan.stored.CreatedAt}
			if replacement.ValidateKeyPair() != nil {
				continue
			}
			plan.action, plan.replacement = localKeyReplace, replacement
			return plan, nil
		}
	}
	return plan, errors.New("imported actor has no active signing key")
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
