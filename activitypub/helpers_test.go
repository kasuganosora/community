package activitypub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// newMockInboxServer creates a mock HTTP server that responds with the given status code.
func newMockInboxServer(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
}

// newMockActorServer creates a mock server serving an ActivityPub actor document.
func newMockActorServer(t *testing.T, actor Actor) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		_ = json.NewEncoder(w).Encode(actor)
	}))
}

// requireNoError is a convenience wrapper.
func requireNoError(t *testing.T, err error) {
	t.Helper()
	require.NoError(t, err)
}
