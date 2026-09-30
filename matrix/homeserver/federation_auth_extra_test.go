package homeserver

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== verifySignature tests =====

func TestEventAuthVerifier_VerifySignature_Valid(t *testing.T) {
	setupMinimalTestDB(t)
	pub, priv := ed25519Key()
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	event := map[string]any{
		"event_id":         "$evt:test.local",
		"type":             "m.room.message",
		"room_id":          "!room:test.local",
		"sender":           "@alice:test.local",
		"content":          map[string]any{"body": "hi"},
		"origin_server_ts": float64(1234),
	}

	// Build a signed copy without signatures/unsigned
	canonical, err := CanonicalJSON(event)
	require.NoError(t, err)
	sig := ed25519.Sign(priv, canonical)
	sigB64 := base64.RawStdEncoding.EncodeToString(sig)

	err = v.verifySignature(event, "test.local", "ed25519:1", sigB64, pub)
	require.NoError(t, err)
}

func TestEventAuthVerifier_VerifySignature_WrongKey(t *testing.T) {
	setupMinimalTestDB(t)
	pub1, priv1 := ed25519Key()
	_, priv2 := ed25519Key()
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	event := map[string]any{"a": float64(1), "b": "two"}

	// Sign with priv2
	canonical, err := CanonicalJSON(event)
	require.NoError(t, err)
	sig := ed25519.Sign(priv2, canonical)
	sigB64 := base64.RawStdEncoding.EncodeToString(sig)

	// Verify with pub1 (different key) → error
	err = v.verifySignature(event, "test.local", "ed25519:1", sigB64, pub1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")

	// Verify with pub1 using a sig made by priv1 → ok
	sig2 := ed25519.Sign(priv1, canonical)
	sig2B64 := base64.RawStdEncoding.EncodeToString(sig2)
	err = v.verifySignature(event, "test.local", "ed25519:1", sig2B64, pub1)
	require.NoError(t, err)
}

func TestEventAuthVerifier_VerifySignature_InvalidBase64(t *testing.T) {
	setupMinimalTestDB(t)
	pub, _ := ed25519Key()
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	event := map[string]any{"a": float64(1)}
	err := v.verifySignature(event, "test.local", "ed25519:1", "!!!not-base64!!!", pub)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode signature")
}

// ===== verifyEventID tests =====

func TestEventAuthVerifier_VerifyEventID_NoDollarPrefix(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	event := map[string]any{
		"event_id":         "no-dollar:test.local",
		"sender":           "@alice:test.local",
		"room_id":          "!room:test.local",
		"origin_server_ts": float64(1234),
	}
	err := v.verifyEventID(event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must start with $")
}

func TestEventAuthVerifier_VerifyEventID_HashMismatch(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	event := map[string]any{
		"event_id":         "$wronghash",
		"sender":           "@alice:test.local",
		"room_id":          "!room:test.local",
		"origin_server_ts": float64(1234),
	}
	err := v.verifyEventID(event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hash mismatch")
}

func TestEventAuthVerifier_VerifyEventID_ValidHash(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	sender := "@alice:test.local"
	roomID := "!room:test.local"
	ts := int64(1234567890)

	hash := sha256.New()
	_, _ = hash.Write([]byte(sender + ":" + roomID + ":" + itoa(ts)))
	expectedHash := base64.RawURLEncoding.EncodeToString(hash.Sum(nil))

	event := map[string]any{
		"event_id":         "$" + expectedHash,
		"sender":           sender,
		"room_id":          roomID,
		"origin_server_ts": float64(ts),
	}
	err := v.verifyEventID(event)
	require.NoError(t, err)
}

// itoa converts int64 to decimal string without importing strconv (keeps the
// test self-contained and mirrors what fmt.Fprintf does inside verifyEventID).
func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// ===== verifyAuthEvents tests =====

func TestEventAuthVerifier_VerifyAuthEvents_CreateEventNoAuthEvents(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	// m.room.create with no auth_events → OK (allowed)
	event := map[string]any{
		"type":        "m.room.create",
		"sender":      "@creator:test.local",
		"room_id":     "!newroom:test.local",
		"event_id":    "$create:test.local",
		"auth_events": []any{},
	}
	err := v.verifyAuthEvents(event)
	require.NoError(t, err)
}

func TestEventAuthVerifier_VerifyAuthEvents_NonCreateMissingAuthEvents(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	// Non-create event with missing auth_events → error
	event := map[string]any{
		"type":     "m.room.message",
		"sender":   "@alice:test.local",
		"room_id":  "!room:test.local",
		"event_id": "$msg:test.local",
	}
	err := v.verifyAuthEvents(event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing auth_events")
}

func TestEventAuthVerifier_VerifyAuthEvents_AuthEventNotFound(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	event := map[string]any{
		"type":        "m.room.message",
		"sender":      "@alice:test.local",
		"room_id":     "!room:test.local",
		"event_id":    "$msg:test.local",
		"auth_events": []any{"$nonexistent:test.local"},
	}
	err := v.verifyAuthEvents(event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth event not found")
}

// ===== getServerPublicKey tests (DB-backed cache hit) =====

func TestEventAuthVerifier_GetServerPublicKey_DBCacheHit(t *testing.T) {
	setupMinimalTestDB(t)
	pub, _ := ed25519Key()
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	// Insert a valid (non-expired) server key into DB
	rec := MatrixServerKey{
		ServerName: "cached.test",
		KeyID:      "ed25519:1",
		PublicKey:  base64.RawStdEncoding.EncodeToString(pub),
		ValidUntil: time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	key, err := v.getServerPublicKey("cached.test")
	require.NoError(t, err)
	assert.Equal(t, ed25519.PublicKey(pub), ed25519.PublicKey(key))
}

func TestEventAuthVerifier_GetServerPublicKey_DBExpiredThenFetchFails(t *testing.T) {
	setupMinimalTestDB(t)
	pub, _ := ed25519Key()
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	// Insert an EXPIRED server key into DB (should be ignored by the
	// valid_until filter, forcing a remote fetch).
	rec := MatrixServerKey{
		ServerName: "expired.test",
		KeyID:      "ed25519:1",
		PublicKey:  base64.RawStdEncoding.EncodeToString(pub),
		ValidUntil: time.Now().Add(-1 * time.Hour), // expired
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	// The DB cache misses (expired), so getServerPublicKey attempts a remote
	// fetch which fails (non-resolvable host / invalid scheme) → error.
	_, err := v.getServerPublicKey("expired.test")
	require.Error(t, err)
}

func TestEventAuthVerifier_GetServerPublicKey_UncachedReturnsError(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	// No DB record and non-resolvable host → remote fetch fails → error.
	_, err := v.getServerPublicKey("never-cached.test")
	require.Error(t, err)
}

// ===== VerifyEventSignature with cached key =====

func TestEventAuthVerifier_VerifyEventSignature_WithCachedKey(t *testing.T) {
	setupMinimalTestDB(t)
	pub, priv := ed25519Key()
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	// Seed the DB cache with a valid key for "signer.test"
	rec := MatrixServerKey{
		ServerName: "signer.test",
		KeyID:      "ed25519:1",
		PublicKey:  base64.RawStdEncoding.EncodeToString(pub),
		ValidUntil: time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	// Build a signed event (without signatures/unsigned)
	event := map[string]any{
		"event_id":         "$sig:test.local",
		"type":             "m.room.message",
		"room_id":          "!room:test.local",
		"sender":           "@alice:signer.test",
		"content":          map[string]any{"body": "hi"},
		"origin_server_ts": float64(1234),
	}
	canonical, err := CanonicalJSON(event)
	require.NoError(t, err)
	sig := ed25519.Sign(priv, canonical)
	sigB64 := base64.RawStdEncoding.EncodeToString(sig)

	event["signatures"] = map[string]any{
		"signer.test": map[string]any{"ed25519:1": sigB64},
	}

	err = v.VerifyEventSignature(event, []string{"signer.test"})
	require.NoError(t, err)
}

func TestEventAuthVerifier_VerifyEventSignature_BadSignature(t *testing.T) {
	setupMinimalTestDB(t)
	pub, _ := ed25519Key()
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	rec := MatrixServerKey{
		ServerName: "bad signer.test",
		KeyID:      "ed25519:1",
		PublicKey:  base64.RawStdEncoding.EncodeToString(pub),
		ValidUntil: time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	event := map[string]any{
		"event_id":         "$bad:test.local",
		"type":             "m.room.message",
		"room_id":          "!room:test.local",
		"sender":           "@alice:test.local",
		"content":          map[string]any{},
		"origin_server_ts": float64(1),
		"signatures": map[string]any{
			"bad signer.test": map[string]any{"ed25519:1": "bogus-signature"},
		},
	}
	err := v.VerifyEventSignature(event, []string{"bad signer.test"})
	require.Error(t, err)
}

func TestEventAuthVerifier_VerifyEventSignature_ServerSigMapNotMap(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	// signatures[server] is not a map → nothing was verified
	event := map[string]any{
		"signatures": map[string]any{
			"test.local": "not-a-map",
		},
	}
	err := v.VerifyEventSignature(event, []string{"test.local"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid signature")
}

func TestEventAuthVerifier_VerifyEventSignature_SigValueNotString(t *testing.T) {
	setupMinimalTestDB(t)
	pub, _ := ed25519Key()
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	rec := MatrixServerKey{
		ServerName: "nonstr.test",
		KeyID:      "ed25519:1",
		PublicKey:  base64.RawStdEncoding.EncodeToString(pub),
		ValidUntil: time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	// sig value is an int, not a string → nothing was verified
	event := map[string]any{
		"signatures": map[string]any{
			"nonstr.test": map[string]any{"ed25519:1": float64(123)},
		},
	}
	err := v.VerifyEventSignature(event, []string{"nonstr.test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid signature")
}

// ===== VerifyFederationEvent tests =====

func TestEventAuthVerifier_VerifyFederationEvent_InvalidStructure(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	// Missing required fields → structure error
	event := map[string]any{
		"event_id": "$x:test.local",
	}
	err := v.VerifyFederationEvent(event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid event structure")
}

func TestEventAuthVerifier_VerifyFederationEvent_MissingSignatures(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	v := NewEventAuthVerifier(hs)

	// Valid structure but missing signatures → signature verification fails
	event := map[string]any{
		"event_id":         "$evt:test.local",
		"type":             "m.room.message",
		"room_id":          "!room:test.local",
		"sender":           "@alice:test.local",
		"content":          map[string]any{},
		"origin_server_ts": float64(1),
	}
	err := v.VerifyFederationEvent(event)
	require.Error(t, err)
	// Either signature or event-ID verification may fail first.
	assert.True(t,
		errors.Is(err, err) && (containsStr(err.Error(), "signature") || containsStr(err.Error(), "event ID")),
		"expected signature or event ID error, got: %v", err)
}

// containsStr is a small helper to avoid pulling strings just for one call.
func containsStr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ===== fetchServerKeys tests =====

func TestFetchServerKeys_Success(t *testing.T) {
	pub, _ := ed25519Key()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"server_name":"remote.test","valid_until_ts":` +
			itoa(time.Now().Add(24*time.Hour).UnixMilli()) +
			`,"verify_keys":{"ed25519:1":{"key":"` + base64.RawStdEncoding.EncodeToString(pub) + `"}},"old_verify_keys":{}}`))
	}))
	defer server.Close()

	c, _, _, _ := newTestFederationClient()
	resp, err := fetchServerKeys(c, server.URL, "remote.test")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "remote.test", resp.ServerName)
	require.Contains(t, resp.VerifyKeys, "ed25519:1")
	assert.Equal(t, base64.RawStdEncoding.EncodeToString(pub), resp.VerifyKeys["ed25519:1"].Key)
}

func TestFetchServerKeys_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c, _, _, _ := newTestFederationClient()
	resp, err := fetchServerKeys(c, server.URL, "remote.test")
	require.Error(t, err)
	assert.Nil(t, resp)
	// httpclient.Do() returns an error for non-2xx, so fetchServerKeys returns
	// that error directly. The message mentions the status code.
	assert.Contains(t, err.Error(), "500")
}

func TestFetchServerKeys_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	c, _, _, _ := newTestFederationClient()
	resp, err := fetchServerKeys(c, server.URL, "remote.test")
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to decode response")
}

func TestFetchServerKeys_ConnectionFailure(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	// Close the server immediately to force a connection error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := server.URL
	server.Close()

	resp, err := fetchServerKeys(c, addr, "remote.test")
	require.Error(t, err)
	assert.Nil(t, resp)
}

// ===== FederationAuthMiddleware: valid signature path =====

func TestFederationAuthMiddleware_ValidSignature_PassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)

	pub, priv := ed25519Key()
	hs := minimalHS("test.local")
	// Seed DB cache so getServerPublicKey returns the key without fetching.
	rec := MatrixServerKey{
		ServerName: "origin.hs",
		KeyID:      "ed25519:1",
		PublicKey:  base64.RawStdEncoding.EncodeToString(pub),
		ValidUntil: time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	restore := resetGlobalHS(hs)
	defer restore()

	// Build a valid X-Matrix auth header by signing the request payload.
	body := `{"hello":"world"}`
	target := "/_matrix/federation/v1/send/123"

	payload := map[string]any{
		"method":      http.MethodPut,
		"uri":         target,
		"origin":      "origin.hs",
		"destination": "test.local",
		"content":     map[string]any{"hello": "world"},
	}
	canonical, err := CanonicalJSON(payload)
	require.NoError(t, err)
	sig := ed25519.Sign(priv, canonical)
	sigB64 := base64.RawStdEncoding.EncodeToString(sig)

	authHeader := `X-Matrix origin="origin.hs",destination="test.local",key="ed25519:1",sig="` + sigB64 + `"`

	called := false
	r := gin.New()
	r.Use(FederationAuthMiddleware())
	r.PUT(target, func(c *gin.Context) {
		called = true
		origin, exists := c.Get("origin")
		assert.True(t, exists)
		assert.Equal(t, "origin.hs", origin)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
	req.Header.Set("Authorization", authHeader)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.True(t, called, "next handler should be called with valid signature")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestFederationAuthMiddleware_InvalidSignature_Rejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)

	pub, _ := ed25519Key()
	hs := minimalHS("test.local")
	rec := MatrixServerKey{
		ServerName: "origin.hs",
		KeyID:      "ed25519:1",
		PublicKey:  base64.RawStdEncoding.EncodeToString(pub),
		ValidUntil: time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, db.GetDB().Create(&rec).Error)

	restore := resetGlobalHS(hs)
	defer restore()

	// Use a garbage signature.
	authHeader := `X-Matrix origin="origin.hs",destination="test.local",key="ed25519:1",sig="garbage!!!"`
	r := gin.New()
	r.Use(FederationAuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		t.Error("next handler should not be called with invalid signature")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", authHeader)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestFederationAuthMiddleware_NoMatchingKey_Rejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()

	// No DB record for "unknown.hs" → getServerPublicKey attempts a remote
	// fetch which fails → middleware returns 401.
	authHeader := `X-Matrix origin="unknown.hs",destination="test.local",key="ed25519:1",sig="abc"`
	r := gin.New()
	r.Use(FederationAuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		t.Error("next handler should not be called")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", authHeader)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Failed to get server key")
}
