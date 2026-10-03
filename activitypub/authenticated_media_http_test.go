package activitypub

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthenticatedMediaHTTPBlocksPrivateTargetsBeforeTLS(t *testing.T) {
	t.Setenv(labPeersEnv, "")
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client := NewAuthenticatedMediaHTTPClient(&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	_, err := client.Get(server.URL + "/private")
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked connection to internal address")
	require.Zero(t, requests.Load())
}
func TestAuthenticatedMediaHTTPNeverFollowsRedirectToPrivateHTTP(t *testing.T) {
	client := NewAuthenticatedMediaHTTPClient(nil)
	var calls int
	client.Transport = outboundTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "https://public.example/media", r.URL.String())
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"http://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: r}, nil
	})
	response, err := client.Get("https://public.example/media")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, 302, response.StatusCode)
	require.Equal(t, 1, calls)
}
func TestAuthenticatedMediaHTTPRetainsPinnedLabGuard(t *testing.T) {
	t.Setenv(labPeersEnv, "synapse.federation.test=172.30.50.10")
	client := NewAuthenticatedMediaHTTPClient(nil)
	// Guard executes before dialing, including port restrictions and TLS-only.
	for _, target := range []string{"http://synapse.federation.test:8448/media", "https://synapse.federation.test:8080/media"} {
		_, err := client.Get(target)
		require.Error(t, err)
		require.Contains(t, err.Error(), "lab peer")
	}
}
