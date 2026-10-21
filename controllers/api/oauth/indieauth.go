package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/config"
	"github.com/pkg/errors"
)

// ClientInformation is the IndieAuth / Misskey OAuth client metadata document.
type ClientInformation struct {
	ID           string
	Name         string
	Logo         string
	RedirectURIs []string
}

type cachedClient struct {
	info    ClientInformation
	expires time.Time
}

var (
	clientMu    sync.Mutex
	clientCache = map[string]cachedClient{}

	// discoverClientInfo is replaced in tests.
	discoverClientInfo = fetchClientInformation
)

func isIndieAuthClientID(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.Hostname() == "" {
		return false
	}

	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}

	host := u.Hostname()
	switch u.Scheme {
	case "https":
	case "http":
		if !allowLoopbackClients() || !isLoopback(host) {
			return false
		}
	default:
		return false
	}

	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() && allowLoopbackClients()
	}

	return true
}

func allowLoopbackClients() bool {
	return config.C.DevMode == "true"
}

func lookupClientInfo(ctx context.Context, clientID string) (ClientInformation, error) {
	clientMu.Lock()
	if hit, ok := clientCache[clientID]; ok && hit.expires.After(time.Now()) {
		clientMu.Unlock()

		return hit.info, nil
	}
	clientMu.Unlock()

	info, err := discoverClientInfo(ctx, clientID)
	if err != nil {
		return ClientInformation{}, err
	}

	clientMu.Lock()
	clientCache[clientID] = cachedClient{info: info, expires: time.Now().Add(10 * time.Minute)}
	clientMu.Unlock()

	return info, nil
}

func fetchClientInformation(ctx context.Context, clientID string) (ClientInformation, error) {
	if !isIndieAuthClientID(clientID) {
		return ClientInformation{}, errors.New("invalid client_id")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return ClientInformation{}, err
	}

	req.Header.Set("Accept", "application/json, text/html;q=0.5")

	resp, err := indieHTTP.Do(req)
	if err != nil {
		return ClientInformation{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ClientInformation{}, errors.Errorf("client metadata status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return ClientInformation{}, err
	}

	info := ClientInformation{ID: clientID, Name: clientID}
	ct := resp.Header.Get("Content-Type")

	if strings.Contains(ct, "json") || (len(body) > 0 && body[0] == '{') {
		var doc struct {
			ClientID     string   `json:"client_id"`
			ClientName   string   `json:"client_name"`
			LogoURI      string   `json:"logo_uri"`
			RedirectURIs []string `json:"redirect_uris"`
		}

		if err = json.Unmarshal(body, &doc); err != nil {
			return ClientInformation{}, err
		}

		if doc.ClientID != "" && doc.ClientID != clientID {
			return ClientInformation{}, errors.New("client_id mismatch")
		}

		if doc.ClientName != "" {
			info.Name = doc.ClientName
		}

		info.Logo = doc.LogoURI
		info.RedirectURIs = doc.RedirectURIs
	}

	for _, link := range parseLinkHeader(resp.Header.Values("Link")) {
		if link.rel == "redirect_uri" && link.href != "" {
			info.RedirectURIs = append(info.RedirectURIs, resolveRef(clientID, link.href))
		}
	}

	filtered := info.RedirectURIs[:0]
	for _, uri := range info.RedirectURIs {
		if isAllowedRedirect(uri) {
			filtered = append(filtered, uri)
		}
	}

	info.RedirectURIs = filtered
	if len(info.RedirectURIs) == 0 {
		return ClientInformation{}, errors.New("client has no redirect_uris")
	}

	return info, nil
}

type linkItem struct {
	href string
	rel  string
}

func parseLinkHeader(values []string) []linkItem {
	var out []linkItem

	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if !strings.HasPrefix(part, "<") {
				continue
			}

			end := strings.IndexByte(part, '>')
			if end < 0 {
				continue
			}

			item := linkItem{href: part[1:end]}
			for _, param := range strings.Split(part[end+1:], ";") {
				param = strings.TrimSpace(param)
				k, v, ok := strings.Cut(param, "=")
				if ok && strings.EqualFold(k, "rel") {
					item.rel = strings.Trim(v, `"`)
				}
			}

			out = append(out, item)
		}
	}

	return out
}

func resolveRef(base, ref string) string {
	bu, err := url.Parse(base)
	if err != nil {
		return ref
	}

	ru, err := url.Parse(ref)
	if err != nil {
		return ref
	}

	return bu.ResolveReference(ru).String()
}

var indieHTTP = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}

		if !isIndieAuthClientID(req.URL.String()) {
			return errors.New("redirect blocked")
		}

		if len(via) > 0 && !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
			return errors.New("redirect host changed")
		}

		return nil
	},
	Transport: &http.Transport{
		Proxy: nil, // never follow HTTP_PROXY into an internal network
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}

			if len(ips) == 0 {
				return nil, errors.New("no address")
			}

			for _, ip := range ips {
				if !isDialable(ip.IP) {
					return nil, errors.New("blocked address")
				}
			}

			d := &net.Dialer{Timeout: 4 * time.Second}

			return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	},
}

func isLoopback(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")

	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func isDialable(ip net.IP) bool {
	if ip == nil {
		return false
	}

	if ip.IsLoopback() {
		return allowLoopbackClients()
	}

	// Enforce the shared SSRF banned-CIDR list (private, CGNAT, benchmark,
	// NAT64, reserved, ...) so all outbound fetchers stay consistent.
	if activitypub.IsBannedIP(ip) {
		return false
	}

	return true
}
