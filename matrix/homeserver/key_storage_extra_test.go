package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== KeyStorage.GetPublicKey tests =====

func TestKeyStorage_GetPublicKey_NotFound(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	_, err := ks.GetPublicKey("ed25519:nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
}

func TestKeyStorage_GetPublicKey_Found(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	pub, _ := ed25519Key()
	pubB64 := base64.RawStdEncoding.EncodeToString(pub)
	rec := MatrixFederationKey{
		KeyID:     "ed25519:getpub",
		KeyType:   "federation",
		PublicKey: pubB64,
		IsActive:  true,
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	got, err := ks.GetPublicKey("ed25519:getpub")
	require.NoError(t, err)
	assert.Equal(t, ed25519.PublicKey(pub), got)
}

func TestKeyStorage_GetPublicKey_InvalidBase64(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	rec := MatrixFederationKey{
		KeyID:     "ed25519:badpub",
		PublicKey: "!!!not-base64!!!",
		IsActive:  true,
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	_, err := ks.GetPublicKey("ed25519:badpub")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode public key")
}

// ===== KeyStorage.GetActiveKey tests =====

func TestKeyStorage_GetActiveKey_None(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	_, err := ks.GetActiveKey()
	require.Error(t, err)
}

func TestKeyStorage_GetActiveKey_Found(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	pub, _ := ed25519Key()
	rec := MatrixFederationKey{
		KeyID:     "ed25519:active1",
		PublicKey: base64.RawStdEncoding.EncodeToString(pub),
		IsActive:  true,
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	active, err := ks.GetActiveKey()
	require.NoError(t, err)
	assert.Equal(t, "ed25519:active1", active.KeyID)
	assert.True(t, active.IsActive)
}

// ===== KeyStorage.RotateKey tests =====

func TestKeyStorage_RotateKey_DeactivatesOld(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	// Generate an initial key
	priv1, pub1, keyID1, err := ks.LoadOrGenerateKey()
	require.NoError(t, err)

	// Wait > 1s so the next generated key gets a distinct unix-second keyID
	// (keyID is formatted as ed25519:<unix_seconds>, so same-second rotations
	// would collide on the unique key_id index).
	time.Sleep(1100 * time.Millisecond)

	// Rotate: old key should be deactivated, new key generated
	priv2, pub2, keyID2, err := ks.RotateKey()
	require.NoError(t, err)

	assert.NotEqual(t, keyID1, keyID2, "rotated key should have a new ID")
	assert.NotEqual(t, priv1, priv2, "rotated key should differ from old key")
	assert.NotEqual(t, pub1, pub2, "rotated key should differ from old key")

	// The new key should be the active one
	active, err := ks.GetActiveKey()
	require.NoError(t, err)
	assert.Equal(t, keyID2, active.KeyID)
}

// ===== KeyStorage.ListKeys tests =====

func TestKeyStorage_ListKeys_Empty(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	keys, err := ks.ListKeys()
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestKeyStorage_ListKeys_WithKeys(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	// Generate two keys. Wait > 1s between generations so each gets a distinct
	// unix-second keyID (keyID = ed25519:<unix_seconds>).
	_, _, keyID1, err := ks.LoadOrGenerateKey()
	require.NoError(t, err)
	time.Sleep(1100 * time.Millisecond)
	_, _, _, err = ks.RotateKey() // generates a second key
	require.NoError(t, err)

	keys, err := ks.ListKeys()
	require.NoError(t, err)
	assert.Len(t, keys, 2)

	// Should be ordered by created_at DESC
	assert.Equal(t, keyID1, keys[1].KeyID, "older key should be last")
}

// ===== KeyStorage.DeleteKey tests =====

func TestKeyStorage_DeleteKey(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	pub, _ := ed25519Key()
	rec := MatrixFederationKey{
		KeyID:     "ed25519:deleteme",
		PublicKey: base64.RawStdEncoding.EncodeToString(pub),
		IsActive:  false,
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	err := ks.DeleteKey("ed25519:deleteme")
	require.NoError(t, err)

	// Should no longer be retrievable
	_, err = ks.GetPublicKey("ed25519:deleteme")
	require.Error(t, err)
}

func TestKeyStorage_DeleteKey_NonExistent(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	// Deleting a non-existent key should not error (gorm Delete is idempotent
	// when there are zero rows affected, but returns no error).
	err := ks.DeleteKey("ed25519:doesnotexist")
	assert.NoError(t, err)
}

// ===== KeyStorage encrypt/decrypt with EncryptedKey configured =====

func TestKeyStorage_EncryptDecrypt_WithSecret(t *testing.T) {
	// Configure an encryption secret.
	savedSecret := config.C.EncryptedKey
	config.C.EncryptedKey = "test-secret-key-for-encryption"
	defer func() { config.C.EncryptedKey = savedSecret }()

	ks := NewKeyStorage()
	plaintext := []byte("super-secret-key-material")

	encrypted, err := ks.encryptKey(plaintext)
	require.NoError(t, err)
	assert.Contains(t, encrypted, "enc:", "encrypted value should have enc: prefix")

	decrypted, err := ks.decryptKey(encrypted)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestKeyStorage_EncryptKey_NoSecretReturnsBase64(t *testing.T) {
	savedSecret := config.C.EncryptedKey
	config.C.EncryptedKey = ""
	defer func() { config.C.EncryptedKey = savedSecret }()

	ks := NewKeyStorage()
	plaintext := []byte("plain-data")

	result, err := ks.encryptKey(plaintext)
	require.NoError(t, err)
	assert.Equal(t, base64.RawStdEncoding.EncodeToString(plaintext), result)
	assert.NotContains(t, result, "enc:")
}

func TestKeyStorage_DecryptKey_NotEncryptedFormat(t *testing.T) {
	savedSecret := config.C.EncryptedKey
	config.C.EncryptedKey = "test-secret"
	defer func() { config.C.EncryptedKey = savedSecret }()

	ks := NewKeyStorage()
	// Value without "enc:" prefix → error
	_, err := ks.decryptKey("plainbase64data")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an encrypted key")
}

func TestKeyStorage_DecryptKey_NoSecretConfigured(t *testing.T) {
	savedSecret := config.C.EncryptedKey
	config.C.EncryptedKey = ""
	defer func() { config.C.EncryptedKey = savedSecret }()

	ks := NewKeyStorage()
	// "enc:" prefix present but no secret configured → error
	_, err := ks.decryptKey("enc:somedata")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encryption key not configured")
}

func TestKeyStorage_DecryptKey_TamperedCiphertext(t *testing.T) {
	savedSecret := config.C.EncryptedKey
	config.C.EncryptedKey = "test-secret"
	defer func() { config.C.EncryptedKey = savedSecret }()

	ks := NewKeyStorage()
	// Encrypt valid data, then tamper with the ciphertext.
	encrypted, err := ks.encryptKey([]byte("data"))
	require.NoError(t, err)

	// Flip a byte in the base64 payload to break GCM authentication.
	tampered := encrypted[:8] // "enc:"
	// Replace the base64 portion with garbage of valid length.
	payload := encrypted[8:]
	if len(payload) > 0 {
		flipped := make([]byte, len(payload))
		copy(flipped, payload)
		flipped[0] = flipByte(payload[0])
		tampered += string(flipped)
	} else {
		tampered += "AAAA"
	}

	_, err = ks.decryptKey(tampered)
	require.Error(t, err)
}

func TestKeyStorage_DecryptKey_ShortData(t *testing.T) {
	savedSecret := config.C.EncryptedKey
	config.C.EncryptedKey = "test-secret"
	defer func() { config.C.EncryptedKey = savedSecret }()

	ks := NewKeyStorage()
	// "enc:" + a very short payload (shorter than nonce size) → error
	short := "enc:" + base64.RawStdEncoding.EncodeToString([]byte("ab"))
	_, err := ks.decryptKey(short)
	require.Error(t, err)
}

// flipByte toggles the lowest bit of b to produce a different byte.
func flipByte(b byte) byte {
	return b ^ 0x01
}

// ===== KeyStorage.LoadOrGenerateKey: encrypted round-trip =====

func TestKeyStorage_LoadOrGenerateKey_EncryptedRoundTrip(t *testing.T) {
	savedSecret := config.C.EncryptedKey
	config.C.EncryptedKey = "roundtrip-secret"
	defer func() { config.C.EncryptedKey = savedSecret }()

	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	priv1, pub1, keyID1, err := ks.LoadOrGenerateKey()
	require.NoError(t, err)
	require.NotEmpty(t, keyID1)

	// Second call should load the same key (now encrypted in DB).
	priv2, pub2, keyID2, err := ks.LoadOrGenerateKey()
	require.NoError(t, err)
	assert.Equal(t, keyID1, keyID2)
	assert.Equal(t, priv1, priv2)
	assert.Equal(t, pub1, pub2)
}

// ===== KeyCache: loadFromDB with invalid base64 =====

func TestKeyCache_LoadFromDB_InvalidBase64(t *testing.T) {
	setupMinimalTestDB(t)
	kc := NewKeyCache()

	rec := MatrixServerKey{
		VerificationPolicy: currentKeyVerificationPolicy,
		ServerName:         "badkey.test",
		KeyID:              "ed25519:1",
		PublicKey:          "!!!invalid-base64!!!",
		ValidUntil:         time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	// Clear in-memory cache to force DB load.
	kc.cache = make(map[string]*KeyCacheEntry)

	got, found := kc.Get("badkey.test", "ed25519:1")
	assert.False(t, found, "invalid base64 key should not be loadable")
	assert.Nil(t, got)
}
