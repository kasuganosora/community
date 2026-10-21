package activitypub

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// maxResponseSize limits the size of HTTP response bodies to prevent OOM attacks.
const maxResponseSize = 10 * 1024 * 1024 // 10 MB

// ssrfBannedNetworks returns the list of CIDR ranges that must never be
// contacted during federation. This includes loopback, private, link-local,
// and other reserved address ranges.
var ssrfBannedNetworks = func() []*net.IPNet {
	cidrs := []string{
		// IPv4
		"0.0.0.0/8",          // "This host" network
		"10.0.0.0/8",         // Private
		"100.64.0.0/10",      // CGNAT
		"127.0.0.0/8",        // Loopback
		"169.254.0.0/16",     // Link-local (includes AWS metadata 169.254.169.254)
		"172.16.0.0/12",      // Private
		"192.0.0.0/24",       // IETF Protocol Assignments
		"192.0.2.0/24",       // Documentation (TEST-NET-1)
		"192.88.99.0/24",     // 6to4 anycast (deprecated)
		"192.168.0.0/16",     // Private
		"198.18.0.0/15",      // Benchmark testing
		"198.51.100.0/24",    // Documentation (TEST-NET-2)
		"203.0.113.0/24",     // Documentation (TEST-NET-3)
		"224.0.0.0/4",        // Multicast
		"240.0.0.0/4",        // Reserved
		"255.255.255.255/32", // Broadcast
		// IPv6 (note: IPv4-mapped IPv6 addresses are handled via To4())
		"::1/128",      // Loopback
		"::/128",       // Unspecified
		"64:ff9b::/96", // NAT64
		"100::/64",     // Discard prefix
		"fc00::/7",     // Unique local
		"fe80::/10",    // Link-local
		"fec0::/10",    // Site-local (deprecated)
		"ff00::/8",     // Multicast
	}

	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, ipNet, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("invalid SSRF CIDR %q: %v", c, err))
		}
		nets = append(nets, ipNet)
	}
	return nets
}()

// isBannedIP returns true if the IP address falls within a banned network range.
// For IPv4-mapped IPv6 addresses (::ffff:a.b.c.d), the IPv4 portion is checked
// against the IPv4 ban list to prevent bypassing filters via IPv6 representation.
// IsBannedIP reports whether ip is in the SSRF banned network list
// (loopback, private, link-local, CGNAT, benchmarking, NAT64, reserved, ...).
func IsBannedIP(ip net.IP) bool { return isBannedIP(ip) }

func isBannedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Convert to 4-byte representation if this is an IPv4 or IPv4-mapped IPv6 address.
	// This is critical: net.ParseIP("127.0.0.1") returns a 16-byte slice that
	// is an IPv4-mapped IPv6 address (::ffff:127.0.0.1). By calling To4(), we get
	// the canonical 4-byte form, and net.IPNet.Contains handles the matching
	// correctly for IPv4 CIDR ranges.
	if ip4 := ip.To4(); ip4 != nil {
		for _, n := range ssrfBannedNetworks {
			if n.Contains(ip4) {
				return true
			}
		}
		return false
	}
	// Pure IPv6 address
	for _, n := range ssrfBannedNetworks {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ssrfSafeDialContext is a net.Dialer-compatible function that rejects
// connections to internal/private/reserved IP addresses. It resolves the
// hostname and checks every resolved address before dialing.
func ssrfSafeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("ssrf: invalid address %q: %w", addr, err)
	}

	peers, err := configuredLabPeers()
	if err != nil {
		return nil, err
	}
	var ips []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IPAddr{{IP: ip}}
	} else {
		ips, err = (&net.Resolver{}).LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("ssrf: failed to resolve %q: %w", host, err)
		}
	}
	pinned, err := selectDialIP(host, port, ips, peers)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(pinned.String(), port))
}

// ssrfSafeTransport is an http.Transport whose dialer rejects internal addresses.
var ssrfSafeTransport = &http.Transport{
	DialContext:           ssrfSafeDialContext,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   20,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	ForceAttemptHTTP2:     false,
}

// ssrfSafeHTTPClient is the SSRF-protected HTTP client used for all outbound
// federation requests (fetching actors, objects, WebFinger, etc).
var ssrfSafeHTTPClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: labCheckedTransport{next: ssrfSafeTransport},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("ssrf: stopped after 5 redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("ssrf: blocked redirect to non-HTTP scheme %q", req.URL.Scheme)
		}
		return nil
	},
}

// ssrfHTTPClientForTesting returns an HTTP client whose dialer bypasses the
// loopback ban. This is intended ONLY for unit tests that use httptest servers
// bound to 127.0.0.1. Production code must never call this.
func ssrfHTTPClientForTesting() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				dialer := &net.Dialer{
					Timeout:   15 * time.Second,
					KeepAlive: 30 * time.Second,
				}
				return dialer.DialContext(ctx, network, addr)
			},
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// ValidateURI validates that a URI is safe for outbound federation requests.
// It ensures the scheme is HTTP/HTTPS and the host does not resolve to an
// internal address. This is used as a pre-check before making requests and
// can be unit-tested without a real network connection.
func ValidateURI(uri string) error {
	u, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("ssrf: invalid URI %q: %w", uri, err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("ssrf: non-HTTP scheme %q", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("ssrf: empty host in URI %q", uri)
	}

	if err := validateLabURL(u); err != nil {
		return err
	}
	peers, err := configuredLabPeers()
	if err != nil {
		return err
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	var ips []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IPAddr{{IP: ip}}
	} else {
		resolver := net.Resolver{}
		ips, err = resolver.LookupIPAddr(context.Background(), host)
		if err != nil {
			return fmt.Errorf("ssrf: failed to resolve %q: %w", host, err)
		}
	}
	_, err = selectDialIP(host, port, ips, peers)
	if err != nil {
		return err
	}
	return nil
}

// IsSSRFBlocked returns true if the given error indicates an SSRF protection
// block. This is useful for testing and logging.
func IsSSRFBlocked(err error) bool {
	if err == nil {
		return false
	}
	return containsSubstr(err.Error(), "ssrf:")
}

// containsSubstr checks if s contains the given substring anywhere.
func containsSubstr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// NewSafeHTTPClient returns an SSRF-protected client for fetching arbitrary
// public web pages (link previews): connections are pinned to a resolved
// public address (also across redirects), at most maxRedirects redirects
// are followed, only http(s) is allowed and the whole request is bounded
// by timeout.
func NewSafeHTTPClient(timeout time.Duration, maxRedirects int) *http.Client {
	return newSafeHTTPClient(timeout, maxRedirects, ssrfSafeHTTPClient.Transport)
}

// connectBoundTransports caches one transport per connect timeout so
// connection pooling still works across clients.
var connectBoundTransports sync.Map // time.Duration -> http.RoundTripper

// NewSafeHTTPClientWithConnectTimeout is NewSafeHTTPClient whose TCP connect
// (DNS + dial) gives up after connect, for user-facing fetches that must not
// hang on unreachable hosts.
func NewSafeHTTPClientWithConnectTimeout(timeout time.Duration, maxRedirects int, connect time.Duration) *http.Client {
	rt, ok := connectBoundTransports.Load(connect)
	if !ok {
		transport := ssrfSafeTransport.Clone()
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, connect)
			defer cancel()
			return ssrfSafeDialContext(ctx, network, addr)
		}
		transport.TLSHandshakeTimeout = connect
		rt, _ = connectBoundTransports.LoadOrStore(connect, http.RoundTripper(labCheckedTransport{next: transport}))
	}
	return newSafeHTTPClient(timeout, maxRedirects, rt.(http.RoundTripper))
}

func newSafeHTTPClient(timeout time.Duration, maxRedirects int, transport http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("ssrf: stopped after %d redirects", maxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("ssrf: blocked redirect to non-HTTP scheme %q", req.URL.Scheme)
			}
			return validateLabURL(req.URL)
		},
	}
}
