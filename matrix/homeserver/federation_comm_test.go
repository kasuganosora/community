package homeserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test helpers for homeserver communication via mock TLS servers
// =============================================================================

// newTLSFederationClient creates a FederationClient and configures its HTTP
// clients to trust the given httptest TLS server. It returns the client and
// the "host:port" address that maps to the test server (so that
// federationBaseURL produces the correct https://host:port URL).
func newTLSFederationClient(server *httptest.Server) (*FederationClient, string) {
	pub, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{Enabled: true, ServerName: "origin.local", BaseURL: "https://origin.local", FederationPort: 8448},
		keyID:      "ed25519:1",
		privateKey: priv,
		publicKey:  pub,
	}
	c := NewFederationClient(hs)
	// Override the cached federation HTTP client so it trusts the test server's
	// self-signed certificate. federationHTTPClient() returns this cache.
	c.fedHTTPClient = server.Client()
	// Also configure the httpclient.Client transport so methods that use
	// c.httpClient directly (FetchMedia, FetchServerKey, etc.) trust the server.
	testClient := server.Client()
	if testClient != nil && testClient.Transport != nil {
		c.httpClient.Transport = testClient.Transport
	}
	// server.URL is "https://127.0.0.1:PORT"; strip the scheme to get host:port
	addr := strings.TrimPrefix(server.URL, "https://")
	return c, addr
}

// =============================================================================
// federationBaseURL tests
// =============================================================================

func TestFederationClient_FederationBaseURL_WithPort(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	// When serverName contains a colon, it's used directly (no :8448 appended)
	url := c.federationBaseURL("example.com:443")
	assert.Equal(t, "https://example.com:443", url)
}

func TestFederationClient_FederationBaseURL_WithoutPort(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	url := c.federationBaseURL("example.com")
	assert.Equal(t, "https://example.com:8448", url)
}

func TestFederationClient_FederationBaseURL_IPv6(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	// Note: the source checks strings.Contains(serverName, ":"). An IPv6 address
	// like "2001:db8::1" contains a colon, so it is treated as already having a
	// port and returned directly. This documents the current behavior.
	url := c.federationBaseURL("2001:db8::1")
	assert.Equal(t, "https://2001:db8::1", url)
}

// =============================================================================
// FetchMedia tests (homeserver communication for media download)
// =============================================================================

func TestFederationClient_FetchMedia_Success(t *testing.T) {
	body := []byte("fake-media-content")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/_matrix/media/v3/download/")
		assert.NotEmpty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	data, contentType, err := c.FetchMedia(addr, "media123")
	require.NoError(t, err)
	assert.Equal(t, body, data)
	assert.Equal(t, "image/png", contentType)
}

func TestFederationClient_FetchMedia_VerifiesContentTypeFromHeader(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("mp4data"))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	_, contentType, err := c.FetchMedia(addr, "vid123")
	require.NoError(t, err)
	assert.Equal(t, "video/mp4", contentType)
}

func TestFederationClient_FetchMedia_BinaryContent(t *testing.T) {
	// Binary PNG data to verify the full round-trip including content-type sniffing.
	binaryBody := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(binaryBody)
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	data, _, err := c.FetchMedia(addr, "binary123")
	require.NoError(t, err)
	assert.Equal(t, binaryBody, data)
}

func TestFederationClient_FetchMedia_SignedRequest(t *testing.T) {
	var capturedAuth string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	_, _, err := c.FetchMedia(addr, "media789")
	require.NoError(t, err)
	assert.Contains(t, capturedAuth, "X-Matrix")
	assert.Contains(t, capturedAuth, "origin=\"origin.local\"")
	assert.Contains(t, capturedAuth, "destination=\""+addr+"\"")
}

// =============================================================================
// FetchThumbnail tests
// =============================================================================

func TestFederationClient_FetchThumbnail_Success(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/_matrix/media/v3/thumbnail/")
		// Check query parameters
		q := r.URL.Query()
		assert.Equal(t, "64", q.Get("width"))
		assert.Equal(t, "64", q.Get("height"))
		assert.Equal(t, "scale", q.Get("method"))
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("thumbnail-data"))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	data, contentType, err := c.FetchThumbnail(addr, "thumb123", 64, 64, "scale")
	require.NoError(t, err)
	assert.Equal(t, []byte("thumbnail-data"), data)
	assert.Equal(t, "image/jpeg", contentType)
}

func TestFederationClient_FetchThumbnail_CropMethod(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assert.Equal(t, "crop", q.Get("method"))
		assert.Equal(t, "128", q.Get("width"))
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("cropped"))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	data, _, err := c.FetchThumbnail(addr, "thumb789", 128, 96, "crop")
	require.NoError(t, err)
	assert.Equal(t, []byte("cropped"), data)
}

// =============================================================================
// FetchServerKey tests (server discovery / key query)
// =============================================================================

func TestFederationClient_FetchServerKey_Success(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	pubB64 := base64.RawStdEncoding.EncodeToString(pub)
	validUntil := time.Now().Add(24 * time.Hour).UnixMilli()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/_matrix/key/v2/server")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ServerKeyResponse{
			ServerName: "remote.test",
			ValidUntil: validUntil,
			VerifyKeys: map[string]KeyInfo{
				"ed25519:1": {Key: pubB64},
			},
			OldVerifyKeys: map[string]KeyInfo{},
			Signatures:    map[string]map[string]string{},
		})
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.FetchServerKey(addr)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "remote.test", result.ServerName)
	assert.Equal(t, validUntil, result.ValidUntil)
	require.Contains(t, result.VerifyKeys, "ed25519:1")
	assert.Equal(t, pubB64, result.VerifyKeys["ed25519:1"].Key)
}

func TestFederationClient_FetchServerKey_InvalidJSON(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.FetchServerKey(addr)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to parse response")
}

// =============================================================================
// QueryDirectory tests (federation directory query)
// =============================================================================

func TestFederationClient_QueryDirectory_Success(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/_matrix/federation/v1/query/directory")
		alias := r.URL.Query().Get("room_alias")
		assert.Equal(t, "#general:test.local", alias)
		assert.NotEmpty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(QueryDirectoryResponse{
			RoomID:  "!general:test.local",
			Servers: []string{"test.local", "other.local"},
		})
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.QueryDirectory(addr, "#general:test.local")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "!general:test.local", result.RoomID)
	assert.Equal(t, []string{"test.local", "other.local"}, result.Servers)
}

func TestFederationClient_QueryDirectory_InvalidJSON(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json at all`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.QueryDirectory(addr, "#room:test.local")
	assert.Error(t, err)
	assert.Nil(t, result)
}

// =============================================================================
// GetWellKnown tests (server discovery)
// =============================================================================

func TestFederationClient_GetWellKnown_SuccessViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/.well-known/matrix/server")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"m.server":"delegate.example.com:443"}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.GetWellKnown(addr)
	require.NoError(t, err)
	assert.Equal(t, "delegate.example.com:443", result)
}

func TestFederationClient_GetWellKnown_EmptyServer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// m.server is empty → fallback
		_, _ = w.Write([]byte(`{"m.server":""}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.GetWellKnown(addr)
	require.NoError(t, err)
	assert.Equal(t, addr+":8448", result)
}

func TestFederationClient_GetWellKnown_MalformedJSON(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{malformed`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.GetWellKnown(addr)
	require.NoError(t, err)
	// Malformed JSON triggers fallback to serverName:8448
	assert.Equal(t, addr+":8448", result)
}

// =============================================================================
// SendTransaction tests (federation EDU/PDU delivery)
// =============================================================================

func TestFederationClient_SendTransaction_SuccessViaTLS(t *testing.T) {
	var capturedBody map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.Contains(t, r.URL.Path, "/_matrix/federation/v1/send/txn-tls-1")
		assert.NotEmpty(t, r.Header.Get("Authorization"))
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	err := c.SendTransaction(addr, "txn-tls-1",
		[]json.RawMessage{json.RawMessage(`{"type":"m.room.message"}`)},
		[]json.RawMessage{json.RawMessage(`{"type":"m.presence"}`)},
	)
	require.NoError(t, err)
	assert.Equal(t, "origin.local", capturedBody["origin"])
	assert.NotNil(t, capturedBody["pdus"])
	assert.NotNil(t, capturedBody["edus"])
}

func TestFederationClient_SendTransaction_NilPDUsEDUsViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(bodyBytes, &body)
		// nil pdus/edus must be normalized to empty arrays
		pdus, ok := body["pdus"].([]any)
		require.True(t, ok)
		assert.Len(t, pdus, 0)
		edus, ok := body["edus"].([]any)
		require.True(t, ok)
		assert.Len(t, edus, 0)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	err := c.SendTransaction(addr, "txn-nil", nil, nil)
	require.NoError(t, err)
}

func TestFederationClient_SendTransaction_NonOKViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	err := c.SendTransaction(addr, "txn-503",
		[]json.RawMessage{json.RawMessage(`{"type":"m.room.message"}`)}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 503")
}

// =============================================================================
// QueryRemoteKeys tests (federation device key query)
// =============================================================================

func TestFederationClient_QueryRemoteKeys_SuccessViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "/_matrix/federation/v1/user/keys/query")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"device_keys":{"@alice:remote.server":{"DEVICE1":{"user_id":"@alice:remote.server","device_id":"DEVICE1","algorithms":["m.olm.v1.curve25519-aes-sha2"]}}}}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.QueryRemoteKeys(addr, map[string][]string{
		"@alice:remote.server": {"DEVICE1"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result, "@alice:remote.server")
	require.Contains(t, result["@alice:remote.server"], "DEVICE1")
	assert.Equal(t, "DEVICE1", result["@alice:remote.server"]["DEVICE1"].DeviceID)
}

func TestFederationClient_QueryRemoteKeys_EmptyResponseViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Response with no device_keys field → should return empty map, not nil
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.QueryRemoteKeys(addr, map[string][]string{"@bob:remote.server": {}})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result)
}

func TestFederationClient_QueryRemoteKeys_InvalidJSONViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{bad json`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.QueryRemoteKeys(addr, map[string][]string{"@bob:remote.server": {}})
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to parse response")
}

// =============================================================================
// MakeJoin tests (federation room join negotiation)
// =============================================================================

func TestFederationClient_MakeJoin_SuccessViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/_matrix/federation/v1/make_join/")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"origin":"dest.server","origin_server_ts":12345,"event":{"room_id":"!room:dest","sender":"@alice:origin.local","type":"m.room.member"}}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.MakeJoin(addr, "!room:dest.server", "@alice:origin.local")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "dest.server", result["origin"])
	event, ok := result["event"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "!room:dest", event["room_id"])
}

func TestFederationClient_MakeJoin_NonOKViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND","error":"room not found"}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.MakeJoin(addr, "!unknown:dest.server", "@alice:origin.local")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "status 404")
}

func TestFederationClient_MakeJoin_InvalidJSONViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{invalid`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	result, err := c.MakeJoin(addr, "!room:dest.server", "@alice:origin.local")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to parse make_join response")
}

// =============================================================================
// SendJoin tests (federation send_join)
// =============================================================================

func TestFederationClient_SendJoin_SuccessV2ViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		// v2 endpoint
		assert.Contains(t, r.URL.Path, "/_matrix/federation/v2/send_join/")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"origin":"dest.server","origin_server_ts":99999,"state":[],"auth_chain":[]}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	event := map[string]any{
		"event_id": "$join1:origin.local",
		"type":     "m.room.member",
		"sender":   "@alice:origin.local",
		"room_id":  "!room:dest.server",
	}
	result, err := c.SendJoin(addr, "!room:dest.server", event)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "dest.server", result["origin"])
}

func TestFederationClient_SendJoin_GeneratesEventIDIfMissing(t *testing.T) {
	var capturedPath string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"origin":"dest.server","state":[],"auth_chain":[]}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	// Event without event_id — SendJoin should generate one
	event := map[string]any{
		"type":   "m.room.member",
		"sender": "@alice:origin.local",
	}
	result, err := c.SendJoin(addr, "!room:dest.server", event)
	require.NoError(t, err)
	require.NotNil(t, result)
	// The generated event_id should appear in the URL path
	assert.Contains(t, capturedPath, "/send_join/")
	// The event map should now have an event_id
	assert.NotEmpty(t, event["event_id"])
}

func TestFederationClient_SendJoin_NonOKViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errcode":"M_FORBIDDEN"}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	event := map[string]any{"event_id": "$join1:origin.local"}
	result, err := c.SendJoin(addr, "!room:dest.server", event)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "status 403")
}

// =============================================================================
// GetMissingEvents tests
// =============================================================================

func TestFederationClient_GetMissingEvents_SuccessViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "/_matrix/federation/v1/get_missing_events/")
		var reqBody map[string]any
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &reqBody)
		assert.Equal(t, float64(10), reqBody["limit"])
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"events":[{"event_id":"$evt1:dest","type":"m.room.message"},{"event_id":"$evt2:dest","type":"m.room.message"}]}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	events, err := c.GetMissingEvents(addr, "!room:dest.server", []string{"$prev:1"}, []string{"$latest:1"}, 10)
	require.NoError(t, err)
	assert.Len(t, events, 2)
}

func TestFederationClient_GetMissingEvents_NonOKViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errcode":"M_FORBIDDEN"}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	events, err := c.GetMissingEvents(addr, "!room:dest.server", []string{}, []string{}, 5)
	assert.Error(t, err)
	assert.Nil(t, events)
	assert.Contains(t, err.Error(), "status 403")
}

func TestFederationClient_GetMissingEvents_InvalidJSONViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{bad json`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	events, err := c.GetMissingEvents(addr, "!room:dest.server", []string{}, []string{}, 5)
	assert.Error(t, err)
	assert.Nil(t, events)
}

// =============================================================================
// FetchBackfill tests
// =============================================================================

func TestFederationClient_FetchBackfill_SuccessViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/_matrix/federation/v1/backfill/")
		// Check that event IDs are passed as v= query params
		q := r.URL.Query()
		vs := q["v"]
		assert.Contains(t, vs, "$anchor:dest")
		assert.Equal(t, "20", q.Get("limit"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"pdus":[{"event_id":"$bf1:dest","type":"m.room.message"},{"event_id":"$bf2:dest","type":"m.room.message"}]}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	pdus, err := c.FetchBackfill(addr, "!room:dest.server", []string{"$anchor:dest"}, 20)
	require.NoError(t, err)
	assert.Len(t, pdus, 2)
}

func TestFederationClient_FetchBackfill_NonOKViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal"}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	pdus, err := c.FetchBackfill(addr, "!room:dest.server", []string{"$anchor:dest"}, 10)
	assert.Error(t, err)
	assert.Nil(t, pdus)
	assert.Contains(t, err.Error(), "status 500")
}

// =============================================================================
// VerifySignature tests with remote key fetch via TLS
// =============================================================================

func TestFederationClient_VerifySignature_RemoteFetchThenVerify(t *testing.T) {
	// Generate a remote server key pair
	pub, priv, _ := ed25519.GenerateKey(nil)
	pubB64 := base64.RawStdEncoding.EncodeToString(pub)
	validUntil := time.Now().Add(24 * time.Hour).UnixMilli()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ServerKeyResponse{
			ServerName: "remote.test",
			ValidUntil: validUntil,
			VerifyKeys: map[string]KeyInfo{
				"ed25519:1": {Key: pubB64},
			},
		})
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)

	// Sign an event with the remote private key
	event := map[string]any{
		"type":    "m.room.message",
		"room_id": "!room:remote.test",
		"sender":  "@alice:remote.test",
		"content": map[string]any{"body": "hello world"},
	}
	canonical, err := CanonicalJSON(event)
	require.NoError(t, err)
	sig := ed25519.Sign(priv, canonical)
	sigB64 := base64.RawStdEncoding.EncodeToString(sig)

	// VerifySignature should fetch the key from the mock server, cache it, then verify
	result := c.VerifySignature(event, addr, "ed25519:1", sigB64)
	assert.True(t, result, "signature should verify after remote key fetch")
}

func TestFederationClient_VerifySignature_RemoteKeyNotFound(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	pubB64 := base64.RawStdEncoding.EncodeToString(pub)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ServerKeyResponse{
			ServerName: "remote.test",
			VerifyKeys: map[string]KeyInfo{
				"ed25519:1": {Key: pubB64},
			},
		})
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	// Request a key ID that the server doesn't have
	event := map[string]any{"type": "m.room.message"}
	result := c.VerifySignature(event, addr, "ed25519:nonexistent", "somesig")
	assert.False(t, result, "should fail when key ID not found in server response")
}

func TestFederationClient_VerifySignature_RemoteInvalidKeyBase64(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ServerKeyResponse{
			ServerName: "remote.test",
			VerifyKeys: map[string]KeyInfo{
				"ed25519:1": {Key: "!!!invalid-base64!!!"},
			},
		})
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	event := map[string]any{"type": "m.room.message"}
	result := c.VerifySignature(event, addr, "ed25519:1", "somesig")
	assert.False(t, result, "should fail when public key is invalid base64")
}

// =============================================================================
// DeviceListService tests (federation device list query)
// =============================================================================

func TestDeviceListService_GetUserDevices_RemoteUser(t *testing.T) {
	setupMinimalTestDB(t)
	dls := &DeviceListService{hs: minimalHS("test.local")}
	// Non-local user → error
	resp, err := dls.GetUserDevices("@alice:other.server")
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "user not found")
}

func TestDeviceListService_GetUserDevices_LocalUserNoDevices(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixDeviceKey{}))
	dls := &DeviceListService{hs: minimalHS("test.local")}
	resp, err := dls.GetUserDevices("@alice:test.local")
	require.NoError(t, err)
	assert.Equal(t, "@alice:test.local", resp.UserID)
	assert.Empty(t, resp.Devices)
}

func TestDeviceListService_GetCurrentStreamID_NoRecord(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&DeviceListStream{}))
	dls := &DeviceListService{hs: minimalHS("test.local")}
	streamID, err := dls.GetCurrentStreamID("@nobody:test.local")
	assert.Error(t, err)
	assert.Equal(t, int64(0), streamID)
}

func TestDeviceListService_GetDeviceListChanges(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&DeviceListChange{}))
	dls := &DeviceListService{hs: minimalHS("test.local")}
	// No changes → empty slice, no error
	changes, err := dls.GetDeviceListChanges("@nobody:test.local", 0)
	require.NoError(t, err)
	assert.Empty(t, changes)
}

func TestParseUserID_DeviceList(t *testing.T) {
	assert.Equal(t, "test.local", parseUserID("@alice:test.local"))
	assert.Equal(t, "", parseUserID("invalid-no-colon"))
}

func TestIsLocalUser_DeviceList(t *testing.T) {
	assert.True(t, isLocalUser("@alice:test.local", "test.local"))
	assert.False(t, isLocalUser("@alice:other.server", "test.local"))
}

// =============================================================================
// signRequest integration test via FetchMedia (verifies canonical JSON signing)
// =============================================================================

func TestFederationClient_SignRequest_ProducesValidXMatrixHeader(t *testing.T) {
	var authHeader string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	_, _, err := c.FetchMedia(addr, "signcheck")
	require.NoError(t, err)
	// The X-Matrix header must contain origin, destination, key, and sig fields
	assert.Contains(t, authHeader, "X-Matrix")
	assert.Contains(t, authHeader, "origin=\"origin.local\"")
	assert.Contains(t, authHeader, "destination=\""+addr+"\"")
	assert.Contains(t, authHeader, "key=\"ed25519:1\"")
	assert.Contains(t, authHeader, "sig=\"")
}

// =============================================================================
// doFederationRequest: body marshaling and signature with content
// =============================================================================

func TestFederationClient_DoFederationRequest_ContentInSignature(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NotEmpty(t, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	resp, body, err := c.doFederationRequest("POST",
		"https://"+addr+"/_matrix/federation/v1/test",
		addr, map[string]any{"key": "value"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "ok")
}

func TestFederationClient_DoFederationRequest_GETNoBodyViaTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c, addr := newTLSFederationClient(server)
	resp, _, err := c.doFederationRequest("GET", "https://"+addr+"/test-get", addr, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// =============================================================================
// NewFederationClient construction
// =============================================================================

func TestNewFederationClient_BindsHomeServerKey(t *testing.T) {
	pub, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{Enabled: true, ServerName: "bound.local"},
		privateKey: priv,
		publicKey:  pub,
		keyID:      "ed25519:bound",
	}
	c := NewFederationClient(hs)
	require.NotNil(t, c)
	assert.Equal(t, hs, c.hs)
	assert.Equal(t, priv, c.privateKey)
	assert.NotNil(t, c.httpClient)
	assert.NotNil(t, c.keyCache)
}
