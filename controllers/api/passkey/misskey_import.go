package passkey

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// MisskeySecurityKey is one row of Misskey's user_security_key table joined
// with its local owner.
type MisskeySecurityKey struct {
	ID                   string // credential id, base64url
	UserID               string // Misskey user id (also the WebAuthn user handle)
	Username             string
	PublicKey            string // base64url COSE key
	Name                 string
	Counter              int64
	CredentialDeviceType string // "singleDevice" | "multiDevice"
	CredentialBackedUp   bool
	Transports           []string
	LastUsed             *time.Time
}

// misskeySecurityKeysSQL reads local users' keys from a Misskey PostgreSQL
// database. transports is a varchar[] there; it is flattened to text so the
// scan does not depend on the driver's array support.
const misskeySecurityKeysSQL = `SELECT k.id, k."userId" AS user_id, u.username, k."publicKey" AS public_key,
  COALESCE(k.name, '') AS name, COALESCE(k.counter, 0) AS counter,
  COALESCE(k."credentialDeviceType", '') AS device_type, COALESCE(k."credentialBackedUp", false) AS backed_up,
  COALESCE(array_to_string(k.transports, ','), '') AS transports, k."lastUsed" AS last_used
FROM user_security_key k JOIN "user" u ON u.id = k."userId"
WHERE u.host IS NULL
ORDER BY k.id`

// LoadMisskeySecurityKeys reads all passkeys of local Misskey users.
func LoadMisskeySecurityKeys(src *gorm.DB) ([]MisskeySecurityKey, error) {
	rows, err := src.Raw(misskeySecurityKeysSQL).Rows()
	if err != nil {
		return nil, errors.Wrap(err, "query misskey user_security_key")
	}
	defer func() { _ = rows.Close() }()

	var out []MisskeySecurityKey
	for rows.Next() {
		var (
			k          MisskeySecurityKey
			transports string
		)
		if err = rows.Scan(&k.ID, &k.UserID, &k.Username, &k.PublicKey, &k.Name, &k.Counter,
			&k.CredentialDeviceType, &k.CredentialBackedUp, &transports, &k.LastUsed); err != nil {
			return nil, errors.Wrap(err, "scan misskey user_security_key")
		}

		for _, t := range strings.Split(transports, ",") {
			if t = strings.TrimSpace(t); t != "" {
				k.Transports = append(k.Transports, t)
			}
		}

		out = append(out, k)
	}

	return out, rows.Err()
}

// decodeB64URL accepts base64url with or without padding (and standard
// base64 as a fallback).
func decodeB64URL(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}

	return nil, errors.New("invalid base64")
}

// ConvertMisskeySecurityKey builds the go-webauthn credential stored in
// passkey_credentials.credential_json. The backup flags must match what the
// authenticator reports, otherwise go-webauthn rejects the login.
func ConvertMisskeySecurityKey(k MisskeySecurityKey) (*webauthn.Credential, error) {
	id, err := decodeB64URL(k.ID)
	if err != nil || len(id) == 0 {
		return nil, errors.New("invalid credential id")
	}

	pub, err := decodeB64URL(k.PublicKey)
	if err != nil || len(pub) == 0 {
		return nil, errors.New("invalid public key")
	}

	if _, err = webauthncose.ParsePublicKey(pub); err != nil {
		return nil, errors.New("unsupported public key")
	}

	backup := k.CredentialDeviceType == "multiDevice" || k.CredentialBackedUp
	cred := &webauthn.Credential{
		ID:              id,
		PublicKey:       pub,
		AttestationType: "none",
		Flags: webauthn.CredentialFlags{
			UserPresent:    true,
			BackupEligible: backup,
			BackupState:    backup,
		},
		Authenticator: webauthn.Authenticator{SignCount: clampCounter(k.Counter)},
	}
	for _, t := range k.Transports {
		cred.Transport = append(cred.Transport, protocol.AuthenticatorTransport(t))
	}

	return cred, nil
}

func clampCounter(n int64) uint32 {
	if n < 0 {
		return 0
	}

	if n > int64(^uint32(0)) {
		return ^uint32(0)
	}

	return uint32(n)
}

// MisskeyImportResult counts what an import did (or would do).
type MisskeyImportResult struct {
	Imported  int
	Skipped   int // credential id already present
	Unmatched int // no local Community account with that username
	Invalid   int // undecodable id or key
	Details   []string
}

// ImportMisskeySecurityKeys writes keys into passkey_credentials, matching
// Misskey usernames to local Community accounts case-insensitively. Existing
// credential ids are skipped, so reruns are idempotent. Messages in Details
// never contain key material.
func ImportMisskeySecurityKeys(dst *gorm.DB, keys []MisskeySecurityKey, dryRun bool) (*MisskeyImportResult, error) {
	res := &MisskeyImportResult{}
	userIDs := map[string]uint64{}

	for _, k := range keys {
		cred, err := ConvertMisskeySecurityKey(k)
		if err != nil {
			res.Invalid++
			res.Details = append(res.Details, fmt.Sprintf("invalid: @%s key %q: %v", k.Username, k.Name, err))

			continue
		}

		lower := strings.ToLower(k.Username)
		userID, ok := userIDs[lower]
		if !ok {
			acc := &models.Account{}
			q := models.WhereAcct(dst.Model(&models.Account{}), k.Username, "", false)
			if err = q.Order("accounts.id").First(acc).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return res, errors.Wrap(err, "look up account")
			}

			userID = acc.UserId
			userIDs[lower] = userID
		}

		if userID == 0 {
			res.Unmatched++
			res.Details = append(res.Details, fmt.Sprintf("unmatched: @%s key %q: no local account", k.Username, k.Name))

			continue
		}

		credID := base64.RawURLEncoding.EncodeToString(cred.ID)
		var n int64
		if err = dst.Model(&models.PasskeyCredential{}).Where("credential_id = ?", credID).Count(&n).Error; err != nil {
			return res, errors.Wrap(err, "check credential")
		}

		if n > 0 {
			res.Skipped++

			continue
		}

		res.Imported++
		res.Details = append(res.Details, fmt.Sprintf("import: @%s key %q", k.Username, k.Name))
		if dryRun {
			continue
		}

		raw, err := json.Marshal(cred)
		if err != nil {
			return res, err
		}

		name := clipRunes(k.Name, 64)
		if name == "" {
			name = "Passkey"
		}

		row := &models.PasskeyCredential{
			UserId:         userID,
			CredentialID:   credID,
			CredentialJSON: raw,
			Name:           name,
			UserHandle:     base64.RawURLEncoding.EncodeToString([]byte(k.UserID)),
			LastUsedAt:     k.LastUsed,
		}
		if err = dst.Create(row).Error; err != nil {
			return res, errors.Wrap(err, "store credential")
		}
	}

	return res, nil
}
