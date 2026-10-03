package homeserver

import (
	"encoding/json"
	"github.com/kasuganosora/community/db"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteKeyBundleKeepsPublicSigningKeysOnly(t *testing.T) {
	user := "@bob:remote.test"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/_matrix/federation/v1/user/keys/query", r.URL.Path)
		require.NotEmpty(t, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(QueryKeysResponse{DeviceKeys: map[string]map[string]*DeviceKeys{user: {"B": {UserID: user, DeviceID: "B", Keys: map[string]string{"curve25519:B": "public"}}}, "@injected:other.test": {"X": {UserID: "@injected:other.test", DeviceID: "X"}}}, MasterKeys: map[string]*CrossSigningKey{user: {UserID: user, Usage: []string{"master"}, Keys: map[string]string{"ed25519:master": "public"}}}, SelfSigningKeys: map[string]*CrossSigningKey{user: {UserID: user, Usage: []string{"self_signing"}}}, UserSigningKeys: map[string]*CrossSigningKey{user: {UserID: user, Usage: []string{"user_signing"}}}})
	}))
	defer server.Close()
	client, host := newTLSFederationClient(server)
	result, err := client.QueryRemoteKeyBundle(host, map[string][]string{user: {}})
	require.NoError(t, err)
	require.Len(t, result.DeviceKeys, 1)
	require.Contains(t, result.MasterKeys, user)
	require.Contains(t, result.SelfSigningKeys, user)
	require.Empty(t, result.UserSigningKeys)
}
func TestRemoteClaimPreservesSignedOneTimeKey(t *testing.T) {
	user := "@bob:remote.test"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/_matrix/federation/v1/user/keys/claim", r.URL.Path)
		require.NotEmpty(t, r.Header.Get("Authorization"))
		var request ClaimKeysRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, "signed_curve25519", request.OneTimeKeys[user]["B"])
		_, _ = w.Write([]byte(`{"one_time_keys":{"@bob:remote.test":{"B":{"signed_curve25519:7":{"key":"curve-key","signatures":{"@bob:remote.test":{"ed25519:B":"signature"}}}}},"@injected:other.test":{"X":{"signed_curve25519:8":{"key":"wrong"}}}}}`))
	}))
	defer server.Close()
	client, host := newTLSFederationClient(server)
	result, err := client.ClaimRemoteKeys(host, map[string]map[string]string{user: {"B": "signed_curve25519"}})
	require.NoError(t, err)
	require.Len(t, result.OneTimeKeys, 1)
	var key map[string]any
	require.NoError(t, json.Unmarshal(result.OneTimeKeys[user]["B"]["signed_curve25519:7"], &key))
	require.Equal(t, "curve-key", key["key"])
	require.NotNil(t, key["signatures"])
}
func TestRemoteKeysRejectHTTPFailuresAndIdentityMismatch(t *testing.T) {
	for _, mode := range []string{"http", "identity", "algorithm"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "http" {
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{}`))
					return
				}
				if mode == "identity" {
					_, _ = w.Write([]byte(`{"device_keys":{"@bob:remote.test":{"B":{"user_id":"@mallory:remote.test","device_id":"B"}}}}`))
					return
				}
				_, _ = w.Write([]byte(`{"one_time_keys":{"@bob:remote.test":{"B":{"curve25519:7":"wrong algorithm"}}}}`))
			}))
			defer server.Close()
			client, host := newTLSFederationClient(server)
			if mode != "algorithm" {
				_, err := client.QueryRemoteKeyBundle(host, map[string][]string{"@bob:remote.test": {}})
				require.Error(t, err)
			}
			if mode != "identity" {
				_, err := client.ClaimRemoteKeys(host, map[string]map[string]string{"@bob:remote.test": {"B": "signed_curve25519"}})
				require.Error(t, err)
			}
		})
	}
}

func TestConcurrentOneTimeKeyClaimsConsumeExactlyOnce(t *testing.T) {
	hs := createTestHomeServer(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixOneTimeKey{}))
	hs.KeyStore = NewKeyStore(hs)
	sqlDB, err := db.GetDB().DB()
	require.NoError(t, err)
	previous := sqlDB.Stats().MaxOpenConnections
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.SetMaxOpenConns(previous)
	require.NoError(t, db.GetDB().Create(&MatrixOneTimeKey{UserID: "@alice:test.local", DeviceID: "A", KeyID: "unique", Algorithm: "signed_curve25519", PublicKey: `{"key":"one-use"}`}).Error)
	start := make(chan struct{})
	results := make(chan int, 16)
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			<-start
			response, err := hs.KeyStore.ClaimOneTimeKeys(&ClaimKeysRequest{OneTimeKeys: map[string]map[string]string{"@alice:test.local": {"A": "signed_curve25519"}}})
			if err != nil {
				failures <- err
				results <- 0
				return
			}
			results <- len(response.OneTimeKeys["@alice:test.local"]["A"])
		}()
	}
	close(start)
	total := 0
	for i := 0; i < 16; i++ {
		total += <-results
	}
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.Equal(t, 1, total)
}
