package homeserver

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
)

// rewriteRoundTripper sends every request to one fake server, whatever the
// URL says, keeping the URL's host as Host. It stands in for "DNS + the real
// internet" so logical names like matrix.test can be exercised.
type rewriteRoundTripper struct {
	to   *httptest.Server
	mu   sync.Mutex
	seen []string
}

func (r *rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.seen = append(r.seen, req.URL.Host+req.URL.Path)
	r.mu.Unlock()
	c := req.Clone(req.Context())
	c.Host = req.URL.Host
	c.URL.Host = strings.TrimPrefix(r.to.URL, "https://")
	return r.to.Client().Transport.RoundTrip(c)
}

type seenRequest struct{ host, auth, path string }

// delegatingFed is a fake federation server for logical name `logical` that
// publishes .well-known delegation to its real 127.0.0.1:port address.
type delegatingFed struct {
	*fakeFed
	logical string
	mu      sync.Mutex
	reqs    []seenRequest
	room    *fakeRoom
	servers []string
}

func newDelegatingFed(t *testing.T, logical string) *delegatingFed {
	t.Helper()
	d := &delegatingFed{logical: logical}
	f := &fakeFed{t: t, hits: map[string]int{}, notaryDocs: map[string]*fakeFed{}}
	d.fakeFed = f
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addr := strings.TrimPrefix(f.srv.URL, "https://")
		if r.URL.Path == "/.well-known/matrix/server" {
			f.hit("well_known")
			if r.Host != logical {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write([]byte(`{"m.server":"` + addr + `"}`))
			return
		}
		d.mu.Lock()
		d.reqs = append(d.reqs, seenRequest{host: r.Host, auth: r.Header.Get("Authorization"), path: r.URL.Path})
		d.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/query/directory") {
			f.hit("directory")
			_ = json.NewEncoder(w).Encode(map[string]any{"room_id": d.room.roomID, "servers": d.servers})
			return
		}
		f.serve(w, r)
	}))
	f.name = logical
	f.hs = minimalHS(logical)
	t.Cleanup(f.srv.Close)
	return d
}

func (d *delegatingFed) transport() (*federationDiscoveryTransport, *rewriteRoundTripper) {
	cfg := federationTLSConfig("")
	cfg.RootCAs = x509.NewCertPool()
	cfg.RootCAs.AddCert(d.srv.Certificate())
	tr := newFederationDiscoveryTransport(cfg)
	rt := &rewriteRoundTripper{to: d.srv}
	tr.wellKnown = &http.Client{Transport: rt}
	return tr, rt
}

func TestJoinAliasFollowsWellKnownDelegationForDirectoryMakeJoinSendJoin(t *testing.T) {
	d := newDelegatingFed(t, "matrix.test")
	hs := joinTestEnv(t, d.fakeFed)
	tr, wk := d.transport()
	defer tr.CloseIdleConnections()
	newFederationClient = func(h *HomeServer) *FederationClient {
		c := NewFederationClient(h)
		c.fedHTTPClient = &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		return c
	}
	d.room = newFakeRoom(t, d.fakeFed, d.fakeFed) // !room:matrix.test, signed by matrix.test
	d.room.serve(d.fakeFed)
	d.servers = []string{"matrix.test"}

	id, merr := hs.Room.JoinRoomContext(context.Background(), "@alice:test.local", "#room:matrix.test", nil)
	require.Nil(t, merr)
	require.Equal(t, d.room.roomID, id)
	require.True(t, joined(t, id, "@alice:test.local"))

	require.Equal(t, 1, d.count("directory"), "alias directory queried once, on the alias's server")
	require.Equal(t, 1, d.count("make_join"))
	require.Equal(t, 1, d.count("send_join"))
	require.Equal(t, 1, d.count("well_known"), "one .well-known lookup is shared by every request to matrix.test")
	require.Equal(t, []string{"matrix.test/.well-known/matrix/server"}, wk.seen)

	addr := strings.TrimPrefix(d.srv.URL, "https://")
	d.mu.Lock()
	defer d.mu.Unlock()
	require.NotEmpty(t, d.reqs)
	for _, r := range d.reqs {
		// Delegated Host header, logical name in the signature, never the
		// logical host:8448.
		require.Equal(t, addr, r.host, r.path)
		require.Contains(t, r.auth, `destination="matrix.test"`, r.path)
	}
}

func TestJoinCandidatesUseAliasServerThenDirectoryRoomAndVias(t *testing.T) {
	d := newDelegatingFed(t, "alias.test")
	hs := joinTestEnv(t, d.fakeFed)
	tr, _ := d.transport()
	defer tr.CloseIdleConnections()
	newFederationClient = func(h *HomeServer) *FederationClient {
		c := NewFederationClient(h)
		c.fedHTTPClient = &http.Client{Transport: tr}
		return c
	}
	d.room = &fakeRoom{roomID: "!room:room-domain.example"}
	d.servers = []string{"res1.example", "bad name", "res2.example", "alias.test", "res1.example"}
	resolved, merr := NewAliasService(hs).ResolveAliasContext(context.Background(), "#x:alias.test")
	require.Nil(t, merr)
	require.Equal(t, "!room:room-domain.example", resolved.RoomID)
	require.Equal(t, []string{"alias.test", "res1.example", "res2.example"}, resolved.Servers)
	got := orderJoinServers("me.example", resolved.RoomID, []string{"via.example"}, resolved.Servers, "")
	require.Equal(t, []string{"room-domain.example", "via.example", "alias.test", "res1.example", "res2.example"}, got)
}

func TestDiscoveryContextExpiryIsNotCachedAsFallback(t *testing.T) {
	// A well-known lookup that merely ran out of OUR time must not leave a
	// "matrix.test -> matrix.test:8448" entry behind for minutes.
	hang := true
	var mu sync.Mutex
	tr := newFederationDiscoveryTransport(federationTLSConfig(""))
	tr.wellKnown = &http.Client{Transport: keyTestTransport(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		h := hang
		mu.Unlock()
		if h {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: nopBody(`{"m.server":"matrix-federation.test:443"}`)}, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_, err := tr.destinations(ctx, "matrix.test")
	cancel()
	require.Error(t, err)
	_, cached := tr.cached("matrix.test")
	require.False(t, cached)
	require.Empty(t, tr.failures)

	// Our flight outlives the first caller, finishes with its own bound (8s),
	// and nothing wrong is remembered. A later caller with a working network
	// resolves the real delegation.
	mu.Lock()
	hang = false
	mu.Unlock()
	require.Eventually(t, func() bool {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		return len(tr.flights) == 0
	}, 12*time.Second, 20*time.Millisecond)
	tr.mu.Lock()
	clear(tr.resolutions)
	clear(tr.failures)
	tr.mu.Unlock()
	results, err := tr.destinations(context.Background(), "matrix.test")
	require.NoError(t, err)
	require.Equal(t, "matrix-federation.test:443", results[0].Destination)
	require.Equal(t, spec.ServerName("matrix-federation.test:443"), results[0].Host)
	require.Equal(t, "matrix-federation.test", results[0].TLSServerName)
}

func TestDiscoveryTransientWellKnownFailureUsesStaleDelegationAndShortFallback(t *testing.T) {
	var mu sync.Mutex
	mode := "ok"
	tr := newFederationDiscoveryTransport(federationTLSConfig(""))
	tr.wellKnown = &http.Client{Transport: keyTestTransport(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		switch mode {
		case "ok":
			h := make(http.Header)
			h.Set("Cache-Control", "max-age=1")
			return &http.Response{StatusCode: 200, Header: h, Body: nopBody(`{"m.server":"deleg.test:443"}`)}, nil
		case "404":
			return &http.Response{StatusCode: 404, Header: make(http.Header), Body: nopBody("")}, nil
		}
		return nil, errors.New("connection reset by peer")
	})}
	r, err := tr.destinations(context.Background(), "a.test")
	require.NoError(t, err)
	require.Equal(t, "deleg.test:443", r[0].Destination)
	// max-age=1 is clamped up to the minimum TTL.
	tr.mu.Lock()
	require.GreaterOrEqual(t, time.Until(tr.resolutions["a.test"].expires), discoveryMinTTL-time.Minute)
	// Force expiry.
	e := tr.resolutions["a.test"]
	e.expires = time.Now().Add(-time.Second)
	tr.resolutions["a.test"] = e
	tr.mu.Unlock()
	mu.Lock()
	mode = "net"
	mu.Unlock()
	r, err = tr.destinations(context.Background(), "a.test")
	require.NoError(t, err)
	require.Equal(t, "deleg.test:443", r[0].Destination, "network failure of the refresh keeps the last good delegation")
	tr.mu.Lock()
	require.LessOrEqual(t, time.Until(tr.resolutions["a.test"].expires), discoveryTransientTTL)
	tr.mu.Unlock()

	// A server never seen before whose well-known is unreachable gets the plain
	// fallback, but only briefly.
	r, err = tr.destinations(context.Background(), "b.test")
	require.NoError(t, err)
	require.Equal(t, "b.test:8448", r[0].Destination)
	tr.mu.Lock()
	require.LessOrEqual(t, time.Until(tr.resolutions["b.test"].expires), discoveryTransientTTL)
	tr.mu.Unlock()

	// A definite 404 is a real "no delegation" and is cached longer.
	mu.Lock()
	mode = "404"
	mu.Unlock()
	r, err = tr.destinations(context.Background(), "c.test")
	require.NoError(t, err)
	require.Equal(t, "c.test:8448", r[0].Destination)
	tr.mu.Lock()
	require.Greater(t, time.Until(tr.resolutions["c.test"].expires), discoveryTransientTTL)
	tr.mu.Unlock()
}

func TestWellKnownTTLIsClamped(t *testing.T) {
	h := func(cc string) http.Header { x := http.Header{}; x.Set("Cache-Control", cc); return x }
	require.Equal(t, discoveryDefaultTTL, wellKnownTTL(http.Header{}))
	require.Equal(t, discoveryMinTTL, wellKnownTTL(h("max-age=0")))
	require.Equal(t, 2*time.Hour, wellKnownTTL(h("public, max-age=7200")))
	require.Equal(t, discoveryMaxTTL, wellKnownTTL(h("max-age=99999999")))
}

func TestDiscoveryConcurrentCallersShareOneLookup(t *testing.T) {
	var mu sync.Mutex
	n := 0
	tr := newFederationDiscoveryTransport(federationTLSConfig(""))
	tr.wellKnown = &http.Client{Transport: keyTestTransport(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		n++
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: nopBody(`{"m.server":"d.test:443"}`)}, nil
	})}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := tr.destinations(context.Background(), "s.test")
			require.NoError(t, err)
		}()
	}
	wg.Wait()
	require.Equal(t, 1, n)
}

func TestSafeFederationTransportRefusesPrivateDestinations(t *testing.T) {
	tr := newSafeFederationDiscoveryTransport(federationTLSConfig(""))
	tr.resolve = func(context.Context, spec.ServerName) ([]fclient.ResolutionResult, error) {
		return []fclient.ResolutionResult{{Destination: "127.0.0.1:8448", Host: "evil.test", TLSServerName: "evil.test"}}, nil
	}
	req, err := http.NewRequest("GET", "https://evil.test:8448/_matrix/key/v2/server", nil)
	require.NoError(t, err)
	req.Header.Set("Host", "evil.test")
	_, err = (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Do(req)
	require.Error(t, err)
	var dial *federationDialError
	require.ErrorAs(t, err, &dial)
	require.Equal(t, "blocked-address", dial.Attempts[0].Class)
	require.Equal(t, "127.0.0.1:8448", dial.Attempts[0].Destination)
	// The well-known client is the SSRF-checked one as well.
	_, err = tr.fetchWellKnown(context.Background(), "localhost")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ssrf")
}

func TestDialErrorNamesResolvedAddressNotPlaceholderPort(t *testing.T) {
	tr := newFederationDiscoveryTransport(federationTLSConfig(""))
	tr.resolve = func(context.Context, spec.ServerName) ([]fclient.ResolutionResult, error) {
		return []fclient.ResolutionResult{{Destination: "127.0.0.1:1", Host: "matrix.test:443", TLSServerName: "matrix.test"}}, nil
	}
	c := NewFederationClient(minimalHS("me.test"))
	c.fedHTTPClient = &http.Client{Transport: tr, Timeout: 5 * time.Second}
	_, _, err := c.doFederationRequestContext(context.Background(), "GET", c.federationBaseURL("matrix.test")+"/_matrix/key/v2/server", "matrix.test", nil, 0)
	require.Error(t, err)
	require.True(t, isDeadHostError(err))
	require.Contains(t, err.Error(), "127.0.0.1:1")
	require.Contains(t, err.Error(), "refused")
	require.NotContains(t, err.Error(), ":8448")
	require.NotContains(t, err.Error(), "X-Matrix")
}

func TestDropDelegationTargetsSkipsDelegatedHostOfAnotherCandidate(t *testing.T) {
	d := newSafeFederationDiscoveryTransport(federationTLSConfig(""))
	d.resolve = func(context.Context, spec.ServerName) ([]fclient.ResolutionResult, error) {
		return []fclient.ResolutionResult{{Destination: "matrix-federation.example.org:443", Host: "matrix-federation.example.org:443", TLSServerName: "matrix-federation.example.org"}}, nil
	}
	_, err := d.destinations(context.Background(), "example.org")
	require.NoError(t, err)
	old := sharedDiscovery.Swap(d)
	defer sharedDiscovery.Store(old)
	require.Equal(t, []string{"example.org", "other.example"}, dropDelegationTargets([]string{"example.org", "matrix-federation.example.org", "other.example"}))
	// Without the delegating name in the list the host is left alone.
	require.Equal(t, []string{"matrix-federation.example.org", "other.example"}, dropDelegationTargets([]string{"matrix-federation.example.org", "other.example"}))
}

func TestBestJoinFailureDoesNotBlameRoomWhenOthersUnreachable(t *testing.T) {
	notFound := joinAttemptOutcome{server: "a", failure: &MatrixError{ErrCode: "M_NOT_FOUND", Error: "Not an active room on this server"}, err: &federationHTTPError{Status: 404}}
	hung := joinAttemptOutcome{server: "b", failure: &MatrixError{ErrCode: "M_UNKNOWN"}, err: &federationTransportError{err: context.DeadlineExceeded}}
	me := bestJoinFailure([]joinAttemptOutcome{hung, notFound, hung})
	require.Contains(t, me.Error, "2 server(s) did not answer")
	require.Contains(t, me.Error, "1 reported")
	// Without unreachable servers the remote answer is passed through.
	require.Equal(t, "Not an active room on this server", bestJoinFailure([]joinAttemptOutcome{notFound}).Error)
}

type nopReadCloser struct{ *strings.Reader }

func (nopReadCloser) Close() error   { return nil }
func nopBody(s string) nopReadCloser { return nopReadCloser{strings.NewReader(s)} }
