package datatransfer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/kasuganosora/community/config"
)

// Credential, key and raw-record spool entries are sealed with AES-256-GCM before they reach spool.sqlite3, so the plaintext
// never rests in the work directory, whichever way a run ends: success
// wipes the spool, but a failed or interrupted run keeps it for
// --reuse-spool. The key derives from the target's ENCRYPTED_KEY, which
// lives in the environment, not the work directory. Preserved raw records
// have their second-factor fields redacted instead (they are never needed).

var sealedSpoolPrefix = []byte("community-transfer-sealed:v1:")

// sealedSection reports spool sections whose records are sealed whole:
// credentials (password hashes, TOTP secrets, backup codes), keys (actor
// private keys) and records (complete raw source rows: OAuth app secrets,
// access tokens, webhook and instance API secrets, keypairs, profiles).
func sealedSection(section string) bool {
	return section == "credentials" || section == "keys" || section == "records"
}

// ErrSpoolKey is returned when sealed spool records cannot be opened with the
// configured ENCRYPTED_KEY (it changed since the spool was written).
var ErrSpoolKey = errors.New("spool credentials were sealed with a different ENCRYPTED_KEY; restore it or rerun without --reuse-spool")

func spoolAEAD() (cipher.AEAD, bool) {
	if config.C.EncryptedKey == "" {
		return nil, false
	}
	m := hmac.New(sha256.New, []byte(config.C.EncryptedKey))
	m.Write([]byte("community-transfer spool seal v1"))
	block, err := aes.NewCipher(m.Sum(nil))
	if err != nil {
		return nil, false
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false
	}
	return aead, true
}

// SpoolSealingAvailable reports whether credential records will be sealed.
func SpoolSealingAvailable() bool {
	_, ok := spoolAEAD()
	return ok
}

func sealSpoolRecord(raw []byte) ([]byte, error) {
	aead, ok := spoolAEAD()
	if !ok {
		return raw, nil
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{}, sealedSpoolPrefix...)
	out = append(out, nonce...)
	return aead.Seal(out, nonce, raw, sealedSpoolPrefix), nil
}

func openSpoolRecord(raw []byte) ([]byte, error) {
	if !bytes.HasPrefix(raw, sealedSpoolPrefix) {
		return raw, nil
	}
	aead, ok := spoolAEAD()
	if !ok {
		return nil, ErrSpoolKey
	}
	body := raw[len(sealedSpoolPrefix):]
	if len(body) < aead.NonceSize() {
		return nil, ErrSpoolKey
	}
	plain, err := aead.Open(nil, body[:aead.NonceSize()], body[aead.NonceSize():], sealedSpoolPrefix)
	if err != nil {
		return nil, ErrSpoolKey
	}
	return plain, nil
}

// redactPreservedRecord strips second-factor secrets from a spooled raw
// record before it is written.
func redactPreservedRecord(raw []byte) []byte {
	if !bytes.Contains(raw, []byte("twoFactor")) {
		return raw
	}
	var rec map[string]json.RawMessage
	if json.Unmarshal(raw, &rec) != nil {
		return raw
	}
	data, changed := RedactSecretFields(rec["data"])
	if !changed {
		return raw
	}
	rec["data"] = data
	out, err := json.Marshal(rec)
	if err != nil {
		return raw
	}
	return out
}
