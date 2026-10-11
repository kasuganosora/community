package homeserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/matrix-org/gomatrixserverlib/fclient"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRrest-of-image")

// rewriteTransport sends every request to the fake remote, keeping the path.
type rewriteTransport struct{ target *url.URL }

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c := req.Clone(req.Context())
	c.URL.Scheme, c.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(c)
}

func fakeFetcher(t *testing.T, h http.Handler) (*remoteMediaFetcher, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	fed, _, _, _ := newTestFederationClient()
	cl := func() *http.Client {
		return &http.Client{Transport: rewriteTransport{u}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &remoteMediaFetcher{fed: fed, disc: cl(), direct: cl(), timeout: 5 * time.Second}, &paths
}

func multipartMedia(w http.ResponseWriter, part string) {
	w.Header().Set("Content-Type", "multipart/mixed; boundary=B")
	fmt.Fprintf(w, "--B\r\nContent-Type: application/json\r\n\r\n{}\r\n--B\r\n%s\r\n--B--\r\n", part)
}

func readAll(t *testing.T, m *remoteMedia) []byte {
	t.Helper()
	b, err := io.ReadAll(m.Body)
	require.NoError(t, err)
	return b
}

func TestRemoteMediaFederationDownloadIsSignedAndStreamed(t *testing.T) {
	var auth string
	f, paths := fakeFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		multipartMedia(w, "Content-Type: image/png\r\nContent-Disposition: inline; filename=\"a.png\"\r\n\r\n"+string(pngBytes))
	}))
	m, cancel, err := f.Open(context.Background(), "tsuki.games", "abc123", "download", nil)
	require.NoError(t, err)
	defer cancel()
	defer m.Close()
	require.Equal(t, pngBytes, readAll(t, m))
	require.Equal(t, "image/png", m.ContentType)
	require.Equal(t, "a.png", m.Filename)
	require.Contains(t, auth, "X-Matrix")
	require.Equal(t, []string{"/_matrix/federation/v1/media/download/abc123"}, *paths)
}

func TestRemoteMediaFollowsLocationPartThroughServer(t *testing.T) {
	f, paths := fakeFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cdn/blob" {
			w.Header().Set("Content-Type", "image/png")
			w.Write(pngBytes)
			return
		}
		multipartMedia(w, "Location: https://cdn.example.org/cdn/blob\r\n\r\n")
	}))
	m, cancel, err := f.Open(context.Background(), "tsuki.games", "abc", "download", nil)
	require.NoError(t, err)
	defer cancel()
	defer m.Close()
	require.Equal(t, pngBytes, readAll(t, m))
	require.Equal(t, "/cdn/blob", (*paths)[len(*paths)-1])
}

func TestRemoteMediaLocationToInternalAddressIsBlocked(t *testing.T) {
	f, _ := fakeFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/_matrix/federation/") {
			http.NotFound(w, r)
			return
		}
		multipartMedia(w, "Location: http://127.0.0.1:9/secret\r\n\r\n")
	}))
	f.direct = activitypub.NewMediaFetchHTTPClient(time.Second)
	f.timeout = 3 * time.Second
	_, cancel, err := f.Open(context.Background(), "tsuki.games", "abc", "download", nil)
	defer cancel()
	require.Error(t, err)
	require.Contains(t, err.Error(), "ssrf")
}

func TestRemoteMediaFallsBackToLegacyEndpoints(t *testing.T) {
	f, paths := fakeFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/_matrix/media/v3/download/") {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(pngBytes)
			return
		}
		http.NotFound(w, r)
	}))
	m, cancel, err := f.Open(context.Background(), "tsuki.games", "abc", "download", nil)
	require.NoError(t, err)
	defer cancel()
	defer m.Close()
	require.Equal(t, pngBytes, readAll(t, m))
	require.Len(t, *paths, 2)
	require.Contains(t, (*paths)[1], "allow_remote=false")
}

func TestRemoteThumbnailFallsBackToOriginal(t *testing.T) {
	f, paths := fakeFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_matrix/federation/v1/media/download/abc" {
			multipartMedia(w, "Content-Type: image/png\r\n\r\n"+string(pngBytes))
			return
		}
		http.NotFound(w, r)
	}))
	params := url.Values{"width": {"96"}, "height": {"96"}, "method": {"crop"}}
	m, cancel, err := f.Open(context.Background(), "tsuki.games", "abc", "thumbnail", params)
	require.NoError(t, err)
	defer cancel()
	defer m.Close()
	require.Equal(t, pngBytes, readAll(t, m))
	require.Contains(t, (*paths)[0], "/media/thumbnail/abc?height=96&method=crop&width=96")
}

func TestRemoteMediaSizeLimit(t *testing.T) {
	t.Setenv("MATRIX_REMOTE_MEDIA_MAX_BYTES", "10")
	f, _ := fakeFetcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		multipartMedia(w, "Content-Type: image/png\r\n\r\n"+string(pngBytes))
	}))
	m, cancel, err := f.Open(context.Background(), "tsuki.games", "abc", "download", nil)
	require.NoError(t, err)
	defer cancel()
	defer m.Close()
	_, err = io.ReadAll(m.Body)
	require.ErrorIs(t, err, errRemoteMediaTooLarge)
}

func TestRemoteMediaRejectsBadTargets(t *testing.T) {
	for _, c := range [][2]string{{"a/b", "x"}, {"host?x=1", "x"}, {"u@host", "x"}, {"host", "../x"}, {"host", ""}, {"host", "a/b"}, {"", "x"}} {
		require.Error(t, validRemoteMediaTarget(c[0], c[1]), c)
	}
	require.NoError(t, validRemoteMediaTarget("matrix.org", "AbC_-123"))
	require.NoError(t, validRemoteMediaTarget("tsuki.games:8448", "AbC"))
	require.NoError(t, validRemoteMediaTarget("[::1]:8448", "AbC"))
}

func TestServeRemoteMediaSniffsAndSandboxes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := func(ct, body string, thumb bool) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("GET", "/", nil)
		serveRemoteMedia(c, &remoteMedia{Body: io.NopCloser(strings.NewReader(body)), ContentType: ct, Filename: "x.bin", Length: -1}, thumb)
		return rec
	}
	rec := run("application/octet-stream", string(pngBytes), false)
	require.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Header().Get("Content-Disposition"), "inline")
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "cross-origin", rec.Header().Get("Cross-Origin-Resource-Policy"))
	require.Equal(t, pngBytes, rec.Body.Bytes())

	rec = run("text/html", "<script>alert(1)</script>", false)
	require.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	rec = run("image/svg+xml", "<svg/>", true)
	require.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	rec = run("", "<html>", false)
	require.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	require.True(t, bytes.HasPrefix(run("image/jpeg", "\xff\xd8\xff", false).Body.Bytes(), []byte("\xff\xd8")))
}

type recordingRT struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req.URL.Host+"|"+req.Host)
	r.mu.Unlock()
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}, Request: req}, nil
}

func TestMediaDiscoveryTransportUsesDelegatedDestination(t *testing.T) {
	disc := newFederationDiscoveryTransport(&tls.Config{})
	disc.resolve = func(_ context.Context, s spec.ServerName) ([]fclient.ResolutionResult, error) {
		require.Equal(t, spec.ServerName("tsuki.games:8448"), s)
		return []fclient.ResolutionResult{{Destination: "matrix.tsuki.games:443", Host: "tsuki.games", TLSServerName: "tsuki.games"}}, nil
	}
	rt := &recordingRT{}
	tr := &mediaDiscoveryTransport{discovery: disc, tlsConfig: &tls.Config{}, pools: map[string]http.RoundTripper{"tsuki.games": rt}}
	req, _ := http.NewRequest("GET", "https://tsuki.games:8448/_matrix/federation/v1/media/download/x", nil)
	req.Header.Set("Host", "tsuki.games:8448")
	resp, err := tr.RoundTrip(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, []string{"matrix.tsuki.games:443|tsuki.games"}, rt.calls)
}
