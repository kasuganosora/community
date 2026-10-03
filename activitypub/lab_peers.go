package activitypub

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ACTIVITYPUB_LAB_PEERS is an explicit, process-local exception for isolated
// federation interoperability labs. Example:
// misskey.federation.test=172.30.50.10,synapse.federation.test=172.30.50.10
// It never disables TLS validation or permits arbitrary private destinations.
const labPeersEnv = "ACTIVITYPUB_LAB_PEERS"

func parseLabPeers(value string) (map[string]net.IP, error) {
	peers := make(map[string]net.IP)
	if strings.TrimSpace(value) == "" {
		return peers, nil
	}
	for _, entry := range strings.Split(value, ",") {
		parts := strings.Split(strings.TrimSpace(entry), "=")
		if len(parts) != 2 {
			return nil, fmt.Errorf("ssrf: invalid %s entry", labPeersEnv)
		}
		host := strings.ToLower(strings.TrimSpace(parts[0]))
		ip := net.ParseIP(strings.TrimSpace(parts[1]))
		if !strings.HasSuffix(host, ".test") || len(host) > 253 || net.ParseIP(host) != nil {
			return nil, fmt.Errorf("ssrf: lab peers must use exact .test hostnames")
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, fmt.Errorf("ssrf: invalid lab hostname")
			}
			for _, char := range label {
				if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
					return nil, fmt.Errorf("ssrf: invalid lab hostname")
				}
			}
		}
		if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
			return nil, fmt.Errorf("ssrf: lab peer must pin one RFC1918 IPv4 address")
		}
		if _, duplicate := peers[host]; duplicate {
			return nil, fmt.Errorf("ssrf: duplicate lab hostname")
		}
		peers[host] = ip.To4()
	}
	return peers, nil
}
func configuredLabPeers() (map[string]net.IP, error) { return parseLabPeers(os.Getenv(labPeersEnv)) }
func validateLabURL(u *url.URL) error {
	peers, err := configuredLabPeers()
	if err != nil {
		return err
	}
	if _, allowed := peers[strings.ToLower(u.Hostname())]; allowed {
		if u.Scheme != "https" || u.User != nil {
			return fmt.Errorf("ssrf: lab peers require HTTPS without URL credentials")
		}
		port := u.Port()
		if port != "" && port != "443" && port != "8448" {
			return fmt.Errorf("ssrf: lab peer port must be 443 or 8448")
		}
	}
	return nil
}

// selectDialIP validates all resolver answers and returns an already-checked
// literal IP. The dialer never resolves the hostname a second time.
func selectDialIP(host, port string, ips []net.IPAddr, peers map[string]net.IP) (net.IP, error) {
	if len(ips) == 0 {
		return nil, fmt.Errorf("ssrf: no addresses resolved for %q", host)
	}
	if pinned, allowed := peers[strings.ToLower(host)]; allowed {
		if port != "443" && port != "8448" {
			return nil, fmt.Errorf("ssrf: blocked lab peer port")
		}
		for _, answer := range ips {
			if answer.Zone != "" || !answer.IP.Equal(pinned) {
				return nil, fmt.Errorf("ssrf: lab DNS answer does not match pinned address for %q", host)
			}
		}
		return pinned, nil
	}
	for _, answer := range ips {
		if isBannedIP(answer.IP) {
			return nil, fmt.Errorf("ssrf: blocked connection to internal address %s (%s)", answer.IP, host)
		}
	}
	return ips[0].IP, nil
}

// The request-level guard also applies to redirects and WebFinger's direct Do
// calls, so an allowed TCP port can never turn into clear-text HTTP transport.
type labCheckedTransport struct{ next http.RoundTripper }

func (t labCheckedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := validateLabURL(request.URL); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(request)
}
