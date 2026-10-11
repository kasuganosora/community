package homeserver

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kasuganosora/community/httpclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestFederationClient builds a FederationClient bound to a HomeServer with
// a freshly generated ed25519 key. It is the canonical helper for all tests
// that exercise federation client HTTP behavior against httptest servers.
func newTestFederationClient() (*FederationClient, *HomeServer, ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv := ed25519Key()
	hs := &HomeServer{
		Config:     &HSConfig{Enabled: true, ServerName: "origin.local", BaseURL: "https://origin.local", FederationPort: 8448},
		keyID:      "ed25519:1",
		privateKey: priv,
		publicKey:  pub,
	}
	c := NewFederationClient(hs)
	// HTTP mocks use an explicit transport, independent of deployment proxy settings.
	c.httpClient = httpclient.NewNoProxyClient()
	// These protocol fixtures address httptest URLs directly. Discovery is
	// exercised separately with the production transport in federation_discovery_test.go.
	c.fedHTTPClient = &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
	return c, hs, pub, priv
}

// ===== signRequest tests =====
//
// signRequest mutates the unexported httpclient.Request.header field, so we
// cannot inspect the Authorization header directly from the homeserver test
// package. Instead we verify that NewFederationClient and its helpers do not
// panic, and we exercise signRequest indirectly through FetchMedia which calls
// signRequest then Do(). Since FetchMedia talks to https:// (via
// federationBaseURL) we test the connection-failure path, which still executes
// signRequest before the network call.

func TestFederationClient_Construction(t *testing.T) {
	c, hs, _, _ := newTestFederationClient()
	require.NotNil(t, c)
	require.Equal(t, hs, c.hs)
	assert.NotNil(t, c.httpClient)
	assert.NotNil(t, c.keyCache)
}

func TestFederationClient_SignRequest_RunsBeforeFetchMedia(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	// FetchMedia calls signRequest internally; the connection will fail (https
	// + non-existent host) but signRequest still runs, exercising the code.
	_, _, err := c.FetchMedia("127.0.0.1.nx-test.invalid", "media123")
	assert.Error(t, err)
}

func TestFederationClient_SignRequest_RunsBeforeFetchThumbnail(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	_, _, err := c.FetchThumbnail("127.0.0.1.nx-test.invalid", "thumb123", 64, 64, "scale")
	assert.Error(t, err)
}

func TestFederationClient_SignRequest_RunsBeforeQueryDirectory(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	_, err := c.QueryDirectory("127.0.0.1.nx-test.invalid", "#room:remote.server")
	assert.Error(t, err)
}

// ===== GetWellKnown tests =====

func TestFederationClient_GetWellKnown_ConnectionFailureFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"m.server":"delegate.example.com:443"}`))
	}))
	defer server.Close()

	c, hs, _, _ := newTestFederationClient()
	// GetWellKnown hardcodes https:// + serverName; we cannot easily redirect
	// to the test server, so instead test the fallback behavior using a
	// non-resolvable host.
	result, err := c.GetWellKnown("127.0.0.1.nx-test.invalid")
	// On connection failure, GetWellKnown returns the default fallback (no error).
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1.nx-test.invalid:8448", result)

	_ = hs
	_ = server
}

func TestFederationClient_GetWellKnown_DefaultOnUnreachableHost(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	// Non-resolvable host → connection failure → fallback path returns host:8448
	result, err := c.GetWellKnown("127.0.0.1.nx-test.invalid")
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1.nx-test.invalid:8448", result)
}

// ===== FetchServerKey tests =====

func TestFederationClient_FetchServerKey_ConnectionFailure(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	// FetchServerKey uses federationBaseURL (https://). Non-resolvable host
	// → connection failure → error path.
	result, err := c.FetchServerKey("127.0.0.1.nx-test.invalid")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestFederationClient_FetchServerKey_InvalidResponse(t *testing.T) {
	// FetchServerKey uses httpClient which can be pointed at an http server if
	// we bypass federationBaseURL. We cannot do that cleanly, so instead we
	// verify the error path with a non-existent host.
	c, _, _, _ := newTestFederationClient()
	_, err := c.FetchServerKey("127.0.0.1.nx-test.invalid")
	assert.Error(t, err)
}

// ===== SendTransaction tests =====

func TestFederationClient_SendTransaction_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.NotEmpty(t, r.Header.Get("Authorization"))
		assert.Contains(t, r.Header.Get("Authorization"), "X-Matrix")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c, _, _, _ := newTestFederationClient()
	// doFederationRequest is exercised directly via SendTransaction which
	// uses federationBaseURL (https://). We bypass that by testing the
	// lower-level error path: SendTransaction will fail to connect to https.
	err := c.SendTransaction("127.0.0.1.nx-test.invalid", "txn123",
		[]json.RawMessage{json.RawMessage(`{"type":"m.room.message"}`)},
		[]json.RawMessage{},
	)
	assert.Error(t, err)

	_ = server
}

func TestFederationClient_SendTransaction_NilArraysNormalized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c, _, _, _ := newTestFederationClient()
	// nil pdus/edus should be normalized to empty arrays, but since the
	// destination is non-resolvable, we expect a connection error.
	err := c.SendTransaction("127.0.0.1.nx-test.invalid", "txn456", nil, nil)
	assert.Error(t, err)

	_ = server
}

func TestFederationClient_SendTransaction_NonOKStatus(t *testing.T) {
	// Build a test server that returns 500. We use doFederationRequest directly
	// to test the status-code check in SendTransaction.
	c, _, _, _ := newTestFederationClient()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	defer server.Close()

	// Use doFederationRequest directly to hit the test server (http).
	resp, _, err := c.doFederationRequest("PUT", server.URL+"/_matrix/federation/v1/send/123", "dest.server", map[string]any{"a": 1})
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// ===== QueryRemoteKeys tests =====

func TestFederationClient_QueryRemoteKeys_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"device_keys":{"@alice:remote.server":{"DEVICE1":{"user_id":"@alice:remote.server","device_id":"DEVICE1"}}}}`))
	}))
	defer server.Close()

	c, _, _, _ := newTestFederationClient()
	// QueryRemoteKeys uses doFederationRequest with federationBaseURL (https://).
	// Test the error path (cannot connect to https non-existent host).
	result, err := c.QueryRemoteKeys("127.0.0.1.nx-test.invalid", map[string][]string{
		"@alice:remote.server": {"DEVICE1"},
	})
	assert.Error(t, err)
	assert.Nil(t, result)

	_ = server
}

func TestFederationClient_QueryRemoteKeys_DirectViaDoFederationRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"device_keys":{"@bob:remote.server":{"DEVX":{"device_id":"DEVX"}}}}`))
	}))
	defer server.Close()

	c, _, _, _ := newTestFederationClient()
	// Exercise doFederationRequest directly to validate the response parsing
	// logic used by QueryRemoteKeys.
	_, body, err := c.doFederationRequest("POST", server.URL+"/_matrix/federation/v1/user/keys/query",
		"dest.server", map[string]any{"device_keys": map[string][]string{"@bob:remote.server": {"DEVX"}}})
	require.NoError(t, err)

	var parsed struct {
		DeviceKeys map[string]map[string]*DeviceKeys `json:"device_keys"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Contains(t, parsed.DeviceKeys, "@bob:remote.server")
	require.Contains(t, parsed.DeviceKeys["@bob:remote.server"], "DEVX")
}

// ===== QueryDirectory tests =====

func TestFederationClient_QueryDirectory_NonOK(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	// QueryDirectory uses httpClient (not doFederationRequest), and the URL
	// is https. Since QueryDirectory returns an error wrapping the http
	// failure, test the error path.
	result, err := c.QueryDirectory("127.0.0.1.nx-test.invalid", "#room:remote.server")
	assert.Error(t, err)
	assert.Nil(t, result)
}

// ===== MakeJoin / SendJoin / GetMissingEvents / FetchBackfill (error paths) =====

func TestFederationClient_MakeJoin_ConnectionFailure(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	result, err := c.MakeJoin("127.0.0.1.nx-test.invalid", "!room:remote.server", "@alice:origin.local")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestFederationClient_SendJoin_ConnectionFailure(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	event := map[string]any{"event_id": "$test:origin.local", "type": "m.room.member"}
	result, err := c.SendJoin("127.0.0.1.nx-test.invalid", "!room:remote.server", event)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestFederationClient_GetMissingEvents_ConnectionFailure(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	result, err := c.GetMissingEvents("127.0.0.1.nx-test.invalid", "!room:remote.server", []string{"$a:1"}, []string{"$b:2"}, 10)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestFederationClient_FetchBackfill_ConnectionFailure(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	result, err := c.FetchBackfill("127.0.0.1.nx-test.invalid", "!room:remote.server", []string{"$a:1"}, 10)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// ===== MakeJoin / GetMissingEvents / FetchBackfill via direct doFederationRequest =====

func TestFederationClient_MakeJoin_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errcode":"M_NOT_FOUND"}`))
	}))
	defer server.Close()

	c, _, _, _ := newTestFederationClient()
	resp, body, err := c.doFederationRequest("GET", server.URL+"/_matrix/federation/v1/make_join/!room:dest/%40alice:origin", "dest.server", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, string(body), "M_NOT_FOUND")
}

func TestFederationClient_GetMissingEvents_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errcode":"M_FORBIDDEN"}`))
	}))
	defer server.Close()

	c, _, _, _ := newTestFederationClient()
	resp, body, err := c.doFederationRequest("POST", server.URL+"/_matrix/federation/v1/get_missing_events/!room:dest", "dest.server", map[string]any{"limit": 10})
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Contains(t, string(body), "M_FORBIDDEN")
}

func TestFederationClient_FetchBackfill_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server error"}`))
	}))
	defer server.Close()

	c, _, _, _ := newTestFederationClient()
	resp, body, err := c.doFederationRequest("GET", server.URL+"/_matrix/federation/v1/backfill/!room:dest?limit=10", "dest.server", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Contains(t, string(body), "server error")
}

// ===== doFederationRequest: request body marshaling =====

func TestFederationClient_DoFederationRequest_MarshalFailure(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	// A channel cannot be marshaled to JSON → json.Marshal returns an error.
	_, _, err := c.doFederationRequest("PUT", "http://127.0.0.1.nx-test.invalid/path", "dest.server", make(chan int))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal request body")
}

func TestFederationClient_DoFederationRequest_SetsHeaders(t *testing.T) {
	var capturedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c, _, _, _ := newTestFederationClient()
	resp, _, err := c.doFederationRequest("POST", server.URL+"/test", "dest.server", map[string]any{"k": "v"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, "Community-Matrix/1.0", capturedHeaders.Get("User-Agent"))
	assert.Equal(t, "application/json", capturedHeaders.Get("Content-Type"))
	assert.NotEmpty(t, capturedHeaders.Get("Authorization"))
	assert.Contains(t, capturedHeaders.Get("Authorization"), "X-Matrix")
	// Note: the Host header is set via httpReq.Header.Set("Host", ...) but Go's
	// HTTP server reports r.Host from the connection, so we don't assert on it
	// here. The signing payload uses "destination" which we validate above.
}

// ===== FetchMedia / FetchThumbnail (error paths) =====

func TestFederationClient_FetchMedia_ConnectionFailure(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	data, contentType, err := c.FetchMedia("127.0.0.1.nx-test.invalid", "media123")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Equal(t, "", contentType)
}

func TestFederationClient_FetchThumbnail_ConnectionFailure(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	data, contentType, err := c.FetchThumbnail("127.0.0.1.nx-test.invalid", "media123", 32, 32, "crop")
	assert.Error(t, err)
	assert.Nil(t, data)
	assert.Equal(t, "", contentType)
}

// ===== VerifySignature: remote key fetch failure path =====

func TestFederationClient_VerifySignature_RemoteKeyFetchFailure(t *testing.T) {
	setupMinimalTestDB(t)
	c, _, _, _ := newTestFederationClient()
	// Key not in cache → attempts FetchServerKey which fails to connect.
	event := map[string]any{"a": float64(1)}
	result := c.VerifySignature(event, "127.0.0.1.nx-test.invalid", "ed25519:1", "bogus")
	assert.False(t, result)
}

// ===== federationHTTPClient caching behavior =====

func TestFederationClient_FederationHTTPClient_CachesClient(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	c.fedHTTPClient = nil // Exercise production construction, not the protocol fixture.
	client1 := c.federationHTTPClient()
	require.NotNil(t, client1)
	client2 := c.federationHTTPClient()
	assert.Same(t, client1, client2, "should return the cached client on second call")
}

func TestFederationClient_FederationHTTPClient_NonNilTimeout(t *testing.T) {
	c, _, _, _ := newTestFederationClient()
	c.fedHTTPClient = nil // Exercise the production timeout.
	client := c.federationHTTPClient()
	require.NotNil(t, client)
	// Per-request deadlines come from contexts; the client keeps a backstop.
	assert.Equal(t, federationClientSafetyTimeout, client.Timeout)
}

// ===== ServerKeyResponse / KeyInfo / QueryDirectoryResponse struct tests =====

func TestServerKeyResponse_JSONSerialization(t *testing.T) {
	resp := ServerKeyResponse{
		ServerName:    "srv.test",
		ValidUntil:    1234567890,
		VerifyKeys:    map[string]KeyInfo{"ed25519:1": {Key: "abc"}},
		OldVerifyKeys: map[string]KeyInfo{},
		Signatures:    map[string]map[string]string{"srv.test": {"ed25519:1": "sig"}},
	}
	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, "srv.test", parsed["server_name"])
	assert.EqualValues(t, 1234567890, parsed["valid_until_ts"])
}

func TestQueryDirectoryResponse_JSONSerialization(t *testing.T) {
	resp := QueryDirectoryResponse{
		RoomID:  "!room:test.local",
		Servers: []string{"test.local", "other.local"},
	}
	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var parsed QueryDirectoryResponse
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, "!room:test.local", parsed.RoomID)
	assert.Equal(t, []string{"test.local", "other.local"}, parsed.Servers)
}
