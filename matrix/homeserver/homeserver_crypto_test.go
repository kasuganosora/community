package homeserver

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// crypto_olm.go: pure cryptographic helper functions
// =============================================================================

func TestDeriveRootKey_Deterministic(t *testing.T) {
	dh1 := []byte("dh1-material-aaaaaa")
	dh2 := []byte("dh2-material-bbbbbb")
	dh3 := []byte("dh3-material-cccccc")

	key1 := deriveRootKey(dh1, dh2, dh3)
	key2 := deriveRootKey(dh1, dh2, dh3)

	// Same inputs → same output (deterministic)
	assert.Len(t, key1, 32)
	assert.Equal(t, key1, key2, "deriveRootKey should be deterministic")
}

func TestDeriveRootKey_DifferentInputs(t *testing.T) {
	key1 := deriveRootKey([]byte("input-a"), []byte("b"), []byte("c"))
	key2 := deriveRootKey([]byte("input-x"), []byte("b"), []byte("c"))
	assert.NotEqual(t, key1, key2, "different inputs should produce different keys")
}

func TestDeriveChainKey_Deterministic(t *testing.T) {
	rootKey := make([]byte, 32)
	copy(rootKey, "root-key-material--")

	enclave1 := deriveChainKey(rootKey, 1)
	enclave2 := deriveChainKey(rootKey, 1)

	buf1, err := enclave1.Open()
	require.NoError(t, err)
	defer buf1.Destroy()
	buf2, err := enclave2.Open()
	require.NoError(t, err)
	defer buf2.Destroy()

	assert.Len(t, buf1.Bytes(), 32)
	assert.Equal(t, buf1.Bytes(), buf2.Bytes(), "deriveChainKey should be deterministic")
}

func TestDeriveMessageKey_ProducesKeys(t *testing.T) {
	chainKey := make([]byte, 32)
	copy(chainKey, "chain-key-material-")

	msgKey, nextChainKey := deriveMessageKey(chainKey, 0)
	assert.Len(t, msgKey, 32)
	assert.Len(t, nextChainKey, 32)
	assert.NotEqual(t, msgKey, nextChainKey)
}

func TestEncryptAESGCM_DecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32) // AES-256
	copy(key, "aes-key-material-----")
	iv := make([]byte, 12) // GCM nonce
	copy(iv, "nonce-12byte")

	plaintext := []byte("secret message to encrypt")
	ciphertext, err := encryptAESGCM(plaintext, key, iv)
	require.NoError(t, err)
	assert.NotEqual(t, plaintext, ciphertext)

	// Decrypt should recover the original plaintext
	decrypted, err := decryptAESGCM(ciphertext, key, iv)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestDecryptAESGCM_WrongKey(t *testing.T) {
	key := make([]byte, 32)
	copy(key, "correct-key-material")
	wrongKey := make([]byte, 32)
	copy(wrongKey, "wrong-key-material!!")
	iv := make([]byte, 12)
	copy(iv, "nonce-12byte")

	ciphertext, err := encryptAESGCM([]byte("data"), key, iv)
	require.NoError(t, err)

	_, err = decryptAESGCM(ciphertext, wrongKey, iv)
	assert.Error(t, err, "decryption with wrong key should fail")
}

func TestEncryptAESGCM_InvalidKeySize(t *testing.T) {
	iv := make([]byte, 12)
	// AES requires 16, 24, or 32 byte keys
	_, err := encryptAESGCM([]byte("data"), []byte("short"), iv)
	assert.Error(t, err)
}

func TestDecryptAESGCM_TamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	copy(key, "key-material-32bytes")
	iv := make([]byte, 12)
	copy(iv, "nonce-12byt")

	ciphertext, err := encryptAESGCM([]byte("sensitive data"), key, iv)
	require.NoError(t, err)

	// Tamper with ciphertext
	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[0] ^= 0xff

	_, err = decryptAESGCM(tampered, key, iv)
	assert.Error(t, err, "tampered ciphertext should fail GCM authentication")
}

func TestComputeMAC_Deterministic(t *testing.T) {
	key := []byte("mac-key")
	data := []byte("data-to-authenticate")

	mac1 := computeMAC(data, key)
	mac2 := computeMAC(data, key)
	assert.Equal(t, mac1, mac2, "computeMAC should be deterministic")
	assert.Len(t, mac1, 16, "MAC should be 128 bits (16 bytes)")
}

func TestComputeMAC_DifferentKeys(t *testing.T) {
	data := []byte("data")
	mac1 := computeMAC(data, []byte("key1"))
	mac2 := computeMAC(data, []byte("key2"))
	assert.NotEqual(t, mac1, mac2)
}

func TestGenerateSessionID_Unique(t *testing.T) {
	id1 := generateSessionID()
	id2 := generateSessionID()
	assert.NotEqual(t, id1, id2, "session IDs should be unique")
	assert.NotEmpty(t, id1)
	// Should be valid base64
	_, err := base64.RawStdEncoding.DecodeString(id1)
	assert.NoError(t, err)
}

// =============================================================================
// crypto_olm.go: NewMegolmSession and Encrypt
// =============================================================================

func TestNewMegolmSession(t *testing.T) {
	session, err := NewMegolmSession("!room:test.local")
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, "!room:test.local", session.RoomID)
	assert.NotEmpty(t, session.SessionID)
	assert.Equal(t, uint32(0), session.MessageIndex)
	assert.NotNil(t, session.SessionKey)
}

func TestMegolmSession_ExportSessionKey(t *testing.T) {
	session, err := NewMegolmSession("!room:test.local")
	require.NoError(t, err)

	key := session.ExportSessionKey()
	assert.NotEmpty(t, key)
	// Should be valid base64
	decoded, err := base64.RawStdEncoding.DecodeString(key)
	require.NoError(t, err)
	assert.Len(t, decoded, 32)
}

func TestDeriveMegolmMessageKey_Deterministic(t *testing.T) {
	sessionKey := make([]byte, 32)
	copy(sessionKey, "session-key-material")

	key1 := deriveMegolmMessageKey(sessionKey, 0)
	key2 := deriveMegolmMessageKey(sessionKey, 0)
	assert.Len(t, key1, 32)
	assert.Equal(t, key1, key2, "deriveMegolmMessageKey should be deterministic")
}

func TestDeriveMegolmMessageKey_DifferentIndex(t *testing.T) {
	sessionKey := make([]byte, 32)
	copy(sessionKey, "session-key-material")

	key0 := deriveMegolmMessageKey(sessionKey, 0)
	key1 := deriveMegolmMessageKey(sessionKey, 1)
	assert.NotEqual(t, key0, key1, "different indices should produce different keys")
}

// =============================================================================
// crypto_olm.go: OlmAccountStore.SaveOlmAccount
// =============================================================================

func TestOlmAccountStore_SaveOlmAccount(t *testing.T) {
	store := &OlmAccountStore{}
	account := &OlmAccount{
		NextKeyID: 1,
	}
	// SaveOlmAccount marshals the account; DB write is intentionally a no-op.
	err := store.SaveOlmAccount("@alice:test.local", "DEVICE1", account)
	assert.NoError(t, err)
}

// =============================================================================
// e2ee_ssss.go: SSSS key derivation and secret sharing
// =============================================================================

func TestSSSSManager_GenerateRandomKey(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	key1, err := sm.GenerateRandomKey()
	require.NoError(t, err)
	assert.Len(t, key1, 32)

	key2, err := sm.GenerateRandomKey()
	require.NoError(t, err)
	assert.NotEqual(t, key1, key2, "random keys should differ")
}

func TestSSSSManager_DeriveKeys_Deterministic(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	key := make([]byte, 32)
	iv := make([]byte, 16)

	aesKey1, macKey1, err := sm.deriveKeys(key, iv)
	require.NoError(t, err)
	aesKey2, macKey2, err := sm.deriveKeys(key, iv)
	require.NoError(t, err)

	assert.Len(t, aesKey1, 32)
	assert.Len(t, macKey1, 32)
	assert.Equal(t, aesKey1, aesKey2)
	assert.Equal(t, macKey1, macKey2)
}

func TestSSSSManager_DecryptSecret_InvalidBase64(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	badSecret := &SSSSSecret{
		Ciphertext: "!!!invalid!!!",
		MAC:        "mac",
		IV:         "iv",
	}
	_, err := sm.DecryptSecret(badSecret, make([]byte, 32))
	assert.Error(t, err)
}

func TestSSSSManager_DecryptSecret_InvalidMACBase64(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	// Ciphertext and IV are valid base64, but MAC is not → decode error
	badSecret := &SSSSSecret{
		Ciphertext: "AAAA",
		IV:         "BBBB",
		MAC:        "!!!invalid-mac!!!",
	}
	_, err := sm.DecryptSecret(badSecret, make([]byte, 32))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode MAC")
}

func TestSSSSManager_SplitRecoverSecret(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	secret := []byte("shared-secret-data!!")

	shares, err := sm.SplitSecret(secret, 2, 3)
	require.NoError(t, err)
	assert.Len(t, shares, 3)
	for _, s := range shares {
		assert.Equal(t, 2, s.Threshold)
		assert.NotEmpty(t, s.Share)
		assert.Greater(t, s.Index, 0)
	}

	// Recover using all shares
	recovered, err := sm.RecoverSecret(shares)
	require.NoError(t, err)
	assert.NotEmpty(t, recovered)
	assert.Len(t, recovered, len(secret))
}

func TestSSSSManager_SplitSecret_ThresholdTooHigh(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	// threshold (3) > shares (2) → error
	_, err := sm.SplitSecret([]byte("secret"), 3, 2)
	assert.Error(t, err)
}

func TestSSSSManager_RecoverSecret_NoShares(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	_, err := sm.RecoverSecret(nil)
	assert.Error(t, err)
}

func TestSSSSManager_RecoverSecret_InsufficientShares(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	// Create a share with threshold 5 but only provide 1
	shares := []*SecretShare{
		{Index: 1, Share: "AAAA", Threshold: 5},
	}
	_, err := sm.RecoverSecret(shares)
	assert.Error(t, err)
}

func TestSSSSManager_RecoverSecret_InvalidBase64Share(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	shares := []*SecretShare{
		{Index: 1, Share: "!!!not-base64!!!", Threshold: 1},
	}
	_, err := sm.RecoverSecret(shares)
	assert.Error(t, err)
}

// =============================================================================
// e2ee_ssss.go: GenerateKeyFromPassphrase
// =============================================================================

func TestSSSSManager_GenerateKeyFromPassphrase_UnsupportedAlgorithm(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	_, err := sm.GenerateKeyFromPassphrase("password", &SSSSKeyInfo{
		Algorithm: "m.scrypt",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported algorithm")
}

func TestSSSSManager_GenerateKeyFromPassphrase_InvalidSalt(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	_, err := sm.GenerateKeyFromPassphrase("password", &SSSSKeyInfo{
		Algorithm: "m.pbkdf2",
		Salt:      "!!!invalid-base64!!!",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode salt")
}

func TestSSSSManager_GenerateKeyFromPassphrase_Success(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	salt := base64.RawStdEncoding.EncodeToString([]byte("salt-value-1234567"))
	key, err := sm.GenerateKeyFromPassphrase("mypassword", &SSSSKeyInfo{
		Algorithm:  "m.pbkdf2",
		Salt:       salt,
		Iterations: 100,
	})
	require.NoError(t, err)
	assert.Len(t, key, 32)
}

func TestSSSSManager_GenerateKeyFromPassphrase_MACValidation(t *testing.T) {
	sm := NewSSSSManager(minimalHS("test.local"))
	salt := base64.RawStdEncoding.EncodeToString([]byte("salt-value-1234567"))
	// Provide a MAC that won't match → should fail
	_, err := sm.GenerateKeyFromPassphrase("mypassword", &SSSSKeyInfo{
		Algorithm:  "m.pbkdf2",
		Salt:       salt,
		Iterations: 100,
		MAC:        "incorrectmac",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid passphrase")
}

// =============================================================================
// e2ee_ssss.go: helper functions computeSSSSMAC and computeHMAC
// =============================================================================

func TestComputeSSSSMAC_Deterministic(t *testing.T) {
	key := []byte("key")
	mac1 := computeSSSSMAC(key, "iv-string")
	mac2 := computeSSSSMAC(key, "iv-string")
	assert.Equal(t, mac1, mac2)
	assert.NotEmpty(t, mac1)
	// Should be valid base64
	_, err := base64.RawStdEncoding.DecodeString(mac1)
	assert.NoError(t, err)
}

func TestComputeHMAC_Deterministic(t *testing.T) {
	mac1 := computeHMAC([]byte("data"), []byte("key"))
	mac2 := computeHMAC([]byte("data"), []byte("key"))
	assert.Equal(t, mac1, mac2)
	assert.Len(t, mac1, 32) // SHA-256
}

// =============================================================================
// relations.go: extractEmojiFromReaction
// =============================================================================

func TestRelationsService_ExtractEmojiFromReaction(t *testing.T) {
	setupMinimalTestDB(t)
	rs := &RelationsService{HS: minimalHS("test.local")}

	// Valid reaction content with emoji
	content := `{"m.relates_to":{"rel_type":"m.annotation","event_id":"$evt:test","key":"👍"}}`
	emoji := rs.extractEmojiFromReaction(content)
	assert.Equal(t, "👍", emoji)
}

func TestRelationsService_ExtractEmojiFromReaction_NoKey(t *testing.T) {
	setupMinimalTestDB(t)
	rs := &RelationsService{HS: minimalHS("test.local")}

	content := `{"m.relates_to":{"rel_type":"m.annotation","event_id":"$evt:test"}}`
	emoji := rs.extractEmojiFromReaction(content)
	assert.Equal(t, "", emoji)
}

func TestRelationsService_ExtractEmojiFromReaction_NoRelatesTo(t *testing.T) {
	setupMinimalTestDB(t)
	rs := &RelationsService{HS: minimalHS("test.local")}

	content := `{"body":"hello"}`
	emoji := rs.extractEmojiFromReaction(content)
	assert.Equal(t, "", emoji)
}

func TestRelationsService_ExtractEmojiFromReaction_InvalidJSON(t *testing.T) {
	setupMinimalTestDB(t)
	rs := &RelationsService{HS: minimalHS("test.local")}

	emoji := rs.extractEmojiFromReaction(`{invalid json`)
	assert.Equal(t, "", emoji)
}

// =============================================================================
// room_upgrade.go: isSupportedRoomVersion
// =============================================================================

func TestIsSupportedRoomVersion_Supported(t *testing.T) {
	for _, v := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"} {
		assert.True(t, isSupportedRoomVersion(v), "version %s should be supported", v)
	}
}

func TestIsSupportedRoomVersion_Unsupported(t *testing.T) {
	for _, v := range []string{"0", "12", "99", "", "abc", "v10"} {
		assert.False(t, isSupportedRoomVersion(v), "version %q should not be supported", v)
	}
}

// =============================================================================
// CanonicalJSON edge cases (homesserver communication signature canonicalization)
// =============================================================================

func TestCanonicalJSON_IntegerFloats(t *testing.T) {
	// Integer-valued floats should be rendered without decimal point (Matrix spec)
	out, err := CanonicalJSON(float64(42))
	require.NoError(t, err)
	assert.Equal(t, "42", string(out))
}

func TestCanonicalJSON_NonIntegerFloat(t *testing.T) {
	out, err := CanonicalJSON(float64(3.14))
	require.NoError(t, err)
	// Non-integer floats use standard JSON marshaling
	assert.Contains(t, string(out), "3.14")
}

func TestCanonicalJSON_ObjectKeyOrdering(t *testing.T) {
	// Keys must be sorted lexicographically
	obj := map[string]any{
		"zebra":  "last",
		"apple":  "first",
		"monkey": "middle",
	}
	out, err := CanonicalJSON(obj)
	require.NoError(t, err)
	// apple should come before monkey, which comes before zebra
	appleIdx := bytes.Index(out, []byte("apple"))
	monkeyIdx := bytes.Index(out, []byte("monkey"))
	zebraIdx := bytes.Index(out, []byte("zebra"))
	assert.Less(t, appleIdx, monkeyIdx)
	assert.Less(t, monkeyIdx, zebraIdx)
}

func TestCanonicalJSON_StringValue(t *testing.T) {
	out, err := CanonicalJSON("hello")
	require.NoError(t, err)
	assert.Equal(t, `"hello"`, string(out))
}

func TestCanonicalJSON_NestedArray_Crypto(t *testing.T) {
	out, err := CanonicalJSON([]any{float64(1), float64(2), float64(3)})
	require.NoError(t, err)
	assert.Equal(t, "[1,2,3]", string(out))
}

// =============================================================================
// json_canonical.go: canonicalJSONObject escape handling
// =============================================================================

func TestCanonicalJSON_StringEscaping(t *testing.T) {
	// Strings with special characters must be properly escaped
	out, err := CanonicalJSON(map[string]any{
		"key": `a"b\c`,
	})
	require.NoError(t, err)
	assert.Contains(t, string(out), `\"`)
	assert.Contains(t, string(out), `\\`)
}
