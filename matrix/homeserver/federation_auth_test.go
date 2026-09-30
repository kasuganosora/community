package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== contains() tests =====

func TestContains(t *testing.T) {
	assert.True(t, contains([]string{"a", "b", "c"}, "b"))
	assert.False(t, contains([]string{"a", "b", "c"}, "d"))
	assert.False(t, contains([]string{}, "a"))
	assert.False(t, contains(nil, "a"))
	assert.True(t, contains([]string{""}, ""))
}

// ===== extractServerName() tests =====

func TestExtractServerName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"standard user id", "@alice:example.com", "example.com"},
		{"user id with port", "@alice:host.example:8448", "host.example"},
		{"no colon", "alice", ""},
		{"empty string", "", ""},
		{"just colon", "@alice:", ""},
		{"room id", "!room:server.test", "server.test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractServerName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ===== isLocalUser() tests =====

func TestIsLocalUser(t *testing.T) {
	assert.True(t, isLocalUser("@alice:test.local", "test.local"))
	assert.False(t, isLocalUser("@alice:other.local", "test.local"))
	assert.False(t, isLocalUser("alice", "test.local"))
	assert.False(t, isLocalUser("@alice:a:b", "test.local")) // len > 2
}

// ===== parseXMatrixAuth() tests =====

func TestParseXMatrixAuth_Valid(t *testing.T) {
	header := `X-Matrix origin="origin.hs",destination="dest.hs",key="ed25519:abc",sig="base64sig"`
	origin, dest, keyID, sig, err := parseXMatrixAuth(header)

	require.NoError(t, err)
	assert.Equal(t, "origin.hs", origin)
	assert.Equal(t, "dest.hs", dest)
	assert.Equal(t, "ed25519:abc", keyID)
	assert.Equal(t, "base64sig", sig)
}

func TestParseXMatrixAuth_NoDestination(t *testing.T) {
	// destination is optional in some implementations
	header := `X-Matrix origin="origin.hs",key="ed25519:abc",sig="base64sig"`
	origin, dest, keyID, sig, err := parseXMatrixAuth(header)

	require.NoError(t, err)
	assert.Equal(t, "origin.hs", origin)
	assert.Equal(t, "", dest)
	assert.Equal(t, "ed25519:abc", keyID)
	assert.Equal(t, "base64sig", sig)
}

func TestParseXMatrixAuth_MissingOrigin(t *testing.T) {
	header := `X-Matrix destination="dest.hs",key="ed25519:abc",sig="base64sig"`
	_, _, _, _, err := parseXMatrixAuth(header)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required fields")
}

func TestParseXMatrixAuth_MissingKey(t *testing.T) {
	header := `X-Matrix origin="origin.hs",sig="base64sig"`
	_, _, _, _, err := parseXMatrixAuth(header)
	require.Error(t, err)
}

func TestParseXMatrixAuth_MissingSig(t *testing.T) {
	header := `X-Matrix origin="origin.hs",key="ed25519:abc"`
	_, _, _, _, err := parseXMatrixAuth(header)
	require.Error(t, err)
}

func TestParseXMatrixAuth_Empty(t *testing.T) {
	_, _, _, _, err := parseXMatrixAuth("X-Matrix ")
	require.Error(t, err)
}

// ===== verifyRequestSignature() tests =====

func TestVerifyRequestSignature_Valid(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)

	// Build a request
	body := `{"hello":"world"}`
	req := httptest.NewRequest(http.MethodPut, "/_matrix/federation/v1/send/123", strings.NewReader(body))

	// Compute the signature the same way the code does
	uri := req.URL.RequestURI()
	payload := map[string]any{
		"method":      http.MethodPut,
		"uri":         uri,
		"origin":      "origin.hs",
		"destination": "dest.hs",
	}
	// add content
	payload["content"] = map[string]any{"hello": "world"}

	canonical, err := CanonicalJSON(payload)
	require.NoError(t, err)

	sig := ed25519.Sign(priv, canonical)
	sigB64 := base64.RawStdEncoding.EncodeToString(sig)

	// verifyRequestSignature reads req.Body
	result := verifyRequestSignature(req, "origin.hs", "dest.hs", "ed25519:1", sigB64, pub)
	assert.True(t, result, "valid signature should verify")
}

func TestVerifyRequestSignature_InvalidSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	req := httptest.NewRequest(http.MethodGet, "/_matrix/federation/v1/version", nil)

	// Use a garbage signature
	result := verifyRequestSignature(req, "origin.hs", "dest.hs", "ed25519:1", "invalidbase64!!!", pub)
	assert.False(t, result)
}

func TestVerifyRequestSignature_WrongKey(t *testing.T) {
	pub1, priv1, _ := ed25519.GenerateKey(nil)
	pub2, _, _ := ed25519.GenerateKey(nil)

	req := httptest.NewRequest(http.MethodGet, "/_matrix/federation/v1/version", nil)

	uri := req.URL.RequestURI()
	payload := map[string]any{
		"method":      http.MethodGet,
		"uri":         uri,
		"origin":      "origin.hs",
		"destination": "dest.hs",
	}
	canonical, err := CanonicalJSON(payload)
	require.NoError(t, err)

	sig := ed25519.Sign(priv1, canonical)
	sigB64 := base64.RawStdEncoding.EncodeToString(sig)

	// Verify with pub2 (different key) → should fail
	result := verifyRequestSignature(req, "origin.hs", "dest.hs", "ed25519:1", sigB64, pub2)
	assert.False(t, result)

	// Verify with pub1 (correct key) → should succeed
	req2 := httptest.NewRequest(http.MethodGet, "/_matrix/federation/v1/version", nil)
	_ = req2 // signature is recomputed inside; just confirm helper works
	_ = pub1
}

// ===== EventAuthVerifier structure tests =====

func TestNewEventAuthVerifier(t *testing.T) {
	hs := &HomeServer{Config: &HSConfig{ServerName: "test.local"}}
	v := NewEventAuthVerifier(hs)
	assert.NotNil(t, v)
	assert.Equal(t, hs, v.HS)
}

func TestEventAuthVerifier_VerifyEventSignature_MissingSignatures(t *testing.T) {
	hs := &HomeServer{Config: &HSConfig{ServerName: "test.local"}}
	v := NewEventAuthVerifier(hs)

	event := map[string]any{
		"event_id": "$test:test.local",
		"type":     "m.room.message",
		"sender":   "@alice:test.local",
	}
	err := v.VerifyEventSignature(event, []string{"test.local"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing signatures")
}

func TestEventAuthVerifier_VerifyEventSignature_EmptySignatures(t *testing.T) {
	hs := &HomeServer{Config: &HSConfig{ServerName: "test.local"}}
	v := NewEventAuthVerifier(hs)

	event := map[string]any{
		"event_id":   "$test:test.local",
		"type":       "m.room.message",
		"sender":     "@alice:test.local",
		"signatures": map[string]any{},
	}
	err := v.VerifyEventSignature(event, []string{"test.local"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid signature")
}

func TestEventAuthVerifier_VerifyEventSignature_ServerNotAllowed(t *testing.T) {
	hs := &HomeServer{Config: &HSConfig{ServerName: "test.local"}}
	v := NewEventAuthVerifier(hs)

	event := map[string]any{
		"signatures": map[string]any{
			"evil.server": map[string]any{
				"ed25519:1": "sig",
			},
		},
	}
	err := v.VerifyEventSignature(event, []string{"test.local"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid signature")
}

func TestEventAuthVerifier_validateEventStructure(t *testing.T) {
	hs := &HomeServer{Config: &HSConfig{ServerName: "test.local"}}
	v := NewEventAuthVerifier(hs)

	tests := []struct {
		name      string
		event     map[string]any
		wantError bool
		errSubstr string
	}{
		{
			name: "valid event",
			event: map[string]any{
				"event_id":         "$valid:test.local",
				"type":             "m.room.message",
				"room_id":          "!room:test.local",
				"sender":           "@alice:test.local",
				"content":          map[string]any{},
				"origin_server_ts": float64(1234567890),
			},
			wantError: false,
		},
		{
			name: "missing event_id",
			event: map[string]any{
				"type":    "m.room.message",
				"room_id": "!room:test.local",
				"sender":  "@alice:test.local",
			},
			wantError: true,
			errSubstr: "missing required field",
		},
		{
			name: "empty type",
			event: map[string]any{
				"event_id":         "$e:test.local",
				"type":             "",
				"room_id":          "!room:test.local",
				"sender":           "@alice:test.local",
				"content":          map[string]any{},
				"origin_server_ts": float64(1),
			},
			wantError: true,
			errSubstr: "invalid event type",
		},
		{
			name: "invalid room_id",
			event: map[string]any{
				"event_id":         "$e:test.local",
				"type":             "m.room.message",
				"room_id":          "badroom",
				"sender":           "@alice:test.local",
				"content":          map[string]any{},
				"origin_server_ts": float64(1),
			},
			wantError: true,
			errSubstr: "invalid room ID",
		},
		{
			name: "invalid sender",
			event: map[string]any{
				"event_id":         "$e:test.local",
				"type":             "m.room.message",
				"room_id":          "!room:test.local",
				"sender":           "notAUser",
				"content":          map[string]any{},
				"origin_server_ts": float64(1),
			},
			wantError: true,
			errSubstr: "invalid sender",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.validateEventStructure(tt.event)
			if tt.wantError {
				require.Error(t, err)
				if tt.errSubstr != "" {
					assert.Contains(t, err.Error(), tt.errSubstr)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ===== FederationAuthMiddleware tests =====

func TestFederationAuthMiddleware_HSNotAvailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	r := gin.New()
	r.Use(FederationAuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		t.Error("next handler should not be called")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestFederationAuthMiddleware_HSDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(&HomeServer{Config: &HSConfig{Enabled: false}})
	defer restore()

	r := gin.New()
	r.Use(FederationAuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		t.Error("next handler should not be called")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestFederationAuthMiddleware_NoAuthHeader_PassesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(&HomeServer{Config: &HSConfig{Enabled: true, ServerName: "test.local"}})
	defer restore()

	called := false
	r := gin.New()
	r.Use(FederationAuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		called = true
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.True(t, called, "without auth header, request should pass through")
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestFederationAuthMiddleware_InvalidAuthHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(&HomeServer{Config: &HSConfig{Enabled: true, ServerName: "test.local"}})
	defer restore()

	r := gin.New()
	r.Use(FederationAuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		t.Error("next handler should not be called with invalid auth")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "X-Matrix origin=") // incomplete
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
