package serve

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClassifyFetchError(t *testing.T) {
	wrap := func(err error) error {
		return &url.Error{Op: "Get", URL: "https://media.example/a.png?sig=secret", Err: err}
	}
	for name, tc := range map[string]struct {
		err      error
		timedOut bool
		class    string
		status   int
		ttl      time.Duration
	}{
		"dns not found":     {wrap(fmt.Errorf("ssrf: failed to resolve %q: %w", "nx.example", &net.DNSError{Err: "no such host", Name: "nx.example", IsNotFound: true})), false, classDNS, 502, permanentFailureTTL},
		"dns temporary":     {wrap(&net.DNSError{Err: "server misbehaving", Name: "x.example", IsTemporary: true}), false, classDNS, 502, transientFailureTTL},
		"blocked address":   {wrap(errors.New("ssrf: blocked connection to internal address 10.0.0.1 (x.example)")), false, classBlocked, 502, permanentFailureTTL},
		"blocked redirect":  {wrap(errors.New(`ssrf: blocked redirect to non-HTTP scheme "file"`)), false, classBlocked, 502, permanentFailureTTL},
		"redirect limit":    {wrap(errors.New("ssrf: stopped after 3 redirects")), false, classRedirects, 502, permanentFailureTTL},
		"header deadline":   {wrap(context.Canceled), true, classTimeout, 504, transientFailureTTL},
		"deadline exceeded": {wrap(context.DeadlineExceeded), false, classTimeout, 504, transientFailureTTL},
		"dial timeout":      {wrap(&net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded}), false, classTimeout, 504, transientFailureTTL},
		"tls record":        {wrap(tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}), false, classTLS, 502, transientFailureTTL},
		"tls unknown ca":    {wrap(&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}), false, classTLS, 502, transientFailureTTL},
		"connect refused":   {wrap(&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}), false, classConnect, 502, transientFailureTTL},
		"connection reset":  {wrap(&net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}), false, classConnect, 502, transientFailureTTL},
		"other":             {wrap(errors.New("malformed HTTP response")), false, classFetch, 502, transientFailureTTL},
		"avatar status 503": {upstreamStatusError{code: 503}, false, classUpstream, 502, transientFailureTTL},
		"avatar status 404": {upstreamStatusError{code: 404}, false, classUpstream, 404, permanentFailureTTL},
	} {
		f := classifyFetchError(tc.err, tc.timedOut)
		require.Equal(t, tc.class, f.class, name)
		require.Equal(t, tc.status, f.status, name)
		require.Equal(t, tc.ttl, f.ttl, name)
	}
}

func TestUpstreamStatusSemantics(t *testing.T) {
	for code, want := range map[int]failure{
		404: {status: 404, ttl: permanentFailureTTL},
		410: {status: 404, ttl: permanentFailureTTL}, // 410 stays reserved for our expired links
		403: {status: 404, ttl: permanentFailureTTL}, // S3-style stores answer 403 for missing keys
		401: {status: 404, ttl: permanentFailureTTL},
		429: {status: 503, ttl: transientFailureTTL},
		500: {status: 502, ttl: transientFailureTTL},
		503: {status: 502, ttl: transientFailureTTL},
		400: {status: 502, ttl: permanentFailureTTL},
	} {
		f := upstreamFailure(code)
		require.Equal(t, want.status, f.status, code)
		require.Equal(t, want.ttl, f.ttl, code)
		require.Equal(t, classUpstream, f.class, code)
		require.Equal(t, code, f.upstream, code)
	}
}

// capturedLog records warn lines and lets the test move the clock.
type capturedLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *capturedLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func captureFailureLog(t *testing.T) (*capturedLog, func(time.Duration)) {
	t.Helper()
	l := &capturedLog{}
	oldWarn, oldNow := logWarn, now
	clock := time.Now()
	var mu sync.Mutex
	now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	logWarn = func(format string, args ...any) {
		l.mu.Lock()
		l.lines = append(l.lines, fmt.Sprintf(format, args...))
		l.mu.Unlock()
	}
	failureLogMu.Lock()
	failureLog = map[string]*failureLogEntry{}
	failureLogMu.Unlock()
	t.Cleanup(func() { logWarn, now = oldWarn, oldNow })
	return l, func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
}

func TestTransientFailuresAreRememberedBriefly(t *testing.T) {
	var hits atomic.Int32
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	withSharedFailures(t, nil)
	_, advance := captureFailureLog(t)
	p := proxyPath("media", srv.URL+"/flaky.mp4?sig=abc")
	w := do(r, "GET", p, nil)
	require.Equal(t, 502, w.Code)
	require.Equal(t, classUpstream, w.Header().Get("X-Media-Proxy-Error"))
	require.Equal(t, 502, do(r, "GET", p, nil).Code)
	require.EqualValues(t, 1, hits.Load(), "memoised")
	advance(transientFailureTTL + time.Second)
	require.Equal(t, 502, do(r, "GET", p, nil).Code)
	require.EqualValues(t, 2, hits.Load(), "a 5xx is retried after the short memo")
}

func TestPermanentFailuresKeepTheirAnswer(t *testing.T) {
	var hits atomic.Int32
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusGone)
	}))
	withSharedFailures(t, nil)
	_, advance := captureFailureLog(t)
	p := proxyPath("media", srv.URL+"/gone.mp4")
	require.Equal(t, 404, do(r, "GET", p, nil).Code)
	advance(transientFailureTTL + time.Second)
	require.Equal(t, 404, do(r, "GET", p, nil).Code, "the memo answers 404 again, not 502")
	require.EqualValues(t, 1, hits.Load())
	advance(permanentFailureTTL)
	require.Equal(t, 404, do(r, "GET", p, nil).Code)
	require.EqualValues(t, 2, hits.Load())
}

func TestRelayFailureClassesAndStatuses(t *testing.T) {
	withSharedFailures(t, nil)
	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		srv, r := setup(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
		t.Cleanup(func() { close(release) })
		old := headerTimeout
		headerTimeout = 100 * time.Millisecond
		t.Cleanup(func() { headerTimeout = old })
		w := do(r, "GET", proxyPath("media", srv.URL+"/slow.mp4"), nil)
		require.Equal(t, http.StatusGatewayTimeout, w.Code)
		require.Equal(t, classTimeout, w.Header().Get("X-Media-Proxy-Error"))
	})
	t.Run("connect", func(t *testing.T) {
		srv, r := setup(t, http.NotFoundHandler())
		dead := srv.URL
		srv.Close()
		w := do(r, "GET", proxyPath("media", dead+"/a.mp4"), nil)
		require.Equal(t, 502, w.Code)
		require.Equal(t, classConnect, w.Header().Get("X-Media-Proxy-Error"))
	})
	t.Run("dns", func(t *testing.T) {
		_, r := setup(t, http.NotFoundHandler())
		Validate = func(string) error {
			return fmt.Errorf("ssrf: failed to resolve %q: %w", "nx.example", &net.DNSError{Err: "no such host", Name: "nx.example", IsNotFound: true})
		}
		w := do(r, "GET", proxyPath("media", "https://nx.example/a.png"), nil)
		require.Equal(t, 502, w.Code)
		require.Equal(t, classDNS, w.Header().Get("X-Media-Proxy-Error"))
	})
	t.Run("blocked", func(t *testing.T) {
		srv, r := setup(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("must not be contacted") }))
		Validate = realValidate
		w := do(r, "GET", proxyPath("media", srv.URL+"/a.png"), nil)
		require.Equal(t, 400, w.Code)
		require.Equal(t, classBlocked, w.Header().Get("X-Media-Proxy-Error"))
	})
	t.Run("rate limited origin", func(t *testing.T) {
		srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "12")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		w := do(r, "GET", proxyPath("media", srv.URL+"/a.mp4"), nil)
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		require.Equal(t, "12", w.Header().Get("Retry-After"))
	})
	t.Run("content type", func(t *testing.T) {
		srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html></html>"))
		}))
		w := do(r, "GET", proxyPath("media", srv.URL+"/a"), nil)
		require.Equal(t, http.StatusUnsupportedMediaType, w.Code)
		require.Equal(t, classContentType, w.Header().Get("X-Media-Proxy-Error"))
	})
}

// A visitor who disconnects must not poison the memo for everyone else.
func TestClientGoneIsNotMemoised(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	srv, r := setup(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1); <-release }))
	t.Cleanup(func() { close(release) })
	withSharedFailures(t, nil)
	raw := srv.URL + "/v.mp4"
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", proxyPath("media", raw), nil).WithContext(ctx)
	time.AfterFunc(100*time.Millisecond, cancel)
	r.ServeHTTP(httptest.NewRecorder(), req)
	_, failed := recentlyFailedLocal(failureKey(raw))
	require.False(t, failed)
}

func TestFailureLogIsRateLimitedAndRedacted(t *testing.T) {
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	withSharedFailures(t, nil)
	logs, advance := captureFailureLog(t)
	raw := srv.URL + "/private/path.png?X-Amz-Signature=topsecret"
	p := proxyPath("media", raw)
	token := strings.TrimPrefix(p, "/proxy/media/")
	host := hostOf(raw)

	require.Equal(t, 404, do(r, "GET", p, nil).Code) // origin failure: logged
	require.Equal(t, 404, do(r, "GET", p, nil).Code) // memo hit: logged once as negative_cache
	require.Equal(t, 404, do(r, "GET", p, nil).Code) // memo hit: suppressed
	lines := logs.all()
	require.Len(t, lines, 2, "%q", lines)
	require.Contains(t, lines[0], "media proxy: failed host="+host+" class=upstream_status status=404 upstream_status=404 kind=media duration_ms=")
	require.Contains(t, lines[0], "memo_ttl=5m0s")
	require.Contains(t, lines[1], "class=negative_cache status=404 cached_class=upstream_status upstream_status=404 ttl_left=5m0s")
	for _, line := range lines {
		require.NotContains(t, line, token)
		require.NotContains(t, line, "topsecret")
		require.NotContains(t, line, "/private/path")
		require.NotContains(t, line, "?")
	}

	// A hot failing link logs again after the window, with the count.
	advance(failureLogWindow)
	require.Equal(t, 404, do(r, "GET", p, nil).Code)
	lines = logs.all()
	require.Len(t, lines, 3)
	require.Contains(t, lines[2], "class=negative_cache")
	require.Contains(t, lines[2], "ttl_left=4m0s")
	require.Contains(t, lines[2], "suppressed=1")

	// Token failures: no host to log for a forged token, never the token.
	require.Equal(t, 403, do(r, "GET", "/proxy/media/AAAAforged", nil).Code)
	lines = logs.all()
	require.Contains(t, lines[len(lines)-1], "host=- class=decrypt status=403")
	require.NotContains(t, lines[len(lines)-1], "AAAAforged")
}
