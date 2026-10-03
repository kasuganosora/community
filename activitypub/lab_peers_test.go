package activitypub

import (
	"github.com/stretchr/testify/require"
	"net"
	"net/http"
	"net/url"
	"testing"
)

func TestLabPeersFailClosed(t *testing.T) {
	for _, value := range []string{"*.federation.test=172.30.50.10", "misskey.federation.test=172.30.50.0/24", "misskey.federation.test=127.0.0.1", "misskey.federation.test=169.254.169.254", "misskey.federation.test=8.8.8.8", "misskey.example.com=172.30.50.10", "172.30.50.10=172.30.50.10", "misskey.federation.test=172.30.50.10,misskey.federation.test=172.30.50.11"} {
		t.Run(value, func(t *testing.T) { _, err := parseLabPeers(value); require.Error(t, err) })
	}
}
func TestLabPeersOnlyAllowPinnedDNS(t *testing.T) {
	const host = "misskey.federation.test"
	peers, err := parseLabPeers(host + "=172.30.50.10")
	require.NoError(t, err)
	answers := func(addresses ...string) []net.IPAddr {
		result := make([]net.IPAddr, 0, len(addresses))
		for _, address := range addresses {
			result = append(result, net.IPAddr{IP: net.ParseIP(address)})
		}
		return result
	}
	for _, port := range []string{"443", "8448"} {
		pinned, err := selectDialIP(host, port, answers("172.30.50.10"), peers)
		require.NoError(t, err)
		require.True(t, pinned.Equal(net.ParseIP("172.30.50.10")))
	}
	cases := []struct {
		name, hostname, port string
		ips                  []net.IPAddr
		mapping              map[string]net.IP
	}{
		{"default disabled", host, "443", answers("172.30.50.10"), nil},
		{"different peer", "other.federation.test", "443", answers("172.30.50.10"), peers},
		{"IP literal", "172.30.50.10", "443", answers("172.30.50.10"), peers},
		{"mismatched IP", host, "443", answers("172.30.50.11"), peers},
		{"rebound private answer", host, "443", answers("172.30.50.10", "10.0.0.1"), peers},
		{"mixed public answer", host, "443", answers("172.30.50.10", "8.8.8.8"), peers},
		{"arbitrary port", host, "8080", answers("172.30.50.10"), peers},
		{"no answer", host, "443", nil, peers},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := selectDialIP(tc.hostname, tc.port, tc.ips, tc.mapping)
			require.Error(t, err)
		})
	}
}

type labTestTransport func(*http.Request) (*http.Response, error)

func (f labTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestLabPeerRequiresTLSOnEveryRequest(t *testing.T) {
	t.Setenv(labPeersEnv, "misskey.federation.test=172.30.50.10")
	called := 0
	transport := labCheckedTransport{next: labTestTransport(func(*http.Request) (*http.Response, error) { called++; return &http.Response{StatusCode: 200}, nil })}
	for _, address := range []string{"http://misskey.federation.test:443/inbox", "https://misskey.federation.test:8080/inbox", "https://user:pass@misskey.federation.test/inbox"} {
		uri, err := url.Parse(address)
		require.NoError(t, err)
		_, err = transport.RoundTrip(&http.Request{URL: uri})
		require.Error(t, err)
	}
	require.Zero(t, called)
	for _, address := range []string{"https://misskey.federation.test/inbox", "https://misskey.federation.test:8448/inbox"} {
		uri, _ := url.Parse(address)
		_, err := transport.RoundTrip(&http.Request{URL: uri})
		require.NoError(t, err)
	}
	require.Equal(t, 2, called)
}
