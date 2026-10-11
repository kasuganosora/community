package homeserver

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type keyTestTransport func(*http.Request) (*http.Response, error)

func (f keyTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchServerKeysUsesConfiguredClientTransport(t *testing.T) {
	client, _, _, _ := newTestFederationClient()
	calls := 0
	client.httpClient.Transport = keyTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "https://keys.example.invalid/_matrix/key/v2/server", r.URL.String())
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"server_name":"keys.example.invalid","verify_keys":{}}`)), Request: r}, nil
	})
	response, err := fetchServerKeys(client, "https://keys.example.invalid", "keys.example.invalid")
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, "keys.example.invalid", response.ServerName)
}
