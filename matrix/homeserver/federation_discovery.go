package homeserver

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/log"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// Discovery cache policy.
//
//   - A good resolution lives for the .well-known Cache-Control max-age,
//     clamped to [discoveryMinTTL, discoveryMaxTTL] (default discoveryDefaultTTL).
//   - A definite "no delegation" answer (404, bad JSON, no SRV) is remembered
//     for discoveryNoDelegationTTL, so a server that stops delegating is noticed.
//   - A .well-known fetch that failed at the network level (timeout, refused)
//     says nothing about the delegation. If an earlier good resolution exists it
//     is reused (stale-if-error); otherwise the plain-8448 fallback is used but
//     only remembered for discoveryTransientTTL. Our own cancelled/expired
//     context is never remembered at all.
//   - A name that cannot be resolved at all is remembered for discoveryFailureTTL.
const (
	discoveryFailureTTL      = time.Minute
	discoveryMinTTL          = 5 * time.Minute
	discoveryDefaultTTL      = time.Hour
	discoveryMaxTTL          = 24 * time.Hour
	discoveryNoDelegationTTL = 15 * time.Minute
	discoveryTransientTTL    = time.Minute
	// A well-known lookup gets its own bound so it cannot eat a whole
	// make_join budget before the real request even starts.
	wellKnownTimeout    = 8 * time.Second
	wellKnownMaxSize    = 50 * 1024
	wellKnownRedirects  = 5
	discoveryMaxEntries = 256
)

type federationResolutionFailure struct {
	err     error
	expires time.Time
}

type federationResolution struct {
	results []fclient.ResolutionResult
	expires time.Time
	source  string
}

// federationLookup is the outcome of one discovery run.
type federationLookup struct {
	results   []fclient.ResolutionResult
	ttl       time.Duration
	source    string // ip-literal, explicit-port, well-known, srv, fallback-8448, ...
	transient bool   // well-known failed at the network level; the answer is a guess
}

type discoveryFlight struct {
	done   chan struct{}
	lookup federationLookup
	err    error
}

// Resolve logical Matrix server names without changing the destination covered
// by the request signature. In particular, an SRV target is a dial address, not
// the TLS identity: certificates must identify the logical/delegated hostname.
// Media uses its own transport but shares these resolutions.
type federationDiscoveryTransport struct {
	tlsConfig *tls.Config
	// resolve, when set (tests), replaces the built-in discovery.
	resolve func(context.Context, spec.ServerName) ([]fclient.ResolutionResult, error)
	// wellKnown fetches /.well-known/matrix/server. Production uses an
	// SSRF-checked client; the default for bare transports follows
	// http.DefaultTransport.
	wellKnown *http.Client
	// safe makes every outbound connection (well-known and federation) go
	// through the SSRF-checked dialer.
	safe        bool
	mu          sync.Mutex
	resolutions map[spec.ServerName]federationResolution
	stale       map[spec.ServerName]federationResolution
	failures    map[spec.ServerName]federationResolutionFailure
	flights     map[spec.ServerName]*discoveryFlight
	transports  map[string]http.RoundTripper
}

func newFederationDiscoveryTransport(tlsConfig *tls.Config) *federationDiscoveryTransport {
	return &federationDiscoveryTransport{
		tlsConfig:   tlsConfig,
		wellKnown:   &http.Client{CheckRedirect: wellKnownRedirectPolicy},
		resolutions: make(map[spec.ServerName]federationResolution),
		stale:       make(map[spec.ServerName]federationResolution),
		failures:    make(map[spec.ServerName]federationResolutionFailure),
		flights:     make(map[spec.ServerName]*discoveryFlight),
		transports:  make(map[string]http.RoundTripper),
	}
}

// newSafeFederationDiscoveryTransport is the production transport: .well-known
// lookups and every federation connection are dial-time checked against
// private/loopback/link-local ranges (activitypub lab-peer exceptions only).
func newSafeFederationDiscoveryTransport(tlsConfig *tls.Config) *federationDiscoveryTransport {
	t := newFederationDiscoveryTransport(tlsConfig)
	t.safe = true
	wk := tlsConfig.Clone()
	t.wellKnown = &http.Client{Transport: activitypub.NewSafeRoundTripper(wk, wellKnownTimeout), CheckRedirect: wellKnownRedirectPolicy}
	return t
}

func wellKnownRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) > wellKnownRedirects || req.URL.Scheme != "https" {
		return http.ErrUseLastResponse
	}
	return nil
}

func (t *federationDiscoveryTransport) cached(server spec.ServerName) ([]fclient.ResolutionResult, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok := t.resolutions[server]; ok && time.Now().Before(c.expires) {
		return c.results, true
	}
	return nil, false
}

// delegatedFrom reports whether host is the dial/TLS target that some other
// server name was recently delegated to (e.g. matrix-federation.matrix.org for
// matrix.org). Such a host is an implementation detail of that server, not a
// federation server name of its own, and usually does not answer on :8448.
func (t *federationDiscoveryTransport) delegatedFrom(host string) (spec.ServerName, bool) {
	host = strings.ToLower(host)
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for name, c := range t.resolutions {
		if now.After(c.expires) || strings.EqualFold(string(name), host) {
			continue
		}
		for _, r := range c.results {
			h := r.Destination
			if hh, _, err := net.SplitHostPort(h); err == nil {
				h = hh
			}
			if strings.EqualFold(h, host) && !strings.EqualFold(string(r.Host), string(name)) {
				if strings.EqualFold(strings.TrimSuffix(string(r.Host), ":443"), host) || strings.EqualFold(r.TLSServerName, host) {
					return name, true
				}
			}
		}
	}
	return "", false
}

func (t *federationDiscoveryTransport) destinations(ctx context.Context, server spec.ServerName) ([]fclient.ResolutionResult, error) {
	if results, ok := t.cached(server); ok {
		return results, nil
	}
	t.mu.Lock()
	if failed, bad := t.failures[server]; bad && time.Now().Before(failed.expires) {
		t.mu.Unlock()
		return nil, failed.err
	}
	flight, running := t.flights[server]
	if !running {
		flight = &discoveryFlight{done: make(chan struct{})}
		t.flights[server] = flight
	}
	t.mu.Unlock()
	if !running {
		// The lookup is shared by every waiter, so it must not die with the
		// first caller's context; each waiter still honours its own.
		go t.runFlight(context.WithoutCancel(ctx), server, flight)
	}
	select {
	case <-flight.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if flight.err != nil {
		return nil, flight.err
	}
	return flight.lookup.results, nil
}

func (t *federationDiscoveryTransport) runFlight(parent context.Context, server spec.ServerName, flight *discoveryFlight) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	lookup, err := t.lookup(ctx, server)
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.flights, server)
	defer close(flight.done)
	if err == nil && len(lookup.results) == 0 {
		err = fmt.Errorf("no federation address for %s", server)
	}
	if err != nil {
		flight.err = err
		// Do not remember our own timeout as a property of the server.
		if ctx.Err() == nil {
			if len(t.failures) >= discoveryMaxEntries {
				clear(t.failures)
			}
			t.failures[server] = federationResolutionFailure{err: err, expires: time.Now().Add(discoveryFailureTTL)}
		}
		return
	}
	flight.lookup = lookup
	if lookup.transient {
		if old, ok := t.stale[server]; ok {
			// Stale-if-error: keep using the last delegation we trusted.
			lookup.results, lookup.source = old.results, old.source+" (stale)"
			flight.lookup = lookup
		}
	}
	if len(t.resolutions) >= discoveryMaxEntries {
		clear(t.resolutions)
	}
	if len(t.stale) >= discoveryMaxEntries {
		clear(t.stale)
	}
	entry := federationResolution{results: lookup.results, expires: time.Now().Add(lookup.ttl), source: lookup.source}
	t.resolutions[server] = entry
	if !lookup.transient {
		t.stale[server] = entry
	}
	log.Info("matrix federation discovery: %s -> %s (source=%s, cache=%s)", server, describeResolution(lookup.results), lookup.source, lookup.ttl.Round(time.Second))
}

func describeResolution(results []fclient.ResolutionResult) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		parts = append(parts, fmt.Sprintf("%s [Host=%s tls=%s]", r.Destination, r.Host, r.TLSServerName))
	}
	return strings.Join(parts, ", ")
}

// lookup runs the Matrix server-name resolution algorithm.
func (t *federationDiscoveryTransport) lookup(ctx context.Context, server spec.ServerName) (federationLookup, error) {
	if t.resolve != nil {
		results, err := t.resolve(ctx, server)
		return federationLookup{results: results, ttl: 5 * time.Minute, source: "custom"}, err
	}
	host, port, valid := spec.ParseAndValidateServerName(server)
	if !valid {
		return federationLookup{}, errors.New("invalid server name")
	}
	bare := host
	if bare[0] == '[' && bare[len(bare)-1] == ']' {
		bare = bare[1 : len(bare)-1]
	}
	if net.ParseIP(bare) != nil {
		dest := string(server)
		if port == -1 {
			dest = net.JoinHostPort(bare, "8448")
		}
		return federationLookup{results: []fclient.ResolutionResult{{Destination: dest, Host: server, TLSServerName: bare}}, ttl: discoveryMaxTTL, source: "ip-literal"}, nil
	}
	if port != -1 {
		return federationLookup{results: []fclient.ResolutionResult{{Destination: string(server), Host: server, TLSServerName: host}}, ttl: discoveryMaxTTL, source: "explicit-port"}, nil
	}
	wk, wkErr := t.fetchWellKnown(ctx, server)
	if ctx.Err() != nil {
		return federationLookup{}, ctx.Err()
	}
	if wkErr == nil {
		target := wk.target
		tHost, tPort, tValid := spec.ParseAndValidateServerName(target)
		if tValid {
			tBare := tHost
			if tBare[0] == '[' && tBare[len(tBare)-1] == ']' {
				tBare = tBare[1 : len(tBare)-1]
			}
			ttl := wk.ttl
			switch {
			case net.ParseIP(tBare) != nil:
				dest := string(target)
				if tPort == -1 {
					dest = net.JoinHostPort(tBare, "8448")
				}
				return federationLookup{results: []fclient.ResolutionResult{{Destination: dest, Host: target, TLSServerName: tBare}}, ttl: ttl, source: "well-known"}, nil
			case tPort != -1:
				return federationLookup{results: []fclient.ResolutionResult{{Destination: string(target), Host: target, TLSServerName: tHost}}, ttl: ttl, source: "well-known"}, nil
			}
			// Delegated hostname without a port: SRV for it, else 8448.
			if recs := lookupFederationSRV(ctx, target); len(recs) > 0 {
				out := make([]fclient.ResolutionResult, 0, len(recs))
				for _, r := range recs {
					out = append(out, fclient.ResolutionResult{Destination: r, Host: target, TLSServerName: string(target)})
				}
				return federationLookup{results: out, ttl: ttl, source: "well-known+srv"}, nil
			}
			if ctx.Err() != nil {
				return federationLookup{}, ctx.Err()
			}
			return federationLookup{results: []fclient.ResolutionResult{{Destination: net.JoinHostPort(string(target), "8448"), Host: target, TLSServerName: string(target)}}, ttl: ttl, source: "well-known"}, nil
		}
		wkErr = errors.New("well-known m.server is not a valid server name")
	}
	transient := isTransientWellKnownError(wkErr)
	if recs := lookupFederationSRV(ctx, server); len(recs) > 0 {
		out := make([]fclient.ResolutionResult, 0, len(recs))
		for _, r := range recs {
			out = append(out, fclient.ResolutionResult{Destination: r, Host: server, TLSServerName: string(server)})
		}
		return federationLookup{results: out, ttl: discoveryNoDelegationTTL, source: "srv", transient: transient}, nil
	}
	if ctx.Err() != nil {
		return federationLookup{}, ctx.Err()
	}
	ttl := discoveryNoDelegationTTL
	if transient {
		ttl = discoveryTransientTTL
	}
	return federationLookup{results: []fclient.ResolutionResult{{Destination: net.JoinHostPort(string(server), "8448"), Host: server, TLSServerName: string(server)}}, ttl: ttl, source: "fallback-8448", transient: transient}, nil
}

type wellKnownAnswer struct {
	target spec.ServerName
	ttl    time.Duration
}

var errNoWellKnownDelegation = errors.New("no .well-known delegation")

// fetchWellKnown GETs https://<server>/.well-known/matrix/server through the
// SSRF-checked client with its own deadline.
func (t *federationDiscoveryTransport) fetchWellKnown(ctx context.Context, server spec.ServerName) (*wellKnownAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, wellKnownTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+strings.TrimRight(string(server), "/")+"/.well-known/matrix/server", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Community-Matrix/1.0")
	resp, err := t.wellKnown.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", errNoWellKnownDelegation, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, wellKnownMaxSize))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Server spec.ServerName `json:"m.server"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.Server == "" {
		return nil, fmt.Errorf("%w: unusable body", errNoWellKnownDelegation)
	}
	return &wellKnownAnswer{target: doc.Server, ttl: wellKnownTTL(resp.Header)}, nil
}

// wellKnownTTL honours Cache-Control max-age (preferred) or Expires, clamped.
func wellKnownTTL(h http.Header) time.Duration {
	ttl := discoveryDefaultTTL
	if exp := h.Get("Expires"); exp != "" {
		if when, err := http.ParseTime(exp); err == nil {
			ttl = time.Until(when)
		}
	}
	for _, part := range strings.Split(h.Get("Cache-Control"), ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], "max-age") {
			if secs, err := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64); err == nil {
				ttl = time.Duration(secs) * time.Second
			}
		}
	}
	return min(max(ttl, discoveryMinTTL), discoveryMaxTTL)
}

// isTransientWellKnownError: the lookup itself did not get a definite answer.
func isTransientWellKnownError(err error) bool {
	if err == nil || errors.Is(err, errNoWellKnownDelegation) {
		return false
	}
	return true
}

// lookupFederationSRV returns "host:port" dial targets for the matrix-fed
// service (Matrix 1.8) and the deprecated matrix service.
func lookupFederationSRV(ctx context.Context, name spec.ServerName) []string {
	for _, service := range []string{"matrix-fed", "matrix"} {
		_, records, err := net.DefaultResolver.LookupSRV(ctx, service, "tcp", string(name))
		// Resolver trouble is not fatal: the next service, then :8448.
		if err != nil || len(records) == 0 {
			continue
		}
		out := make([]string, 0, len(records))
		for _, rec := range records {
			target := strings.TrimSuffix(rec.Target, ".")
			if target == "" {
				continue
			}
			out = append(out, net.JoinHostPort(target, strconv.Itoa(int(rec.Port))))
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func (t *federationDiscoveryTransport) transport(tlsName string) http.RoundTripper {
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing := t.transports[tlsName]; existing != nil {
		return existing
	}
	if len(t.transports) >= discoveryMaxEntries {
		for _, transport := range t.transports {
			if c, ok := transport.(interface{ CloseIdleConnections() }); ok {
				c.CloseIdleConnections()
			}
		}
		clear(t.transports)
	}
	config := t.tlsConfig.Clone()
	config.ServerName = tlsName
	var rt http.RoundTripper
	if t.safe {
		// Dial-time validation of every DNS answer; no separate dial timeout
		// beyond the request context and the dialer's own 15s.
		rt = activitypub.NewSafeRoundTripper(config, 0)
	} else {
		rt = &http.Transport{TLSClientConfig: config, MaxIdleConns: 100, MaxIdleConnsPerHost: 20, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ForceAttemptHTTP2: true}
	}
	t.transports[tlsName] = rt
	return rt
}

// federationDialAttempt is one destination tried for a logical server.
type federationDialAttempt struct {
	Destination string
	TLSName     string
	Class       string
	Err         error
}

// federationDialError says which addresses a request to a logical server name
// was actually sent to and how each failed. It never contains URLs with
// query strings, request bodies or credentials.
type federationDialError struct {
	Server   string
	Attempts []federationDialAttempt
}

func (e *federationDialError) Error() string {
	parts := make([]string, 0, len(e.Attempts))
	for _, a := range e.Attempts {
		parts = append(parts, fmt.Sprintf("%s (tls=%s): %s: %s", a.Destination, a.TLSName, a.Class, shortErr(a.Err)))
	}
	return fmt.Sprintf("server %s unreachable: %s", e.Server, strings.Join(parts, "; "))
}

func (e *federationDialError) Unwrap() error {
	if len(e.Attempts) == 0 {
		return nil
	}
	return e.Attempts[len(e.Attempts)-1].Err
}

func shortErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return msg
}

// classifyFederationError gives a stable, low-cardinality label for logs.
func classifyFederationError(err error) string {
	var dns *net.DNSError
	switch {
	case err == nil:
		return "none"
	case strings.Contains(err.Error(), "ssrf:"):
		return "blocked-address"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.As(err, &dns):
		return "dns"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "reset"
	case strings.Contains(err.Error(), "x509:"), strings.Contains(err.Error(), "tls:"):
		return "tls"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	return "network"
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
		return nil, &federationDialError{Server: server, Attempts: []federationDialAttempt{{Destination: "(discovery)", TLSName: "-", Class: classifyFederationError(err), Err: err}}}
	}
	if req.Body != nil && req.GetBody != nil {
		defer req.Body.Close()
	}
	var attempts []federationDialAttempt
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
		attempts = append(attempts, federationDialAttempt{Destination: result.Destination, TLSName: result.TLSServerName, Class: classifyFederationError(requestErr), Err: requestErr})
		if req.Context().Err() != nil {
			break
		}
	}
	if req.Context().Err() == nil {
		// Every destination failed on its own merits: re-resolve next time.
		t.mu.Lock()
		delete(t.resolutions, spec.ServerName(server))
		t.mu.Unlock()
	}
	return nil, &federationDialError{Server: server, Attempts: attempts}
}

func (t *federationDiscoveryTransport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, transport := range t.transports {
		if c, ok := transport.(interface{ CloseIdleConnections() }); ok {
			c.CloseIdleConnections()
		}
	}
}

var (
	sharedDiscovery     atomic.Pointer[federationDiscoveryTransport]
	sharedDiscoveryOnce sync.Once
)

// peekSharedDiscovery returns the shared transport if it exists yet.
func peekSharedDiscovery() *federationDiscoveryTransport { return sharedDiscovery.Load() }

// sharedFederationDiscoveryTransport returns the process-wide transport so
// resolutions (.well-known, SRV) and connection pools survive across the
// short-lived FederationClient values.
func sharedFederationDiscoveryTransport(tlsConfig *tls.Config) *federationDiscoveryTransport {
	sharedDiscoveryOnce.Do(func() {
		if os.Getenv("MATRIX_FEDERATION_UNSAFE_DIAL") == "1" {
			// Escape hatch for closed labs whose peers live on private ranges
			// without being listed in the lab-peer allowlist.
			sharedDiscovery.Store(newFederationDiscoveryTransport(tlsConfig))
			return
		}
		sharedDiscovery.Store(newSafeFederationDiscoveryTransport(tlsConfig))
	})
	return sharedDiscovery.Load()
}
