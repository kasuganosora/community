package homeserver

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

func TestFederationDiscoveryPreservesSignatureAndReplaysBody(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "delegated.example.com:443", r.Host)
		require.Equal(t, "example.com", r.TLS.ServerName)
		require.Equal(t, `X-Matrix destination="origin.example"`, r.Header.Get("Authorization"))
		require.Equal(t, "/_matrix/federation/v2/send_join/!room:origin.example/$event", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, `{"event":"signed"}`, string(body))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	config := federationTLSConfig("")
	config.RootCAs = x509.NewCertPool()
	config.RootCAs.AddCert(server.Certificate())
	transport := newFederationDiscoveryTransport(config)
	defer transport.CloseIdleConnections()
	resolutions := 0
	transport.resolve = func(_ context.Context, name spec.ServerName) ([]fclient.ResolutionResult, error) {
		resolutions++
		require.Equal(t, spec.ServerName("origin.example"), name)
		return []fclient.ResolutionResult{
			{Destination: "127.0.0.1:1", Host: "delegated.example.com:443", TLSServerName: "example.com"},
			{Destination: strings.TrimPrefix(server.URL, "https://"), Host: "delegated.example.com:443", TLSServerName: "example.com"},
		}, nil
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for range 2 {
		req, err := http.NewRequest("PUT", "https://origin.example:8448/_matrix/federation/v2/send_join/!room:origin.example/$event", strings.NewReader(`{"event":"signed"}`))
		require.NoError(t, err)
		req.Header.Set("Host", "origin.example")
		req.Header.Set("Authorization", `X-Matrix destination="origin.example"`)
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, "origin.example:8448", req.URL.Host)
		require.Equal(t, "origin.example", req.Header.Get("Host"))
	}
	require.Equal(t, 2, requests)
	require.Equal(t, 1, resolutions)
}

func TestFederationDiscoveryRejectsWrongTLSIdentity(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request reached server with invalid TLS identity") }))
	defer server.Close()
	config := federationTLSConfig("")
	config.RootCAs = x509.NewCertPool()
	config.RootCAs.AddCert(server.Certificate())
	transport := newFederationDiscoveryTransport(config)
	defer transport.CloseIdleConnections()
	transport.resolve = func(context.Context, spec.ServerName) ([]fclient.ResolutionResult, error) {
		return []fclient.ResolutionResult{{Destination: strings.TrimPrefix(server.URL, "https://"), Host: "logical.example", TLSServerName: "wrong.invalid"}}, nil
	}
	req, err := http.NewRequest("GET", "https://logical.example/_matrix/key/v2/server", nil)
	require.NoError(t, err)
	_, err = transport.RoundTrip(req)
	require.Error(t, err)
	require.Empty(t, transport.resolutions, "failed destinations must be refreshed next attempt")
}

func TestFederationDiscoveryHonorsExplicitPortAndIPv6(t *testing.T) {
	transport := newFederationDiscoveryTransport(federationTLSConfig(""))
	for _, tc := range []struct{ name, destination, tlsName string }{
		{"example.com:9448", "example.com:9448", "example.com"},
		{"[2001:db8::1]", "[2001:db8::1]:8448", "2001:db8::1"},
		{"127.0.0.1:9448", "127.0.0.1:9448", "127.0.0.1"},
	} {
		results, err := transport.destinations(context.Background(), spec.ServerName(tc.name))
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, tc.destination, results[0].Destination)
		require.Equal(t, tc.tlsName, results[0].TLSServerName)
	}
	_, err := transport.destinations(context.Background(), "https://not-a-server/path")
	require.Error(t, err)
}

func TestFederationDirectoryDiscoversWellKnownTarget(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/_matrix/federation/v1/query/directory", r.URL.Path)
		require.Equal(t, "#offtopic-godot:origin.example", r.URL.Query().Get("room_alias"))
		require.Contains(t, r.Header.Get("Authorization"), `destination="origin.example"`)
		_, _ = w.Write([]byte(`{"room_id":"!actual:remote.example","servers":["remote.example"]}`))
	}))
	defer server.Close()
	client, _, _, _ := newTestFederationClient()
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	discovered := 0
	http.DefaultTransport = keyTestTransport(func(req *http.Request) (*http.Response, error) {
		discovered++
		require.Equal(t, "https://origin.example/.well-known/matrix/server", req.URL.String())
		require.Empty(t, req.Header.Get("Authorization"), "discovery itself must not forward signatures")
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"m.server":"` + strings.TrimPrefix(server.URL, "https://") + `"}`))}, nil
	})
	config := federationTLSConfig("")
	config.RootCAs = x509.NewCertPool()
	config.RootCAs.AddCert(server.Certificate())
	transport := newFederationDiscoveryTransport(config)
	defer transport.CloseIdleConnections()
	client.fedHTTPClient = &http.Client{Transport: transport, Timeout: 5 * time.Second}
	result, err := client.QueryDirectory("origin.example", "#offtopic-godot:origin.example")
	require.NoError(t, err)
	require.Equal(t, "!actual:remote.example", result.RoomID)
	require.Equal(t, 1, discovered)
}

func TestFederationDiscoverySRVPreservesLogicalTLSName(t *testing.T) {
	for _, service := range []string{"_matrix-fed._tcp.", "_matrix._tcp."} {
		t.Run(service, func(t *testing.T) {
			conn, err := net.ListenPacket("udp", "127.0.0.1:0")
			require.NoError(t, err)
			defer conn.Close()
			go func() {
				packet := make([]byte, 2048)
				for {
					n, peer, readErr := conn.ReadFrom(packet)
					if readErr != nil {
						return
					}
					var query dnsmessage.Message
					if query.Unpack(packet[:n]) != nil {
						continue
					}
					response := dnsmessage.Message{Header: dnsmessage.Header{ID: query.Header.ID, Response: true, RecursionDesired: true, RecursionAvailable: true}, Questions: query.Questions}
					if len(query.Questions) > 0 && query.Questions[0].Name.String() == service+"origin.example." {
						response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeSRV, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.SRVResource{Port: 9448, Target: dnsmessage.MustNewName("dial-target.example.")}}}
					} else {
						response.RCode = dnsmessage.RCodeNameError
					}
					wire, packErr := response.Pack()
					if packErr == nil {
						_, _ = conn.WriteTo(wire, peer)
					}
				}
			}()
			previousResolver, previousTransport := net.DefaultResolver, http.DefaultTransport
			t.Cleanup(func() { net.DefaultResolver = previousResolver; http.DefaultTransport = previousTransport })
			net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "udp", conn.LocalAddr().String())
			}}
			http.DefaultTransport = keyTestTransport(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
			})
			transport := newFederationDiscoveryTransport(federationTLSConfig(""))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			results, err := transport.destinations(ctx, "origin.example")
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Equal(t, "dial-target.example:9448", results[0].Destination)
			require.Equal(t, "origin.example", results[0].TLSServerName)
			require.Equal(t, spec.ServerName("origin.example"), results[0].Host)
		})
	}
}
