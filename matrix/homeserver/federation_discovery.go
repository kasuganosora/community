package homeserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

type federationResolution struct {
	results []fclient.ResolutionResult
	expires time.Time
}

// Resolve logical Matrix server names without changing the destination covered
// by the request signature. In particular, an SRV target is a dial address, not
// the TLS identity: certificates must identify the logical/delegated hostname.
// Media uses its separate dial-time network boundary and never this transport.
type federationDiscoveryTransport struct {
	tlsConfig   *tls.Config
	resolve     func(context.Context, spec.ServerName) ([]fclient.ResolutionResult, error)
	mu          sync.Mutex
	resolutions map[spec.ServerName]federationResolution
	transports  map[string]*http.Transport
}

func newFederationDiscoveryTransport(tlsConfig *tls.Config) *federationDiscoveryTransport {
	return &federationDiscoveryTransport{
		tlsConfig: tlsConfig, resolve: fclient.ResolveServer,
		resolutions: make(map[spec.ServerName]federationResolution),
		transports:  make(map[string]*http.Transport),
	}
}

func (t *federationDiscoveryTransport) destinations(ctx context.Context, server spec.ServerName) ([]fclient.ResolutionResult, error) {
	t.mu.Lock()
	cached, ok := t.resolutions[server]
	t.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.results, nil
	}
	results, err := t.resolve(ctx, server)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no federation address for %s", server)
	}
	t.mu.Lock()
	// Bound caches even when clients try many distinct remote server names.
	if len(t.resolutions) >= 256 {
		clear(t.resolutions)
	}
	t.resolutions[server] = federationResolution{results: results, expires: time.Now().Add(5 * time.Minute)}
	t.mu.Unlock()
	return results, nil
}

func (t *federationDiscoveryTransport) transport(tlsName string) *http.Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing := t.transports[tlsName]; existing != nil {
		return existing
	}
	if len(t.transports) >= 256 {
		for _, transport := range t.transports {
			transport.CloseIdleConnections()
		}
		clear(t.transports)
	}
	config := t.tlsConfig.Clone()
	config.ServerName = tlsName
	transport := &http.Transport{TLSClientConfig: config, MaxIdleConns: 100, MaxIdleConnsPerHost: 20, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ForceAttemptHTTP2: true}
	t.transports[tlsName] = transport
	return transport
}

func (t *federationDiscoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	server := req.Header.Get("Host")
	if server == "" {
		server = req.URL.Host
	}
	results, err := t.destinations(req.Context(), spec.ServerName(server))
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	if req.Body != nil && req.GetBody != nil {
		defer req.Body.Close()
	}
	for index, result := range results {
		attempt := req.Clone(req.Context())
		attempt.URL.Scheme = "https"
		attempt.URL.Host = result.Destination
		attempt.Host = string(result.Host)
		attempt.Header.Del("Host")
		if req.Body != nil && req.GetBody != nil {
			attempt.Body, err = req.GetBody()
			if err != nil {
				return nil, err
			}
		} else if index > 0 && req.Body != nil {
			break
		}
		response, requestErr := t.transport(result.TLSServerName).RoundTrip(attempt)
		if requestErr == nil {
			return response, nil
		}
		err = requestErr
	}
	t.mu.Lock()
	delete(t.resolutions, spec.ServerName(server))
	t.mu.Unlock()
	return nil, err
}

func (t *federationDiscoveryTransport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, transport := range t.transports {
		transport.CloseIdleConnections()
	}
}
