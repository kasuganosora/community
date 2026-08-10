package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== FederationClient.federationBaseURL tests =====

func TestFederationBaseURL_WithPort(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{Config: &HSConfig{ServerName: "origin.local"}, keyID: "ed25519:1", privateKey: priv}
	c := NewFederationClient(hs)
	// serverName already has port
	assert.Equal(t, "https://host.example:9000", c.federationBaseURL("host.example:9000"))
}

func TestFederationBaseURL_DefaultPort(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	c := NewFederationClient(hs)
	assert.Equal(t, "https://example.com:8448", c.federationBaseURL("example.com"))
}

func TestFederationBaseURL_IPv6(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	c := NewFederationClient(hs)
	// "[::1]" contains ":" so the function treats it as already having a port
	result := c.federationBaseURL("[::1]")
	assert.Equal(t, "https://[::1]", result)
}

func TestFederationBaseURL_IPv6_NoColon(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "origin.local"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	c := NewFederationClient(hs)
	// A hostname without ":" gets the default port 8448
	result := c.federationBaseURL("example.org")
	assert.Equal(t, "https://example.org:8448", result)
}

// ===== FederationService.GetServerKey tests =====

func TestFederationService_GetServerKey(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "myserver.test"},
		keyID:      "ed25519:0",
		privateKey: priv,
		publicKey:  pub,
	}
	fed := NewFederationService(hs)

	result := fed.GetServerKey()
	require.NotNil(t, result)

	assert.Equal(t, "myserver.test", result["server_name"])

	validUntil, ok := result["valid_until_ts"].(int64)
	assert.True(t, ok)
	assert.True(t, validUntil > time.Now().UnixMilli())

	verifyKeys, ok := result["verify_keys"].(map[string]any)
	require.True(t, ok)
	keyInfo, ok := verifyKeys["ed25519:0"].(map[string]any)
	require.True(t, ok)
	expectedKey := base64.RawStdEncoding.EncodeToString(pub)
	assert.Equal(t, expectedKey, keyInfo["key"])

	oldKeys, ok := result["old_verify_keys"].(map[string]any)
	assert.True(t, ok)
	assert.Empty(t, oldKeys)

	sigs, ok := result["signatures"].(map[string]any)
	require.True(t, ok)
	serverSig, ok := sigs["myserver.test"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, serverSig, "ed25519:0")
}

func TestFederationService_GetServerKey_SignatureVerifiable(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "verify.test"},
		keyID:      "ed25519:1",
		privateKey: priv,
		publicKey:  pub,
	}
	fed := NewFederationService(hs)

	result := fed.GetServerKey()
	sigs := result["signatures"].(map[string]any)
	serverSig := sigs["verify.test"].(map[string]any)
	sigStr := serverSig["ed25519:1"].(string)

	// Verify the signature
	sigBytes, err := base64.RawStdEncoding.DecodeString(sigStr)
	require.NoError(t, err)

	// Reconstruct canonical JSON of the signed payload (without signatures)
	delete(result, "signatures")
	canonical, err := CanonicalJSON(result)
	require.NoError(t, err)

	assert.True(t, ed25519.Verify(pub, canonical, sigBytes), "server key signature should verify")
}

// ===== FederationService.signJSON tests =====

func TestFederationService_SignJSON_MapInput(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "s.test"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	fed := NewFederationService(hs)

	data := map[string]any{"a": float64(1), "b": "two"}
	sig := fed.signJSON(data)
	assert.NotEmpty(t, sig)
}

func TestFederationService_SignJSON_StripsSignatures(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "s.test"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	fed := NewFederationService(hs)

	// signJSON must ignore "signatures" and "unsigned" when signing
	data := map[string]any{
		"a":          float64(1),
		"signatures": map[string]any{"x": "y"},
		"unsigned":   map[string]any{"z": "w"},
	}
	sig := fed.signJSON(data)
	sigBytes, _ := base64.RawStdEncoding.DecodeString(sig)

	// Canonical without signatures/unsigned
	clean := map[string]any{"a": float64(1)}
	canonical, err := CanonicalJSON(clean)
	require.NoError(t, err)

	assert.True(t, ed25519.Verify(pub, canonical, sigBytes), "signature should match canonical without signatures/unsigned")
}

func TestFederationService_SignJSON_StructInput(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "s.test"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	fed := NewFederationService(hs)

	type myData struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	data := myData{A: 1, B: "two"}
	sig := fed.signJSON(data)
	assert.NotEmpty(t, sig)
}

func TestFederationService_signEvent(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "s.test"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	fed := NewFederationService(hs)

	event := map[string]any{
		"event_id": "$test:s.test",
		"type":     "m.room.message",
	}
	signed := fed.signEvent(event)

	sigs, ok := signed["signatures"].(map[string]any)
	require.True(t, ok)
	serverSig, ok := sigs["s.test"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, serverSig, "ed25519:1")
}

func TestFederationService_signEvent_PreservesExistingSignatures(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "s.test"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	fed := NewFederationService(hs)

	event := map[string]any{
		"event_id": "$test:s.test",
		"signatures": map[string]any{
			"other.server": map[string]any{"ed25519:2": "existingsig"},
		},
	}
	signed := fed.signEvent(event)

	sigs := signed["signatures"].(map[string]any)
	assert.Contains(t, sigs, "other.server", "existing signature should be preserved")
	assert.Contains(t, sigs, "s.test", "new signature should be added")
}

// ===== computeReferenceHash tests =====

func TestComputeReferenceHash_MessageEvent(t *testing.T) {
	event := map[string]any{
		"type":             "m.room.message",
		"room_id":          "!room:test.local",
		"sender":           "@alice:test.local",
		"content":          map[string]any{"body": "hello", "msgtype": "m.text"},
		"origin_server_ts": float64(1234567890),
		// extra fields that should be stripped during redaction
		"foo":            "bar",
		"_hidden":        "secret",
		"extra_unsigned": "x",
	}
	hash := computeReferenceHash(event, "m.room.message")
	assert.True(t, len(hash) > 1, "hash should not be empty")
	assert.True(t, hash[0] == '$', "event ID should start with $")
}

func TestComputeReferenceHash_MemberEvent(t *testing.T) {
	event := map[string]any{
		"type":      "m.room.member",
		"room_id":   "!room:test.local",
		"sender":    "@alice:test.local",
		"state_key": "@alice:test.local",
		"content": map[string]any{
			"membership":  "join",
			"displayname": "Alice",
			"avatar_url":  "mxc://test/avatar",
			"reason":      "should be stripped",
		},
		"origin_server_ts": float64(1234567890),
	}
	hash := computeReferenceHash(event, "m.room.member")
	assert.True(t, hash[0] == '$')
	// Hash should be deterministic
	hash2 := computeReferenceHash(event, "m.room.member")
	assert.Equal(t, hash, hash2, "hash should be deterministic for same input")
}

func TestComputeReferenceHash_CreateEvent(t *testing.T) {
	event := map[string]any{
		"type":      "m.room.create",
		"room_id":   "!newroom:test.local",
		"sender":    "@creator:test.local",
		"state_key": "",
		"content": map[string]any{
			"creator":      "@creator:test.local",
			"room_version": "10",
			"extra_field":  "stripped",
		},
	}
	hash := computeReferenceHash(event, "m.room.create")
	assert.True(t, hash[0] == '$')
}

func TestComputeReferenceHash_StripsUnderscorePrefix(t *testing.T) {
	event := map[string]any{
		"type":      "m.room.message",
		"room_id":   "!room:test.local",
		"sender":    "@alice:test.local",
		"content":   map[string]any{"body": "x"},
		"_internal": "should_be_stripped",
	}
	hash := computeReferenceHash(event, "m.room.message")
	assert.True(t, hash[0] == '$')
}

// ===== parseFederationPDU tests =====

func TestParseFederationPDU_Complete(t *testing.T) {
	event := map[string]any{
		"event_id":         "$exists:test.local",
		"room_id":          "!room:test.local",
		"type":             "m.room.message",
		"sender":           "@alice:test.local",
		"state_key":        "",
		"content":          map[string]any{"body": "hi"},
		"origin_server_ts": float64(1000),
	}

	eventID, roomID, eventType, sender, stateKey, hasStateKey, content, ts :=
		parseFederationPDU(event)

	assert.Equal(t, "$exists:test.local", eventID)
	assert.Equal(t, "!room:test.local", roomID)
	assert.Equal(t, "m.room.message", eventType)
	assert.Equal(t, "@alice:test.local", sender)
	assert.Equal(t, "", stateKey)
	assert.True(t, hasStateKey)
	assert.Equal(t, "hi", content["body"])
	assert.Equal(t, float64(1000), ts)
}

func TestParseFederationPDU_NoEventIDComputesHash(t *testing.T) {
	event := map[string]any{
		"room_id":          "!room:test.local",
		"type":             "m.room.message",
		"sender":           "@alice:test.local",
		"content":          map[string]any{"body": "hi"},
		"origin_server_ts": float64(1000),
	}
	eventID, _, _, _, _, _, _, _ := parseFederationPDU(event)
	assert.True(t, eventID[0] == '$', "computed event ID should start with $")
	assert.Equal(t, eventID, event["event_id"], "event_id should be set in map")
}

func TestParseFederationPDU_NoStateKey(t *testing.T) {
	event := map[string]any{
		"event_id": "$e:test.local",
		"room_id":  "!room:test.local",
		"type":     "m.room.message",
		"sender":   "@alice:test.local",
	}
	_, _, _, _, _, hasStateKey, _, _ := parseFederationPDU(event)
	assert.False(t, hasStateKey)
}

// ===== handleFederationVersion handler test =====

func TestHandleFederationVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/_matrix/federation/v1/version", nil)

	handleFederationVersion(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Community Matrix Homeserver", resp["server"]["name"])
	assert.Equal(t, "1.0.0", resp["server"]["version"])
}

// ===== handleGetServerKey handler test =====

func TestHandleGetServerKey_Handler(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config:     &HSConfig{ServerName: "handler.test"},
		keyID:      "ed25519:1",
		privateKey: priv,
	}
	restore := resetGlobalHS(hs)
	defer restore()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/_matrix/key/v2/server", nil)

	handleGetServerKey(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "handler.test", resp["server_name"])
}
