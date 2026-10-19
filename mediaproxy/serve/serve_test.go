package serve

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/clientip"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/stretchr/testify/require"
)

func pngBytes() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	return b.Bytes()
}

func setup(t *testing.T, upstream http.Handler) (*httptest.Server, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	old := config.C
	config.C.EncryptedKey = "0123456789abcdef-test-key"
	config.C.AppBaseURL = "https://c.test"
	config.C.MediaProxyEnabled = "true"
	oldClient, oldValidate := NewClient, Validate
	NewClient = func() *http.Client { return &http.Client{} }
	Validate = func(string) error { return nil }
	failures.Range(func(k, _ any) bool { failures.Delete(k); return true })
	failN = 0
	ipMu.Lock()
	ipStates = map[string]*ipState{}
	ipMu.Unlock()
	limiters.Range(func(k, _ any) bool { limiters.Delete(k); return true })
	t.Cleanup(func() { config.C = old; NewClient, Validate = oldClient, oldValidate })
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	r := gin.New()
	r.Match([]string{"GET", "HEAD", "POST"}, "/proxy/media/:token", Serve)
	return srv, r
}

func proxyPath(kind, raw string) string {
	tok, _ := mediaproxy.Seal(kind, raw, 0)
	return "/proxy/media/" + tok
}

func do(r http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRejectsForgedAndTamperedTokens(t *testing.T) {
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("upstream must not be contacted") }))
	raw := srv.URL + "/a.png"
	p := proxyPath("avatar", raw)
	require.Equal(t, 403, do(r, "GET", "/proxy/media/AAAA", nil).Code)
	require.Equal(t, 403, do(r, "GET", "/proxy/media/"+base64.RawURLEncoding.EncodeToString([]byte(raw)), nil).Code, "plain base64 URL")
	flipped := []byte(p)
	if flipped[len(flipped)-3] == 'A' {
		flipped[len(flipped)-3] = 'B'
	} else {
		flipped[len(flipped)-3] = 'A'
	}
	require.Equal(t, 403, do(r, "GET", string(flipped), nil).Code)
	require.Equal(t, 403, do(r, "GET", "/proxy/media/"+strings.Repeat("A", 4000), nil).Code)
	require.Equal(t, 405, do(r, "POST", p, nil).Code)
	config.C.MediaProxyEnabled = "false"
	require.Equal(t, 404, do(r, "GET", p, nil).Code)
}

func TestRelaysImageWithSafeHeaders(t *testing.T) {
	var gotUA, gotRef, gotCookie string
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotUA, gotRef, gotCookie = req.UserAgent(), req.Header.Get("Referer"), req.Header.Get("Cookie")
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"abc"`)
		_, _ = w.Write(pngBytes())
	}))
	w := do(r, "GET", proxyPath("media", srv.URL+"/a.png"), map[string]string{"Referer": "https://c.test/", "Cookie": "s=1", "Authorization": "Bearer x"})
	require.Equal(t, 200, w.Code)
	require.Equal(t, pngBytes(), w.Body.Bytes())
	require.Equal(t, "image/png", w.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.Contains(t, w.Header().Get("Content-Security-Policy"), "sandbox")
	require.Equal(t, `"abc"`, w.Header().Get("ETag"))
	require.Contains(t, w.Header().Get("Cache-Control"), "max-age")
	require.Contains(t, gotUA, "CommunityMediaProxy")
	require.Empty(t, gotRef)
	require.Empty(t, gotCookie)

	head := do(r, "HEAD", proxyPath("media", srv.URL+"/a.png"), nil)
	require.Equal(t, 200, head.Code)
	require.Empty(t, head.Body.Bytes())
}

func TestBlocksActiveContent(t *testing.T) {
	for name, tc := range map[string]struct{ ctype, body string }{
		"svg declared":     {"image/svg+xml", "<svg xmlns='http://www.w3.org/2000/svg'><script>alert(1)</script></svg>"},
		"html as image":    {"image/png", "<html><script>alert(1)</script></html>"},
		"svg as image/png": {"image/png", "<?xml version='1.0'?><svg xmlns='http://www.w3.org/2000/svg'/>"},
		"html declared":    {"text/html", "<html></html>"},
		"octet stream":     {"application/octet-stream", "abc"},
		"javascript":       {"application/javascript", "alert(1)"},
		"no content type":  {"", "abc"},
	} {
		srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tc.ctype != "" {
				w.Header().Set("Content-Type", tc.ctype)
			} else {
				w.Header()["Content-Type"] = nil
			}
			_, _ = w.Write([]byte(tc.body))
		}))
		w := do(r, "GET", proxyPath("media", srv.URL+"/x"), nil)
		require.Equal(t, 415, w.Code, name)
		require.NotContains(t, w.Body.String(), "alert", name)
	}
}

func TestSizeCaps(t *testing.T) {
	big := append(pngBytes(), bytes.Repeat([]byte{0}, 5000)...)
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(big)
	}))
	config.C.MediaProxyMaxBytes = "1000"
	require.Equal(t, 413, do(r, "GET", proxyPath("media", srv.URL+"/big.png"), nil).Code)

	vsrv, vr := setup(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		if req.URL.Path == "/chunked.mp4" {
			for i := 0; i < 5; i++ {
				_, _ = w.Write(make([]byte, 1000))
				w.(http.Flusher).Flush()
			}
			return
		}
		w.Header().Set("Content-Length", "5000")
		_, _ = w.Write(make([]byte, 5000))
	}))
	config.C.MediaProxyMaxStreamBytes = "1000"
	require.Equal(t, 413, do(vr, "GET", proxyPath("media", vsrv.URL+"/v.mp4"), nil).Code)
	// Without a length the stream is cut at the cap.
	require.LessOrEqual(t, do(vr, "GET", proxyPath("media", vsrv.URL+"/chunked.mp4"), nil).Body.Len(), 1000)
}

func TestRangeIsStreamedThrough(t *testing.T) {
	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i)
	}
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeContent(w, req, "v.mp4", time0, bytes.NewReader(data))
	}))
	w := do(r, "GET", proxyPath("media", srv.URL+"/v.mp4"), map[string]string{"Range": "bytes=10-19"})
	require.Equal(t, 206, w.Code)
	require.Equal(t, data[10:20], w.Body.Bytes())
	require.Equal(t, "bytes 10-19/1000", w.Header().Get("Content-Range"))
	require.Equal(t, "video/mp4", w.Header().Get("Content-Type"))
	w = do(r, "GET", proxyPath("media", srv.URL+"/v.mp4"), map[string]string{"Range": "bytes=5000-"})
	require.Equal(t, 416, w.Code)
}

func TestNothingIsPersistedAndFailuresAreMemoised(t *testing.T) {
	var hits atomic.Int32
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(404)
	}))
	p := proxyPath("avatar", srv.URL+"/gone.png")
	require.Equal(t, 404, do(r, "GET", p, nil).Code)
	w := do(r, "GET", p, nil)
	require.Equal(t, 404, w.Code, "the failure memo repeats the original answer, not a 502")
	require.Equal(t, "negative_cache; upstream_status", w.Header().Get("X-Media-Proxy-Error"))
	require.EqualValues(t, 1, hits.Load())

	png := pngBytes()
	var okHits atomic.Int32
	srv2, r2 := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		okHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	p2 := proxyPath("avatar", srv2.URL+"/a.png")
	do(r2, "GET", p2, nil)
	do(r2, "GET", p2, nil)
	require.EqualValues(t, 2, okHits.Load(), "every request streams from the origin")
}

func TestConditionalRequestsAreRelayed(t *testing.T) {
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("If-None-Match") == `"v1"` {
			w.Header().Set("ETag", `"v1"`)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes())
	}))
	w := do(r, "GET", proxyPath("avatar", srv.URL+"/a.png"), map[string]string{"If-None-Match": `"v1"`})
	require.Equal(t, 304, w.Code)
	require.Empty(t, w.Body.Bytes())
}

func TestSSRFAndRedirects(t *testing.T) {
	// Real validators: a loopback source is refused before any request.
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("must not be contacted") }))
	Validate = realValidate
	require.Equal(t, 400, do(r, "GET", proxyPath("media", srv.URL+"/a.png"), nil).Code)
	require.Equal(t, 400, do(r, "GET", proxyPath("media", "http://169.254.169.254/latest/meta-data"), nil).Code)
	require.Equal(t, 400, do(r, "GET", proxyPath("media", "http://[::1]:8080/x"), nil).Code)
	require.Equal(t, 400, do(r, "GET", proxyPath("media", "file:///etc/passwd"), nil).Code)

	// Real client: even when validation passes, the dialer refuses private
	// addresses, so a redirect into the internal network cannot be followed.
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("internal host reached") }))
	defer internal.Close()
	Validate = func(string) error { return nil }
	NewClient = realClient
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, nil, internal.URL, http.StatusFound)
	}))
	defer redirect.Close()
	require.Equal(t, 502, do(r, "GET", proxyPath("media", redirect.URL+"/a.png"), nil).Code)
}

func TestRedirectLimit(t *testing.T) {
	var n atomic.Int32
	var srv *httptest.Server
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if n.Add(1) < 10 {
			http.Redirect(w, req, srv.URL+"/next", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes())
	}))
	NewClient = func() *http.Client {
		return &http.Client{CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return http.ErrUseLastResponse
			}
			return nil
		}}
	}
	w := do(r, "GET", proxyPath("media", srv.URL+"/a.png"), nil)
	require.NotEqual(t, 200, w.Code)
	require.LessOrEqual(t, n.Load(), int32(maxRedirects+1))
}

func pngServer(t *testing.T) (*httptest.Server, *gin.Engine) {
	return setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"e1"`)
		_, _ = w.Write(pngBytes())
	}))
}

func TestAnySiteAndGuestCanUseAValidToken(t *testing.T) {
	srv, r := pngServer(t)
	p := proxyPath("avatar", srv.URL+"/a.png")
	for name, hdr := range map[string]map[string]string{
		"guest, no headers": nil,
		"foreign referrer":  {"Referer": "https://evil.example/page"},
		"foreign origin":    {"Origin": "https://evil.example"},
		"cross-site fetch":  {"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors"},
	} {
		w := do(r, "GET", p, hdr)
		require.Equal(t, 200, w.Code, name)
		require.Equal(t, "cross-origin", w.Header().Get("Cross-Origin-Resource-Policy"), name)
		require.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"), name)
		require.Empty(t, w.Header().Get("Set-Cookie"), name)
	}
	head := do(r, "HEAD", p, map[string]string{"Origin": "https://evil.example"})
	require.Equal(t, 200, head.Code)
	require.Equal(t, "cross-origin", head.Header().Get("Cross-Origin-Resource-Policy"))
}

func TestExpiredLinkIsGoneAndTamperedIsForbidden(t *testing.T) {
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("upstream must not be contacted") }))
	defer func() { mediaproxy.Now = time.Now }()
	raw := srv.URL + "/a.png"
	tok, _ := mediaproxy.Seal(mediaproxy.KindMedia, raw, time.Now().Unix()-10)
	w := do(r, "GET", "/proxy/media/"+tok, nil)
	require.Equal(t, 410, w.Code)
	require.Contains(t, w.Body.String(), "expired")
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "cross-origin", w.Header().Get("Cross-Origin-Resource-Policy"))
	require.Equal(t, 410, do(r, "HEAD", "/proxy/media/"+tok, nil).Code)

	// Editing the token to extend it breaks authentication, not expiry.
	b, _ := base64.RawURLEncoding.DecodeString(tok)
	b[len(b)-1] ^= 1
	require.Equal(t, 403, do(r, "GET", "/proxy/media/"+base64.RawURLEncoding.EncodeToString(b), nil).Code)
}

func TestMaxAgeNeverOutlivesTheToken(t *testing.T) {
	srv, r := pngServer(t)
	defer func() { mediaproxy.Now = time.Now }()
	nowSec := time.Now().Unix()
	mediaproxy.Now = func() time.Time { return time.Unix(nowSec, 0) }
	for _, tc := range []struct {
		remaining int64
		want      string
	}{{100, "public, max-age=100"}, {30, "public, max-age=30"}, {0, "public, max-age=0"}, {400000, "public, max-age=86400"}} {
		tok, _ := mediaproxy.Seal(mediaproxy.KindMedia, srv.URL+"/a.png", nowSec+tc.remaining)
		w := do(r, "GET", "/proxy/media/"+tok, nil)
		require.Equal(t, 200, w.Code)
		require.Equal(t, tc.want, w.Header().Get("Cache-Control"))
		require.Equal(t, `"e1"`, w.Header().Get("ETag"))
	}
	// API-minted links live at most 3600s, so the cap applies to them too.
	tok, _ := mediaproxy.Seal(mediaproxy.KindMedia, srv.URL+"/a.png", mediaproxy.ExpiryFor(time.Unix(nowSec, 0)))
	cc := do(r, "GET", "/proxy/media/"+tok, nil).Header().Get("Cache-Control")
	age, err := strconv.Atoi(strings.TrimPrefix(cc, "public, max-age="))
	require.NoError(t, err)
	require.LessOrEqual(t, age, 3600)
	require.GreaterOrEqual(t, age, 2990)
	// Links without an expiry keep the default.
	tok, _ = mediaproxy.Seal(mediaproxy.KindMedia, srv.URL+"/a.png", 0)
	require.Equal(t, "public, max-age=86400", do(r, "GET", "/proxy/media/"+tok, nil).Header().Get("Cache-Control"))
}

func TestPerIPRateLimit(t *testing.T) {
	srv, r := pngServer(t)
	p := proxyPath("avatar", srv.URL+"/a.png")
	// Make the per-host limiter irrelevant for this test.
	hostRateReset := func() { limiters.Range(func(k, _ any) bool { limiters.Delete(k); return true }) }
	var limited bool
	for i := 0; i < ipBurst+10; i++ {
		hostRateReset()
		req := httptest.NewRequest("GET", p, nil)
		req.RemoteAddr = "203.0.113.7:1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == 429 {
			limited = true
			require.NotEmpty(t, w.Header().Get("Retry-After"))
			break
		}
	}
	require.True(t, limited, "one client cannot exceed its burst")
	// Another client is unaffected.
	hostRateReset()
	req := httptest.NewRequest("GET", p, nil)
	req.RemoteAddr = "198.51.100.9:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
}

func TestPerIPConcurrencyLimit(t *testing.T) {
	var releases []func()
	for i := 0; i < ipConcurrent; i++ {
		rel, ok := ipAcquire("192.0.2.1")
		require.True(t, ok)
		releases = append(releases, rel)
	}
	_, ok := ipAcquire("192.0.2.1")
	require.False(t, ok, "too many simultaneous transfers for one client")
	_, ok = ipAcquire("192.0.2.2")
	require.True(t, ok, "other clients are independent")
	releases[0]()
	_, ok = ipAcquire("192.0.2.1")
	require.True(t, ok, "a finished transfer frees a slot")
	for _, rel := range releases[1:] {
		rel()
	}
}

func TestPerIPLimitKeysOnTheRealAddressBehindAProxy(t *testing.T) {
	srv, r := pngServer(t)
	require.NoError(t, clientip.Configure(r))
	p := proxyPath("avatar", srv.URL+"/a.png")
	reset := func() { limiters.Range(func(k, _ any) bool { limiters.Delete(k); return true }) }
	get := func(peer, realIP, xff string) int {
		reset()
		req := httptest.NewRequest("GET", p, nil)
		req.RemoteAddr = peer
		if realIP != "" {
			req.Header.Set("X-Real-IP", realIP)
		}
		req.Header.Set("X-Forwarded-For", xff)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	// nginx at 172.26.0.1 reports the real client in X-Real-IP; the client
	// rotates a forged leading X-Forwarded-For to dodge the limit.
	limited := false
	for i := 0; i < ipBurst+20; i++ {
		forged := "10." + strconv.Itoa(i/250) + "." + strconv.Itoa(i%250) + ".1"
		if get("172.26.0.1:4000", "198.51.100.7", forged+", 198.51.100.7") == 429 {
			limited = true
			break
		}
	}
	require.True(t, limited, "forged XFF values must not create new buckets")
	require.Equal(t, 200, get("172.26.0.1:4000", "198.51.100.8", "198.51.100.8"), "another real client has its own bucket")
	// Direct, untrusted peers cannot choose a bucket either.
	for i := 0; i < ipBurst+20; i++ {
		if get("203.0.113.50:1", "", "7."+strconv.Itoa(i)+".0.1") == 429 {
			return
		}
	}
	t.Fatal("untrusted peer escaped its limit with forged headers")
}

// Object stores often label every file application/octet-stream; the bytes
// decide, and only safe raster images and audio/video pass.
func TestOctetStreamIsSniffed(t *testing.T) {
	mp4 := append([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0}, make([]byte, 32)...)
	webp := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
	avif := append([]byte{0, 0, 0, 0x1c, 'f', 't', 'y', 'p', 'a', 'v', 'i', 'f', 0, 0, 0, 0}, make([]byte, 32)...)
	for name, tc := range map[string]struct {
		ctype string
		body  []byte
		want  int
		out   string
	}{
		"png":          {"application/octet-stream", pngBytes(), 200, "image/png"},
		"png binary":   {"binary/octet-stream", pngBytes(), 200, "image/png"},
		"webp":         {"application/octet-stream", webp, 200, "image/webp"},
		"avif":         {"application/octet-stream", avif, 200, "image/avif"},
		"mp4":          {"application/octet-stream", mp4, 200, "video/mp4"},
		"html":         {"application/octet-stream", []byte("<html><script>alert(1)</script></html>"), 415, ""},
		"svg":          {"binary/octet-stream", []byte("<?xml version='1.0'?><svg xmlns='http://www.w3.org/2000/svg'/>"), 415, ""},
		"png as plain": {"text/plain", pngBytes(), 415, ""},
	} {
		srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", tc.ctype)
			_, _ = w.Write(tc.body)
		}))
		w := do(r, "GET", proxyPath("media", srv.URL+"/x"), nil)
		require.Equal(t, tc.want, w.Code, name)
		if tc.want == 200 {
			require.Equal(t, tc.out, w.Header().Get("Content-Type"), name)
			require.Equal(t, tc.body, w.Body.Bytes(), name)
		}
	}
}
