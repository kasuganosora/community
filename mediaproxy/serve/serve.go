// Package serve implements GET/HEAD /proxy/media/:token, which relays
// remote media to browsers (see package mediaproxy). It is a pure
// stream-through proxy, except avatars, which are resized and cached on
// disk by package avatarcache.
package serve

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/avatarcache"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/systemsettings"
	"golang.org/x/time/rate"
)

const (
	headerTimeout  = 10 * time.Second
	maxRedirects   = 3
	maxConcurrent  = 32
	acquireWait    = 3 * time.Second
	failureTTL     = 2 * time.Minute
	hostRate       = rate.Limit(20)
	hostBurst      = 40
	maxAge         = 86400 // seconds, capped by the token's remaining life
	ipRate         = rate.Limit(40)
	ipBurst        = 120
	ipConcurrent   = 12
	maxIPKeys      = 20000
	sandboxPolicy  = "default-src 'none'; style-src 'unsafe-inline'; sandbox"
	maxFailureKeys = 5000
)

// Overridable in tests.
var (
	// NewClient builds the outbound client; it must pin dialing to public
	// addresses, bound redirects and check every redirect target.
	NewClient = func() *http.Client { return activitypub.NewSafeHTTPClient(0, maxRedirects) }
	// Validate is the SSRF pre-check of a source URL.
	Validate = activitypub.ValidateURI
	now      = time.Now
)

var (
	slots    = make(chan struct{}, maxConcurrent)
	failures sync.Map // cache key -> time.Time (expiry)
	failN    int64
	failMu   sync.Mutex
	limiters sync.Map // host -> *rate.Limiter
	flights  sync.Map // cache key -> *sync.Mutex
)

func hostAllowed(host string) bool {
	l, _ := limiters.LoadOrStore(host, rate.NewLimiter(hostRate, hostBurst))
	return l.(*rate.Limiter).Allow()
}

func markFailed(key string) {
	failMu.Lock()
	defer failMu.Unlock()
	if failN >= maxFailureKeys {
		failures.Range(func(k, v any) bool {
			if now().After(v.(time.Time)) {
				failures.Delete(k)
				failN--
			}
			return true
		})
		if failN >= maxFailureKeys {
			return
		}
	}
	if _, loaded := failures.LoadOrStore(key, now().Add(failureTTL)); !loaded {
		failN++
	}
}

func recentlyFailed(key string) bool {
	v, ok := failures.Load(key)
	if !ok {
		return false
	}
	if now().After(v.(time.Time)) {
		failMu.Lock()
		if _, ok := failures.LoadAndDelete(key); ok {
			failN--
		}
		failMu.Unlock()
		return false
	}
	return true
}

// failureKey identifies a source URL in the in-memory failure memo.
func failureKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])
}

// Serve handles GET and HEAD /proxy/media/:token.
func Serve(c *gin.Context) {
	if !mediaproxy.Enabled() {
		c.Status(http.StatusNotFound)
		return
	}
	// Other sites and API clients may use proxied URLs; the encrypted token
	// is the only authorisation. Responses carry no credentials.
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	claims, err := mediaproxy.Open(c.Param("token"))
	switch {
	case errors.Is(err, mediaproxy.ErrExpired):
		c.Header("Cache-Control", "no-store")
		c.String(http.StatusGone, "media link expired; reload the post to get a new one")
		return
	case err != nil:
		c.Header("Cache-Control", "no-store")
		c.String(http.StatusForbidden, "invalid media link")
		return
	}
	kind, rawURL := claims.Kind, claims.URL
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Header("Allow", "GET, HEAD")
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	key := failureKey(rawURL)
	if recentlyFailed(key) {
		c.Status(http.StatusBadGateway)
		return
	}
	if err := Validate(rawURL); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	ip := c.ClientIP()
	if !ipAllowed(ip) {
		c.Header("Retry-After", "5")
		c.Status(http.StatusTooManyRequests)
		return
	}
	if kind == mediaproxy.KindAvatar && serveCachedAvatar(c, key, rawURL, strings.ToLower(u.Host), claims) {
		return
	}
	if !hostAllowed(strings.ToLower(u.Host)) {
		c.Header("Retry-After", "5")
		c.Status(http.StatusTooManyRequests)
		return
	}
	release, ok := acquire(c.Request.Context())
	if !ok {
		c.Header("Retry-After", "2")
		c.Status(http.StatusServiceUnavailable)
		return
	}
	defer release()
	releaseIP, ok := ipAcquire(ip)
	if !ok {
		c.Header("Retry-After", "2")
		c.Status(http.StatusTooManyRequests)
		return
	}
	defer releaseIP()

	relay(c, key, rawURL, kind, claims)
}

func acquire(ctx context.Context) (func(), bool) {
	t := time.NewTimer(acquireWait)
	defer t.Stop()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, true
	case <-t.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

func setCommonHeaders(c *gin.Context, ctype string) {
	c.Header("Content-Type", ctype)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", sandboxPolicy)
	c.Header("Content-Disposition", "inline")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	c.Header("Access-Control-Allow-Origin", "*")
}

// sniffImage returns the content type of an image header, or "" when the
// bytes are not a raster image the browser can display safely. SVG, HTML and
// other text types never pass.
func sniffImage(head []byte) string {
	t := http.DetectContentType(head)
	if i := strings.Index(t, ";"); i >= 0 {
		t = t[:i]
	}
	switch t {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/x-icon":
		return t
	}
	// DetectContentType does not know AVIF/HEIC/JXL: ISO-BMFF "ftyp" brands.
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		switch string(head[8:12]) {
		case "avif", "avis":
			return "image/avif"
		case "heic", "heix", "mif1":
			return "image/heic"
		}
	}
	if len(head) >= 2 && head[0] == 0xFF && head[1] == 0x0A {
		return "image/jxl"
	}
	return ""
}

// octetLike reports generic types some object stores (S3, R2, MinIO and
// servers behind them) send for every file; the real type is in the bytes.
func octetLike(t string) bool {
	switch t {
	case "", "application/octet-stream", "binary/octet-stream", "application/x-octet-stream", "application/unknown":
		return true
	}
	return false
}

// sniffMedia names the video or audio type of a file's first bytes.
func sniffMedia(head []byte) string {
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		switch brand := string(head[8:12]); brand {
		case "qt  ":
			return "video/quicktime"
		case "M4A ", "M4B ":
			return "audio/mp4"
		case "avif", "avis", "heic", "heix", "mif1":
			return "" // images, handled by sniffImage
		default:
			return "video/mp4" // isom, mp41, mp42, 3gp*, ...
		}
	}
	t := http.DetectContentType(head)
	if i := strings.Index(t, ";"); i >= 0 {
		t = t[:i]
	}
	switch {
	case t == "application/ogg":
		return "audio/ogg"
	case strings.HasPrefix(t, "video/"), strings.HasPrefix(t, "audio/"):
		return t
	}
	return ""
}

func upstreamType(h http.Header) string {
	t, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return strings.ToLower(t)
}

// relay fetches the source and streams it to the client.
func relay(c *gin.Context, key, rawURL, kind string, claims mediaproxy.Claims) {
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	timer := time.AfterFunc(headerTimeout, cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		timer.Stop()
		c.Status(http.StatusBadRequest)
		return
	}
	// Nothing about the visitor is forwarded except cache validators and
	// Range: no cookies, referrer, auth or client address.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CommunityMediaProxy/1.0; +"+strings.TrimRight(systemsettings.GetBaseURL(), "/")+")")
	req.Header.Set("Accept", "image/*,video/*,audio/*;q=0.9,*/*;q=0.1")
	for _, h := range []string{"If-None-Match", "If-Modified-Since", "If-Range"} {
		if v := c.GetHeader(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	if rng := c.GetHeader("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	resp, err := NewClient().Do(req)
	timer.Stop()
	if err != nil {
		markFailed(key)
		c.Status(http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		for _, h := range []string{"Etag", "Last-Modified"} {
			if v := resp.Header.Get(h); v != "" {
				c.Header(h, v)
			}
		}
		c.Header("Cache-Control", cacheControlFor(claims))
		c.Status(http.StatusNotModified)
		return
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		markFailed(key)
		c.Status(http.StatusNotFound)
		return
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		c.Header("Content-Range", resp.Header.Get("Content-Range"))
		c.Status(http.StatusRequestedRangeNotSatisfiable)
		return
	case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent:
		markFailed(key)
		c.Status(http.StatusBadGateway)
		return
	}

	declared := upstreamType(resp.Header)
	var src io.Reader = resp.Body
	if strings.HasPrefix(declared, "image/") && resp.ContentLength < 0 && resp.StatusCode == http.StatusOK {
		// Without a length the cap cannot be enforced after the headers are
		// sent, so such (rare) images are read in full, bounded, first.
		data, err := io.ReadAll(io.LimitReader(resp.Body, mediaproxy.MaxBytes()+1))
		if err != nil || int64(len(data)) > mediaproxy.MaxBytes() {
			markFailed(key)
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		src = bytes.NewReader(data)
		resp.ContentLength = int64(len(data))
	}
	body := bufio.NewReaderSize(src, 1024)
	if octetLike(declared) && (resp.StatusCode == http.StatusOK || strings.HasPrefix(resp.Header.Get("Content-Range"), "bytes 0-")) {
		head, _ := body.Peek(512)
		if t := sniffImage(head); t != "" {
			declared = t
		} else if t := sniffMedia(head); t != "" {
			declared = t
		}
	}
	limit := mediaproxy.MaxStreamBytes()
	ctype := declared
	switch {
	case strings.HasPrefix(declared, "image/"):
		limit = mediaproxy.MaxBytes()
		if strings.Contains(declared, "svg") {
			markFailed(key)
			c.Status(http.StatusUnsupportedMediaType)
			return
		}
		// The first bytes must be a raster image; a body that merely claims
		// to be one (HTML, SVG, scripts) is refused. A range that does not
		// start at the beginning has no header to check.
		if resp.StatusCode == http.StatusOK || strings.HasPrefix(resp.Header.Get("Content-Range"), "bytes 0-") {
			head, _ := body.Peek(512)
			if ctype = sniffImage(head); ctype == "" {
				markFailed(key)
				c.Status(http.StatusUnsupportedMediaType)
				return
			}
		}
	case strings.HasPrefix(declared, "video/") || strings.HasPrefix(declared, "audio/"):
	default:
		markFailed(key)
		c.Status(http.StatusUnsupportedMediaType)
		return
	}
	if resp.ContentLength > limit {
		markFailed(key)
		c.Status(http.StatusRequestEntityTooLarge)
		return
	}

	setCommonHeaders(c, ctype)
	c.Header("Cache-Control", cacheControlFor(claims))
	c.Header("Accept-Ranges", "bytes")
	for _, h := range []string{"Content-Range", "Etag", "Last-Modified"} {
		if v := resp.Header.Get(h); v != "" {
			c.Header(h, v)
		}
	}
	if resp.ContentLength >= 0 {
		c.Header("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	c.Status(resp.StatusCode)
	if c.Request.Method == http.MethodHead {
		return
	}
	n, err := io.Copy(c.Writer, io.LimitReader(body, limit))
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Debug("media proxy: stream aborted after %d bytes: %v", n, err)
	}
}

// cacheControlFor lets browsers cache for a day, but never past the moment
// the token stops working.
func cacheControlFor(claims mediaproxy.Claims) string {
	age := int64(maxAge)
	if claims.Expiry != 0 {
		if r := int64(claims.Remaining() / time.Second); r < age {
			age = r
		}
	}
	if age < 0 {
		age = 0
	}

	return "public, max-age=" + strconv.FormatInt(age, 10)
}

// errBusy marks a fetch refused by the local host/concurrency limits.
var errBusy = errors.New("media proxy busy")

// errUpstreamGone marks a source that answered 404/410.
var errUpstreamGone = errors.New("media proxy: source gone")

// avatarCache is the cache used for avatars; overridable in tests.
var avatarCache = avatarcache.Default

// serveCachedAvatar answers an avatar from the resized disk cache (fetching
// it on a miss). It returns false when the cache is unavailable so the
// caller relays the original instead.
func serveCachedAvatar(c *gin.Context, key, rawURL, host string, claims mediaproxy.Claims) bool {
	cache := avatarCache()
	if cache == nil {
		return false
	}
	size := avatarcache.NormalizeSize(c.Query("size"))
	entry, err := cache.Get(c.Request.Context(), "remote|"+rawURL, size, avatarcache.RemoteTTL(), func(ctx context.Context) ([]byte, error) {
		if !hostAllowed(host) {
			return nil, errBusy
		}
		release, ok := acquire(ctx)
		if !ok {
			return nil, errBusy
		}
		defer release()
		return fetchImage(ctx, rawURL)
	})
	if err != nil {
		switch {
		case errors.Is(err, errBusy):
			c.Header("Retry-After", "2")
			c.Status(http.StatusServiceUnavailable)
		case errors.Is(err, errUpstreamGone):
			markFailed(key)
			c.Status(http.StatusNotFound)
		case errors.Is(err, avatarcache.ErrTooLarge):
			markFailed(key)
			c.Status(http.StatusRequestEntityTooLarge)
		case errors.Is(err, avatarcache.ErrInvalidType):
			markFailed(key)
			c.Status(http.StatusUnsupportedMediaType)
		default:
			markFailed(key)
			c.Status(http.StatusBadGateway)
		}
		return true
	}
	f, err := os.Open(entry.Path)
	if err != nil {
		return false // evicted between lookup and open
	}
	defer func() { _ = f.Close() }()
	setCommonHeaders(c, entry.ContentType)
	c.Header("Cache-Control", cacheControlFor(claims))
	http.ServeContent(c.Writer, c.Request, "", entry.ModTime, f)
	return true
}

// fetchImage downloads a raster image source in full, bounded by
// MEDIA_PROXY_MAX_BYTES and the avatar cache's own limit.
func fetchImage(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CommunityMediaProxy/1.0; +"+strings.TrimRight(systemsettings.GetBaseURL(), "/")+")")
	req.Header.Set("Accept", "image/*;q=0.9,*/*;q=0.1")
	resp, err := NewClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil, errUpstreamGone
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	limit := min(mediaproxy.MaxBytes(), int64(avatarcache.MaxSourceBytes))
	if resp.ContentLength > limit {
		return nil, avatarcache.ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, avatarcache.ErrTooLarge
	}
	if sniffImage(data[:min(len(data), 512)]) == "" {
		return nil, avatarcache.ErrInvalidType
	}
	return data, nil
}

// Per-client limits: the endpoint is open to other sites, so every client
// address gets its own request rate and number of simultaneous transfers.
type ipState struct {
	limiter *rate.Limiter
	active  int
	seen    time.Time
}

var (
	ipMu     sync.Mutex
	ipStates = map[string]*ipState{}
)

func ipEntry(ip string) *ipState {
	st := ipStates[ip]
	if st == nil {
		if len(ipStates) >= maxIPKeys {
			for k, v := range ipStates {
				if v.active == 0 && now().Sub(v.seen) > time.Minute {
					delete(ipStates, k)
				}
			}
		}
		st = &ipState{limiter: rate.NewLimiter(ipRate, ipBurst)}
		ipStates[ip] = st
	}
	st.seen = now()

	return st
}

func ipAllowed(ip string) bool {
	ipMu.Lock()
	defer ipMu.Unlock()

	return ipEntry(ip).limiter.Allow()
}

func ipAcquire(ip string) (func(), bool) {
	ipMu.Lock()
	defer ipMu.Unlock()
	st := ipEntry(ip)
	if st.active >= ipConcurrent {
		return nil, false
	}
	st.active++

	return func() {
		ipMu.Lock()
		st.active--
		ipMu.Unlock()
	}, true
}
