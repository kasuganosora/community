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
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	connectTimeout = 5 * time.Second
	maxRedirects   = 3
	maxConcurrent  = 32
	acquireWait    = 3 * time.Second
	hostRate       = rate.Limit(20)
	hostBurst      = 40
	maxAge         = 86400 // seconds, capped by the token's remaining life
	ipRate         = rate.Limit(40)
	ipBurst        = 120
	ipConcurrent   = 12
	maxIPKeys      = 20000
	sandboxPolicy  = "default-src 'none'; style-src 'unsafe-inline'; sandbox"
	maxFailureKeys = 5000
	hostConcurrent = 4                // simultaneous origin fetches per host
	maxCooldown    = 60 * time.Second // cap on an origin's Retry-After
)

// hostWaitBound is how long a cache miss may queue for a paused or busy
// origin before the proxy answers 503. Overridable in tests.
var hostWaitBound = 12 * time.Second

// Overridable in tests.
var (
	fetchTimeout  = 15 * time.Second // whole download of a cached avatar
	headerTimeout = 10 * time.Second // relay: connect + TLS + response headers
	stallTimeout  = 15 * time.Second // relay: no upstream bytes for this long

	// NewClient builds the outbound client; it must pin dialing to public
	// addresses, bound redirects and check every redirect target.
	NewClient = func() *http.Client {
		return activitypub.NewSafeHTTPClientWithConnectTimeout(0, maxRedirects, connectTimeout)
	}
	// sharedFailures is the cross-process failure memo (Redis), nil when
	// unavailable. Overridable in tests.
	sharedFailures = redisFailures
	// Validate is the SSRF pre-check of a source URL.
	Validate = activitypub.ValidateURI
	now      = time.Now
)

var (
	slots    = make(chan struct{}, maxConcurrent)
	failures sync.Map // cache key -> failureRecord
	failN    int64
	failMu   sync.Mutex
	limiters sync.Map // host -> *rate.Limiter
	flights  sync.Map // cache key -> *sync.Mutex
)

var (
	hostSlots sync.Map // host -> chan struct{}
	cooldowns sync.Map // host -> time.Time until which the origin asked us to wait
)

// hostAcquire bounds simultaneous fetches from one origin, so a profile with
// dozens of emoji does not burst a remote server into rate limiting us.
func hostAcquire(ctx context.Context, host string) (func(), bool) {
	return hostAcquireWithin(ctx, host, acquireWait)
}

func hostAcquireWithin(ctx context.Context, host string, wait time.Duration) (func(), bool) {
	v, _ := hostSlots.LoadOrStore(host, make(chan struct{}, hostConcurrent))
	ch := v.(chan struct{})
	if wait <= 0 {
		wait = time.Millisecond
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	case <-t.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

// hostCooling returns the seconds left of an origin's Retry-After, if any.
func hostCooling(host string) (string, bool) {
	v, ok := cooldowns.Load(host)
	if !ok {
		return "", false
	}
	left := v.(time.Time).Sub(now())
	if left <= 0 {
		cooldowns.Delete(host)
		return "", false
	}
	return strconv.Itoa(int((left + time.Second - 1) / time.Second)), true
}

// waitCooldown blocks while host is paused (its Retry-After), as long as
// the pause ends before deadline. It reports false when the pause outlasts
// the deadline or ctx ends.
func waitCooldown(ctx context.Context, host string, deadline time.Time) bool {
	v, ok := cooldowns.Load(host)
	if !ok {
		return true
	}
	until := v.(time.Time)
	left := until.Sub(now())
	if left <= 0 {
		return true
	}
	if until.After(deadline) {
		return false
	}
	t := time.NewTimer(left)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// coolHost records an origin's 429 Retry-After (default 5s, capped).
func coolHost(host, retryAfterHeader string) string {
	secs := 5
	if n, err := strconv.Atoi(strings.TrimSpace(retryAfterHeader)); err == nil && n > 0 {
		secs = n
	} else if t, err := http.ParseTime(retryAfterHeader); err == nil {
		secs = max(int(t.Sub(now())/time.Second), 1)
	}
	d := min(time.Duration(secs)*time.Second, maxCooldown)
	cooldowns.Store(host, now().Add(d))
	return strconv.Itoa(int(d / time.Second))
}

func hostAllowed(host string) bool {
	l, _ := limiters.LoadOrStore(host, rate.NewLimiter(hostRate, hostBurst))
	return l.(*rate.Limiter).Allow()
}

// failureRecord is a memoised failure: the answer to repeat and when the
// origin may be tried again.
type failureRecord struct {
	status   int
	class    string
	upstream int
	expires  time.Time
}

func (r failureRecord) ttlLeft() time.Duration { return max(r.expires.Sub(now()), 0) }

func (r failureRecord) failure() failure {
	return failure{status: r.status, class: r.class, upstream: r.upstream}
}

// markFailed remembers f for its ttl (locally and in the shared memo), so
// repeated requests are answered the same way without contacting the origin.
func markFailed(key string, f failure) {
	if f.ttl <= 0 {
		return
	}
	rec := failureRecord{status: f.status, class: f.class, upstream: f.upstream, expires: now().Add(f.ttl)}
	if s := sharedFailures(); s != nil {
		s.mark(key, rec)
	}
	markFailedLocal(key, rec)
}

func markFailedLocal(key string, rec failureRecord) {
	failMu.Lock()
	defer failMu.Unlock()
	if failN >= maxFailureKeys {
		failures.Range(func(k, v any) bool {
			if now().After(v.(failureRecord).expires) {
				failures.Delete(k)
				failN--
			}
			return true
		})
		if failN >= maxFailureKeys {
			return
		}
	}
	if _, loaded := failures.Swap(key, rec); !loaded {
		failN++
	}
}

func recentlyFailed(key string) (failureRecord, bool) {
	if rec, ok := recentlyFailedLocal(key); ok {
		return rec, true
	}
	if s := sharedFailures(); s != nil {
		if rec, ok := s.failed(key); ok {
			markFailedLocal(key, rec)
			return rec, true
		}
	}
	return failureRecord{}, false
}

func recentlyFailedLocal(key string) (failureRecord, bool) {
	v, ok := failures.Load(key)
	if !ok {
		return failureRecord{}, false
	}
	rec := v.(failureRecord)
	if now().After(rec.expires) {
		failMu.Lock()
		if _, ok := failures.LoadAndDelete(key); ok {
			failN--
		}
		failMu.Unlock()
		return failureRecord{}, false
	}
	return rec, true
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
	started := now()
	claims, err := mediaproxy.Open(c.Param("token"))
	switch {
	case errors.Is(err, mediaproxy.ErrExpired):
		c.Header("Cache-Control", "no-store")
		c.Header("X-Media-Proxy-Error", classExpired)
		c.String(http.StatusGone, "media link expired; reload the post to get a new one")
		logFailure(hostOf(claims.URL), claims.Kind, started, failure{status: http.StatusGone, class: classExpired}, nil)
		return
	case err != nil:
		// Nothing in an unopened token is trusted, not even the kind byte.
		c.Header("Cache-Control", "no-store")
		c.Header("X-Media-Proxy-Error", classDecrypt)
		c.String(http.StatusForbidden, "invalid media link")
		logFailure("", "", started, failure{status: http.StatusForbidden, class: classDecrypt}, nil)
		return
	}
	kind, rawURL := claims.Kind, claims.URL
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		answerFailure(c, hostOf(rawURL), kind, started, failure{status: http.StatusBadRequest, class: classBadURL}, nil)
		return
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Header("Allow", "GET, HEAD")
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	host := strings.ToLower(u.Hostname())
	key := failureKey(rawURL)
	// A stored avatar/emoji is answered before any failure memo, rate
	// limit or origin pause: those only guard fetches from the origin.
	stale := false
	if cacheKey, size, _, ok := cachedRendition(c, kind, rawURL); ok {
		if cache := avatarCache(); cache != nil {
			if e, fresh, found := cache.Peek(cacheKey, size, avatarcache.RemoteTTL()); found {
				if fresh && serveEntry(c, e, claims) {
					return
				}
				stale = true
			}
		}
	}
	if rec, ok := recentlyFailed(key); ok {
		if stale && serveStale(c, kind, rawURL, claims) {
			return
		}
		answerFailure(c, host, kind, started, rec.failure(), &rec)
		return
	}
	if err := Validate(rawURL); err != nil {
		f := classifyFetchError(err, false)
		if f.class != classDNS && f.class != classTimeout {
			// The SSRF guard refused the address itself: the link is not
			// proxyable, which is the client's request, not a gateway error.
			f = failure{status: http.StatusBadRequest, class: classBlocked}
		}
		markFailed(key, f)
		answerFailure(c, host, kind, started, f, nil)
		return
	}
	ip := c.ClientIP()
	if !ipAllowed(ip) {
		if stale && serveStale(c, kind, rawURL, claims) {
			return
		}
		answerFailure(c, host, kind, started, failure{status: http.StatusTooManyRequests, class: classRateLimited}, nil)
		return
	}
	if cacheKey, size, render, ok := cachedRendition(c, kind, rawURL); ok && serveCached(c, key, rawURL, host, kind, claims, started, cacheKey, size, render) {
		return
	}
	if !waitCooldown(c.Request.Context(), host, now().Add(hostWaitBound)) {
		wait, _ := hostCooling(host)
		if wait == "" {
			wait = "2"
		}
		c.Header("Retry-After", wait)
		answerFailure(c, host, kind, started, failure{status: http.StatusServiceUnavailable, class: classRateLimited}, nil)
		return
	}
	if !hostAllowed(host) {
		answerFailure(c, host, kind, started, failure{status: http.StatusTooManyRequests, class: classRateLimited}, nil)
		return
	}
	release, ok := acquire(c.Request.Context())
	if !ok {
		c.Header("Retry-After", "2")
		answerFailure(c, host, kind, started, failure{status: http.StatusServiceUnavailable, class: classBusy}, nil)
		return
	}
	defer release()
	releaseIP, ok := ipAcquire(ip)
	if !ok {
		c.Header("Retry-After", "2")
		answerFailure(c, host, kind, started, failure{status: http.StatusTooManyRequests, class: classRateLimited}, nil)
		return
	}
	defer releaseIP()
	releaseHost, ok := hostAcquire(c.Request.Context(), host)
	if !ok {
		c.Header("Retry-After", "2")
		answerFailure(c, host, kind, started, failure{status: http.StatusServiceUnavailable, class: classBusy}, nil)
		return
	}
	defer releaseHost()

	relay(c, key, rawURL, host, kind, claims, started)
}

// hostOf returns the lower-case hostname of raw (no port, path or query).
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
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
func relay(c *gin.Context, key, rawURL, host, kind string, claims mediaproxy.Claims, started time.Time) {
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	var timedOut atomic.Bool
	timer := time.AfterFunc(headerTimeout, func() { timedOut.Store(true); cancel() })
	fail := func(f failure) {
		markFailed(key, f)
		answerFailure(c, host, kind, started, f, nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		timer.Stop()
		answerFailure(c, host, kind, started, failure{status: http.StatusBadRequest, class: classBadURL}, nil)
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
		if c.Request.Context().Err() != nil && !timedOut.Load() {
			// The visitor went away: not the origin's fault, remember nothing.
			log.Debug("media proxy: client gone before the origin answered host=%s", host)
			c.Status(http.StatusBadGateway)
			return
		}
		fail(classifyFetchError(err, timedOut.Load()))
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
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		c.Header("Content-Range", resp.Header.Get("Content-Range"))
		c.Status(http.StatusRequestedRangeNotSatisfiable)
		return
	case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent:
		f := upstreamFailure(resp.StatusCode)
		if resp.StatusCode == http.StatusTooManyRequests {
			c.Header("Retry-After", coolHost(host, resp.Header.Get("Retry-After")))
		} else if f.status == http.StatusServiceUnavailable {
			c.Header("Retry-After", retryAfter(resp.Header.Get("Retry-After")))
		}
		fail(f)
		return
	}

	declared := upstreamType(resp.Header)
	var src io.Reader = resp.Body
	if strings.HasPrefix(declared, "image/") && resp.ContentLength < 0 && resp.StatusCode == http.StatusOK {
		// Without a length the cap cannot be enforced after the headers are
		// sent, so such (rare) images are read in full, bounded, first.
		data, err := io.ReadAll(io.LimitReader(resp.Body, mediaproxy.MaxBytes()+1))
		if err != nil {
			if c.Request.Context().Err() != nil && !timedOut.Load() {
				c.Status(http.StatusBadGateway)
				return
			}
			fail(classifyFetchError(err, timedOut.Load()))
			return
		}
		if int64(len(data)) > mediaproxy.MaxBytes() {
			fail(failure{status: http.StatusRequestEntityTooLarge, class: classSizeLimit, ttl: permanentFailureTTL})
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
			fail(failure{status: http.StatusUnsupportedMediaType, class: classContentType, ttl: permanentFailureTTL})
			return
		}
		// The first bytes must be a raster image; a body that merely claims
		// to be one (HTML, SVG, scripts) is refused. A range that does not
		// start at the beginning has no header to check.
		if resp.StatusCode == http.StatusOK || strings.HasPrefix(resp.Header.Get("Content-Range"), "bytes 0-") {
			head, _ := body.Peek(512)
			if ctype = sniffImage(head); ctype == "" {
				fail(failure{status: http.StatusUnsupportedMediaType, class: classContentType, ttl: permanentFailureTTL})
				return
			}
		}
	case strings.HasPrefix(declared, "video/") || strings.HasPrefix(declared, "audio/"):
	default:
		fail(failure{status: http.StatusUnsupportedMediaType, class: classContentType, ttl: permanentFailureTTL})
		return
	}
	if resp.ContentLength > limit {
		fail(failure{status: http.StatusRequestEntityTooLarge, class: classSizeLimit, ttl: permanentFailureTTL})
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
	stall := time.AfterFunc(stallTimeout, cancel)
	defer stall.Stop()
	n, err := io.Copy(c.Writer, &stallReader{r: io.LimitReader(body, limit), timer: stall})
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Debug("media proxy: stream aborted after %d bytes: %v", n, err)
	}
}

// retryAfter relays a short numeric Retry-After of a rate-limited origin.
func retryAfter(v string) string {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 && n <= 60 {
		return strconv.Itoa(n)
	}
	return "30"
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

// errCooling: the origin was paused while this fetch queued; wait again.
var errCooling = errors.New("origin paused")

// avatarCache is the cache used for avatars; overridable in tests.
var avatarCache = avatarcache.Default

// cachedRendition names the disk-cache rendition for avatars (resized, or
// the first-frame still with ?static=1) and custom emoji (byte for byte:
// emoji may be wide; origins such as misskey.io rate-limit refetching
// dozens of them per profile view). ok is false for other kinds.
func cachedRendition(c *gin.Context, kind, rawURL string) (cacheKey string, size int, render func([]byte, int) ([]byte, string, error), ok bool) {
	switch kind {
	case mediaproxy.KindAvatar:
		size = avatarcache.NormalizeSize(c.Query("size"))
		if c.Query("static") == "1" {
			return "remote-static|" + rawURL, size, avatarcache.RenderStatic, true
		}
		return "remote|" + rawURL, size, avatarcache.Render, true
	case mediaproxy.KindEmoji:
		return "emoji|" + rawURL, 0, avatarcache.Passthrough, true
	}
	return "", 0, nil, false
}

// serveStale answers a stored (expired) rendition, if there still is one.
func serveStale(c *gin.Context, kind, rawURL string, claims mediaproxy.Claims) bool {
	cache := avatarCache()
	cacheKey, size, _, ok := cachedRendition(c, kind, rawURL)
	if cache == nil || !ok {
		return false
	}
	e, _, found := cache.Peek(cacheKey, size, 0)
	return found && serveEntry(c, e, claims)
}

// serveEntry writes a cached file; false when it vanished (evicted).
func serveEntry(c *gin.Context, entry *avatarcache.Entry, claims mediaproxy.Claims) bool {
	f, err := os.Open(entry.Path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	setCommonHeaders(c, entry.ContentType)
	c.Header("Cache-Control", cacheControlFor(claims))
	http.ServeContent(c.Writer, c.Request, "", entry.ModTime, f)
	return true
}

func serveCached(c *gin.Context, key, rawURL, host, kind string, claims mediaproxy.Claims, started time.Time, cacheKey string, size int, render func([]byte, int) ([]byte, string, error)) bool {
	cache := avatarCache()
	if cache == nil {
		return false
	}
	entry, err := cache.GetRendered(c.Request.Context(), cacheKey, size, avatarcache.RemoteTTL(), func(ctx context.Context) ([]byte, error) {
		// Queue (bounded) behind a paused or busy origin instead of failing
		// at once; a 429 while queued pauses the host and waits again.
		deadline := now().Add(hostWaitBound)
		for {
			if !waitCooldown(ctx, host, deadline) || !hostAllowed(host) {
				return nil, errBusy
			}
			data, err := func() ([]byte, error) {
				release, ok := acquire(ctx)
				if !ok {
					return nil, errBusy
				}
				defer release()
				releaseHost, ok := hostAcquireWithin(ctx, host, deadline.Sub(now()))
				if !ok {
					return nil, errBusy
				}
				defer releaseHost()
				if _, cooling := hostCooling(host); cooling {
					return nil, errCooling // paused while we queued
				}
				return fetchImage(ctx, rawURL)
			}()
			var up upstreamStatusError
			if errors.As(err, &up) && up.code == http.StatusTooManyRequests {
				coolHost(host, up.retryAfter)
				continue
			}
			if errors.Is(err, errCooling) {
				continue
			}
			return data, err
		}
	}, render)
	if err != nil {
		var f failure
		switch {
		case errors.Is(err, errBusy):
			wait := "2"
			if w, cooling := hostCooling(host); cooling {
				wait = w
			}
			c.Header("Retry-After", wait)
			answerFailure(c, host, kind, started, failure{status: http.StatusServiceUnavailable, class: classBusy}, nil)
			return true
		case errors.Is(err, avatarcache.ErrTooLarge):
			f = failure{status: http.StatusRequestEntityTooLarge, class: classSizeLimit, ttl: permanentFailureTTL}
		case errors.Is(err, avatarcache.ErrInvalidType):
			// A decode failure may be our decoder's limit, not a bad file:
			// remember it only briefly so a fixed build serves it at once.
			f = failure{status: http.StatusUnsupportedMediaType, class: classContentType, ttl: transientFailureTTL}
		default:
			// The fetch is detached from this request, so a cancellation
			// here is the fetch deadline, not the visitor leaving.
			f = classifyFetchError(err, errors.Is(err, context.Canceled))
		}
		markFailed(key, f)
		answerFailure(c, host, kind, started, f, nil)
		return true
	}
	return serveEntry(c, entry, claims) // false: evicted between lookup and open
}

// fetchImage downloads a raster image source in full, bounded by
// MEDIA_PROXY_MAX_BYTES and the avatar cache's own limit.
func fetchImage(ctx context.Context, rawURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
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
	if resp.StatusCode != http.StatusOK {
		return nil, upstreamStatusError{code: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After")}
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
