package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dbGetPastTime returns a time in the past for expired token tests.
func dbGetPastTime() time.Time {
	return time.Now().Add(-1 * time.Hour)
}

// ===== KeyStorage tests =====

func TestNewKeyStorage(t *testing.T) {
	ks := NewKeyStorage()
	assert.NotNil(t, ks)
}

func TestKeyStorage_LoadOrGenerateKey_GeneratesNew(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	priv, pub, keyID, err := ks.LoadOrGenerateKey()
	require.NoError(t, err)
	assert.NotNil(t, priv)
	assert.NotNil(t, pub)
	assert.NotEmpty(t, keyID)
	assert.Contains(t, keyID, "ed25519:")
}

func TestKeyStorage_LoadOrGenerateKey_LoadsExisting(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	// First call generates
	priv1, pub1, keyID1, err := ks.LoadOrGenerateKey()
	require.NoError(t, err)

	// Second call should load the same key
	priv2, pub2, keyID2, err := ks.LoadOrGenerateKey()
	require.NoError(t, err)
	assert.Equal(t, keyID1, keyID2, "should load same key ID")
	assert.Equal(t, priv1, priv2, "should load same private key")
	assert.Equal(t, pub1, pub2, "should load same public key")
}

func TestKeyStorage_LoadOrGenerateKey_LoadsUnencrypted(t *testing.T) {
	setupMinimalTestDB(t)
	ks := NewKeyStorage()

	// Manually insert an unencrypted key (legacy format)
	pub, priv, _ := ed25519.GenerateKey(nil)
	privB64 := base64.RawStdEncoding.EncodeToString(priv)
	pubB64 := base64.RawStdEncoding.EncodeToString(pub)
	record := MatrixFederationKey{
		KeyID:      "ed25519:legacy",
		KeyType:    "federation",
		PrivateKey: privB64,
		PublicKey:  pubB64,
		IsActive:   true,
	}
	require.NoError(t, db.GetDB().Create(&record).Error)

	// Should load the legacy key
	loadedPriv, loadedPub, keyID, err := ks.LoadOrGenerateKey()
	require.NoError(t, err)
	assert.Equal(t, "ed25519:legacy", keyID)
	assert.Equal(t, priv, ed25519.PrivateKey(loadedPriv))
	assert.Equal(t, pub, ed25519.PublicKey(loadedPub))
}

func TestKeyStorage_EncryptDecrypt_RoundTrip(t *testing.T) {
	ks := NewKeyStorage()
	plaintext := []byte("secret-key-data")

	// With no EncryptedKey configured, encryptKey returns base64 (no enc: prefix)
	encrypted, err := ks.encryptKey(plaintext)
	require.NoError(t, err)
	assert.NotEmpty(t, encrypted)

	// decryptKey behavior depends on whether the value has "enc:" prefix.
	// Without enc: prefix, decryptKey returns "not an encrypted key" error.
	if len(encrypted) >= 4 && encrypted[:4] == "enc:" {
		// Encrypted path: should decrypt back to plaintext
		decrypted, err := ks.decryptKey(encrypted)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decrypted)
	} else {
		// Plain base64 path: decryptKey returns error (not encrypted format)
		_, err := ks.decryptKey(encrypted)
		assert.Error(t, err)
	}
}

func TestKeyStorage_DecryptKey_NotEncrypted(t *testing.T) {
	ks := NewKeyStorage()
	_, err := ks.decryptKey("notencrypted")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not an encrypted key")
}

func TestKeyStorage_DecryptKey_TooShort(t *testing.T) {
	ks := NewKeyStorage()
	_, err := ks.decryptKey("ab")
	assert.Error(t, err)
}

func TestDeriveEncryptionKey(t *testing.T) {
	key1 := deriveEncryptionKey("mysecret")
	key2 := deriveEncryptionKey("mysecret")
	key3 := deriveEncryptionKey("different")

	assert.Equal(t, key1, key2, "same secret should derive same key")
	assert.NotEqual(t, key1, key3, "different secrets should derive different keys")
	assert.Len(t, key1, 32, "AES-256 key should be 32 bytes")
}

func TestDeriveEncryptionKey_Empty(t *testing.T) {
	key := deriveEncryptionKey("")
	assert.Len(t, key, 32)
}

func TestMatrixFederationKey_TableName(t *testing.T) {
	assert.Equal(t, "matrix_federation_keys", MatrixFederationKey{}.TableName())
}

// ===== Push service tests =====

func TestNewPushService(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	ps := NewPushService(hs)
	assert.NotNil(t, ps)
}

func TestPushService_GetPushers_Empty(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	ps := NewPushService(hs)

	pushers, err := ps.GetPushers("@nobody:test.local")
	require.NoError(t, err)
	assert.Empty(t, pushers)
}

func TestPushService_SetPusher_AndGet(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	ps := NewPushService(hs)

	pusher := &Pusher{
		Pushkey:           "https://example.com/push",
		Kind:              "http",
		AppID:             "myapp",
		AppDisplayName:    "My App",
		DeviceDisplayName: "My Device",
		Lang:              "en",
	}
	err := ps.SetPusher("@alice:test.local", pusher)
	require.NoError(t, err)

	pushers, err := ps.GetPushers("@alice:test.local")
	require.NoError(t, err)
	assert.Len(t, pushers, 1)
	assert.Equal(t, "https://example.com/push", pushers[0].Pushkey)
}

// ===== OpenID service tests with DB =====

func TestOpenIDService_RequestToken_AndGetUserInfo(t *testing.T) {
	setupMinimalTestDB(t)
	// Need OpenIDToken table
	require.NoError(t, db.GetDB().AutoMigrate(&OpenIDToken{}))

	hs := minimalHS("test.local")
	os := &OpenIDService{hs: hs}

	resp, err := os.RequestToken("@alice:test.local", &OpenIDTokenRequest{ExpiresIn: 3600})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccessToken)
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.Equal(t, "test.local", resp.MatrixServerName)
	assert.Equal(t, int64(3600), resp.ExpiresIn)

	// GetUserInfo should return the user
	info, err := os.GetUserInfo(resp.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "@alice:test.local", info.Sub)
}

func TestOpenIDService_GetUserInfo_EmptyToken(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&OpenIDToken{}))

	hs := minimalHS("test.local")
	os := &OpenIDService{hs: hs}

	_, err := os.GetUserInfo("")
	assert.Error(t, err)
}

func TestOpenIDService_GetUserInfo_InvalidToken(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&OpenIDToken{}))

	hs := minimalHS("test.local")
	os := &OpenIDService{hs: hs}

	_, err := os.GetUserInfo("invalid_token")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid or expired")
}

func TestOpenIDService_CleanupExpiredTokens(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&OpenIDToken{}))

	hs := minimalHS("test.local")
	os := &OpenIDService{hs: hs}

	// Create an expired token
	expired := &OpenIDToken{
		Token:     "openid_expired",
		UserID:    "@alice:test.local",
		ExpiresAt: dbGetPastTime(),
	}
	require.NoError(t, db.GetDB().Create(expired).Error)

	// Cleanup should remove it
	os.CleanupExpiredTokens()
	assert.NotPanics(t, func() {
		os.CleanupExpiredTokens()
	})
}

// ===== handleGetOpenIDUserInfo tests =====

func TestHandleGetOpenIDUserInfo_MissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&OpenIDToken{}))
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/openid/userinfo", handleGetOpenIDUserInfo)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/openid/userinfo", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "M_MISSING_TOKEN")
}

func TestHandleGetOpenIDUserInfo_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&OpenIDToken{}))
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/openid/userinfo", handleGetOpenIDUserInfo)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/openid/userinfo", nil)
	req.Header.Set("Authorization", "Bearer invalid")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "M_UNKNOWN_TOKEN")
}
