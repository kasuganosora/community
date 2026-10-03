package homeserver

import (
	"encoding/pem"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFederationHTTPClientConcurrentInitialization(t *testing.T) {
	client := &FederationClient{}
	results := make(chan *http.Client, 24)
	var workers sync.WaitGroup
	for range 24 {
		workers.Go(func() { results <- client.federationHTTPClient() })
	}
	workers.Wait()
	close(results)
	first := <-results
	for current := range results {
		require.Same(t, first, current)
	}
}

func TestFederationExtraCADoesNotDisableTLSVerification(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	caPath := filepath.Join(t.TempDir(), "lab-ca.pem")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600))
	trusted := federationTLSConfig(caPath)
	require.False(t, trusted.InsecureSkipVerify)
	transport := &http.Transport{TLSClientConfig: trusted}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Get(server.URL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.NoError(t, response.Body.Close())

	// Merely placing an invalid CA file at the configured path grants no trust.
	require.NoError(t, os.WriteFile(caPath, []byte("invalid certificate"), 0600))
	untrusted := &http.Transport{TLSClientConfig: federationTLSConfig(caPath)}
	defer untrusted.CloseIdleConnections()
	_, err = (&http.Client{Transport: untrusted}).Get(server.URL)
	require.Error(t, err)

	// A trusted certificate still must identify the requested server.
	wrongName := trusted.Clone()
	wrongName.ServerName = "wrong.federation.test"
	mismatch := &http.Transport{TLSClientConfig: wrongName}
	defer mismatch.CloseIdleConnections()
	_, err = (&http.Client{Transport: mismatch}).Get(server.URL)
	require.Error(t, err)
}
