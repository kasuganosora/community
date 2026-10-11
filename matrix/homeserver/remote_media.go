package homeserver

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/log"
	"github.com/matrix-org/gomatrixserverlib/spec"
)

// Remote Matrix media is always fetched by this server and streamed to the
// client; a client is never redirected to the origin server (it may be
// unreachable from the user's network). Order of attempts for mxc://S/ID:
//
//  1. signed federation request /_matrix/federation/v1/media/{download,thumbnail}
//     against the destination found by .well-known / SRV delegation. The
//     multipart answer carries either the bytes or a Location (CDN) part,
//     which is followed here.
//  2. legacy /_matrix/media/v3 then /r0 on the same federation destination,
//  3. the same legacy paths on S's client API (/.well-known/matrix/client).
//
// A thumbnail that cannot be produced remotely falls back to the original.
// Nothing is cached.
//
// Environment:
//
//	MATRIX_REMOTE_MEDIA_MAX_BYTES        largest remote file streamed (default 52428800)
//	MATRIX_REMOTE_MEDIA_TIMEOUT_SECONDS  whole-request deadline, default 120
const (
	remoteMediaHeaderTimeout  = 20 * time.Second
	defaultRemoteMediaTimeout = 120 * time.Second
)

func remoteMediaMaxBytes() int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("MATRIX_REMOTE_MEDIA_MAX_BYTES")), 10, 64); err == nil && n > 0 {
		return n
	}
	return matrixMediaLimit
}

func remoteMediaTimeout() time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("MATRIX_REMOTE_MEDIA_TIMEOUT_SECONDS"))); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return defaultRemoteMediaTimeout
}

var (
	errRemoteMediaTooLarge = errors.New("remote media too large")
	serverNameRe           = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:[0-9]{1,5})?$|^\[[0-9A-Fa-f:.]+\](:[0-9]{1,5})?$`)
	mediaIDRe              = regexp.MustCompile(`^[A-Za-z0-9_\-.=+~]{1,255}$`)
)

// remoteMedia is an open, size-bounded remote body.
type remoteMedia struct {
	Body        io.ReadCloser
	ContentType string
	Filename    string
	Length      int64 // -1 when unknown
}

func (m *remoteMedia) Close() error { return m.Body.Close() }

type limitedMediaBody struct {
	r         io.Reader
	remaining int64
	closers   []io.Closer
}

func (l *limitedMediaBody) Read(p []byte) (int, error) {
	if l.remaining < 0 {
		return 0, errRemoteMediaTooLarge
	}
	if int64(len(p)) > l.remaining+1 {
		p = p[:l.remaining+1]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	if l.remaining < 0 {
		return n, errRemoteMediaTooLarge
	}
	return n, err
}

func (l *limitedMediaBody) Close() error {
	var first error
	for _, c := range l.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// mediaDiscoveryTransport routes a request addressed to a logical Matrix
// server name to the host found by well-known / SRV discovery, keeping the
// logical name as Host and verifying TLS against the delegated name. Every
// connection goes through the SSRF-checked dialer.
type mediaDiscoveryTransport struct {
	discovery *federationDiscoveryTransport
	tlsConfig *tls.Config
	mu        sync.Mutex
	pools     map[string]http.RoundTripper
}

func (t *mediaDiscoveryTransport) pool(tlsName string) http.RoundTripper {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p := t.pools[tlsName]; p != nil {
		return p
	}
	if len(t.pools) >= 128 {
		clear(t.pools)
	}
	cfg := t.tlsConfig.Clone()
	cfg.ServerName = tlsName
	p := activitypub.NewSafeRoundTripper(cfg, remoteMediaHeaderTimeout)
	t.pools[tlsName] = p
	return p
}

func (t *mediaDiscoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	server := req.Header.Get("Host")
	if server == "" {
		server = req.URL.Host
	}
	results, err := t.discovery.destinations(req.Context(), spec.ServerName(server))
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, result := range results {
		attempt := req.Clone(req.Context())
		attempt.URL.Scheme = "https"
		attempt.URL.Host = result.Destination
		attempt.Host = string(result.Host)
		attempt.Header.Del("Host")
		resp, err := t.pool(result.TLSServerName).RoundTrip(attempt)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if req.Context().Err() != nil {
			break
		}
	}
	t.discovery.mu.Lock()
	delete(t.discovery.resolutions, spec.ServerName(server))
	t.discovery.mu.Unlock()
	return nil, lastErr
}

type remoteMediaFetcher struct {
	fed     *FederationClient
	disc    *http.Client // discovery-routed, never follows redirects
	direct  *http.Client // SSRF-checked, follows a few redirects (CDN / client API)
	timeout time.Duration
}

var (
	remoteFetcherMu   sync.Mutex
	remoteFetcherByHS = map[*HomeServer]*remoteMediaFetcher{}
)

func (hs *HomeServer) remoteMedia() *remoteMediaFetcher {
	remoteFetcherMu.Lock()
	defer remoteFetcherMu.Unlock()
	if f := remoteFetcherByHS[hs]; f != nil {
		return f
	}
	tlsConfig := federationTLSConfig("/complement/ca/ca.crt")
	fed := hs.mediaFederation()
	f := &remoteMediaFetcher{
		fed:     fed,
		disc:    newMediaDiscoveryClient(tlsConfig),
		direct:  activitypub.NewMediaFetchHTTPClient(remoteMediaHeaderTimeout),
		timeout: remoteMediaTimeout(),
	}
	remoteFetcherByHS[hs] = f
	return f
}

func newMediaDiscoveryClient(tlsConfig *tls.Config) *http.Client {
	return &http.Client{
		Transport:     &mediaDiscoveryTransport{discovery: sharedFederationDiscoveryTransport(tlsConfig), tlsConfig: tlsConfig, pools: map[string]http.RoundTripper{}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func validRemoteMediaTarget(server, id string) error {
	if !serverNameRe.MatchString(server) || len(server) > 255 || !mediaIDRe.MatchString(id) || id == "." || id == ".." {
		return errors.New("invalid media server name or id")
	}
	return nil
}

// Open returns the remote media for kind "download" or "thumbnail".
// cancel must be called when the caller is done with the body.
func (f *remoteMediaFetcher) Open(ctx context.Context, server, id, kind string, params url.Values) (*remoteMedia, context.CancelFunc, error) {
	if err := validRemoteMediaTarget(server, id); err != nil {
		return nil, func() {}, err
	}
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	m, err := f.openKind(ctx, server, id, kind, params)
	if err != nil && kind == "thumbnail" && ctx.Err() == nil {
		// Servers that cannot thumbnail (or reject the size) still have the
		// original, which the client scales itself.
		var orig *remoteMedia
		var err2 error
		if orig, err2 = f.openKind(ctx, server, id, "download", nil); err2 == nil {
			m, err = orig, nil
		} else {
			err = fmt.Errorf("%w; original: %v", err, err2)
		}
	}
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	return m, cancel, nil
}

func (f *remoteMediaFetcher) openKind(ctx context.Context, server, id, kind string, params url.Values) (*remoteMedia, error) {
	query := ""
	if len(params) > 0 {
		query = "?" + params.Encode()
	}
	var errs []string
	m, err := f.openFederation(ctx, server, id, kind, query)
	if err == nil {
		return m, nil
	}
	errs = append(errs, "federation: "+err.Error())
	legacyQuery := url.Values{"allow_remote": {"false"}}
	for k, v := range params {
		legacyQuery[k] = v
	}
	for _, ver := range []string{"v3", "r0"} {
		if ctx.Err() != nil {
			break
		}
		path := "/_matrix/media/" + ver + "/" + kind + "/" + url.PathEscape(server) + "/" + url.PathEscape(id) + "?" + legacyQuery.Encode()
		if m, err = f.openLegacy(ctx, server, path, true); err == nil {
			return m, nil
		}
		errs = append(errs, "legacy-"+ver+": "+err.Error())
	}
	if base := f.clientBaseURL(ctx, server); base != "" {
		for _, ver := range []string{"v3", "r0"} {
			if ctx.Err() != nil {
				break
			}
			path := "/_matrix/media/" + ver + "/" + kind + "/" + url.PathEscape(server) + "/" + url.PathEscape(id) + "?" + legacyQuery.Encode()
			if m, err = f.openURL(ctx, f.direct, base+path); err == nil {
				return m, nil
			}
			errs = append(errs, "client-"+ver+": "+err.Error())
		}
	}
	return nil, errors.New(strings.Join(errs, "; "))
}

func (f *remoteMediaFetcher) openFederation(ctx context.Context, server, id, kind, query string) (*remoteMedia, error) {
	base := f.fed.federationBaseURL(server)
	target := base + "/_matrix/federation/v1/media/" + kind + "/" + url.PathEscape(id) + query
	req, err := f.fed.prepareSignedFederationRequest(ctx, http.MethodGet, target, server, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.disc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	typ, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || typ != "multipart/mixed" || params["boundary"] == "" {
		_ = resp.Body.Close()
		return nil, errors.New("not a multipart media response")
	}
	mr := multipart.NewReader(resp.Body, params["boundary"])
	meta, err := mr.NextPart()
	if err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(meta, 65537))
	if err != nil || len(raw) > 65536 || !json.Valid(raw) {
		_ = resp.Body.Close()
		return nil, errors.New("invalid media metadata")
	}
	body, err := mr.NextPart()
	if err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	if loc := body.Header.Get("Location"); loc != "" {
		_ = resp.Body.Close()
		u, perr := url.Parse(loc)
		if perr != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
			return nil, errors.New("invalid redirect location")
		}
		return f.openURL(ctx, f.direct, loc)
	}
	_, disp, _ := mime.ParseMediaType(body.Header.Get("Content-Disposition"))
	return &remoteMedia{
		Body:        &limitedMediaBody{r: body, remaining: remoteMediaMaxBytes(), closers: []io.Closer{resp.Body}},
		ContentType: body.Header.Get("Content-Type"),
		Filename:    cleanRemoteFilename(disp["filename"]),
		Length:      -1,
	}, nil
}

// openLegacy requests an unauthenticated legacy path on the federation
// destination of server.
func (f *remoteMediaFetcher) openLegacy(ctx context.Context, server, path string, _ bool) (*remoteMedia, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+server+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Community-Matrix/1.0")
	req.Header.Set("Host", server)
	// The federation port is implied by discovery; the placeholder authority
	// only has to be parseable.
	req.URL.Host = server
	return f.finish(ctx, f.disc, req)
}

func (f *remoteMediaFetcher) openURL(ctx context.Context, client *http.Client, target string) (*remoteMedia, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Community-Matrix/1.0")
	return f.finish(ctx, client, req)
}

func (f *remoteMediaFetcher) finish(ctx context.Context, client *http.Client, req *http.Request) (*remoteMedia, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	// A legacy endpoint on the discovery client may redirect (CDN): follow it
	// through the SSRF-checked client.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" && client != f.direct {
		loc, perr := req.URL.Parse(resp.Header.Get("Location"))
		_ = resp.Body.Close()
		if perr != nil || loc.User != nil || (loc.Scheme != "https" && loc.Scheme != "http") {
			return nil, errors.New("invalid redirect location")
		}
		return f.openURL(ctx, f.direct, loc.String())
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	limit := remoteMediaMaxBytes()
	if resp.ContentLength > limit {
		_ = resp.Body.Close()
		return nil, errRemoteMediaTooLarge
	}
	_, disp, _ := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	return &remoteMedia{
		Body:        &limitedMediaBody{r: resp.Body, remaining: limit, closers: []io.Closer{resp.Body}},
		ContentType: resp.Header.Get("Content-Type"),
		Filename:    cleanRemoteFilename(disp["filename"]),
		Length:      resp.ContentLength,
	}, nil
}

// clientBaseURL reads https://server/.well-known/matrix/client.
func (f *remoteMediaFetcher) clientBaseURL(ctx context.Context, server string) string {
	if strings.Contains(server, ":") && !strings.HasPrefix(server, "[") {
		host, _, _ := strings.Cut(server, ":")
		server = host
	}
	m, err := f.openURL(ctx, f.direct, "https://"+server+"/.well-known/matrix/client")
	if err != nil {
		return ""
	}
	defer m.Close()
	raw, err := io.ReadAll(io.LimitReader(m.Body, 65536))
	if err != nil {
		return ""
	}
	var doc struct {
		HS struct {
			BaseURL string `json:"base_url"`
		} `json:"m.homeserver"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	u, err := url.Parse(strings.TrimRight(doc.HS.BaseURL, "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && u.Path != "/") {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func cleanRemoteFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// safeInlineType reports whether a response of this type may be shown inline.
func safeInlineType(ct string) bool {
	typ, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	switch {
	case typ == "image/svg+xml":
		return false
	case strings.HasPrefix(typ, "image/"), strings.HasPrefix(typ, "video/"), strings.HasPrefix(typ, "audio/"):
		return true
	}
	return typ == "text/plain" || typ == "application/pdf"
}

// serveRemoteMedia streams m to the client, sniffing the type when the remote
// did not give a useful one.
func serveRemoteMedia(c *gin.Context, m *remoteMedia, thumbnail bool) {
	br := bufio.NewReaderSize(m.Body, 4096)
	prefix, _ := br.Peek(512)
	ct := m.ContentType
	if typ, _, err := mime.ParseMediaType(ct); err != nil || typ == "" || typ == "application/octet-stream" || typ == "binary/octet-stream" {
		ct = "application/octet-stream"
		if sniffed := http.DetectContentType(prefix); safeInlineType(sniffed) {
			ct = sniffed
		}
	}
	if thumbnail && !safeInlineType(ct) {
		ct = "application/octet-stream"
	}
	h := c.Writer.Header()
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; script-src 'none'; plugin-types application/pdf; style-src 'unsafe-inline'; object-src 'self'")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	h.Set("Cache-Control", "private, max-age=86400")
	disposition := "attachment"
	if safeInlineType(ct) {
		disposition = "inline"
	}
	if m.Filename != "" && !thumbnail {
		h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": m.Filename}))
	} else {
		h.Set("Content-Disposition", disposition)
	}
	if m.Length >= 0 {
		h.Set("Content-Length", strconv.FormatInt(m.Length, 10))
	}
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, br); err != nil {
		log.Warn("remote matrix media stream interrupted: %v", err)
	}
}

// serveRemoteMatrixMedia is the single entry point for every endpoint that is
// asked for media of another server.
func serveRemoteMatrixMedia(c *gin.Context, serverName, mediaID, kind string, params url.Values) {
	hs := GetHomeServer()
	m, cancel, err := hs.remoteMedia().Open(c.Request.Context(), serverName, mediaID, kind, params)
	if err != nil {
		log.Warn("remote matrix media %s mxc://%s/%s failed: %v", kind, serverName, mediaID, err)
		code, errcode := http.StatusNotFound, "M_NOT_FOUND"
		if errors.Is(err, errRemoteMediaTooLarge) {
			code, errcode = http.StatusBadGateway, "M_TOO_LARGE"
		}
		c.JSON(code, gin.H{"errcode": errcode, "error": "Remote media not available"})
		return
	}
	defer cancel()
	defer m.Close()
	serveRemoteMedia(c, m, kind == "thumbnail")
}

// thumbnailParams validates the client's thumbnail query like the spec.
func thumbnailParams(c *gin.Context) (url.Values, bool) {
	w, h := 320, 240
	if s := c.Query("width"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 4096 {
			return nil, false
		}
		w = n
	}
	if s := c.Query("height"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 4096 {
			return nil, false
		}
		h = n
	}
	method := c.Query("method")
	if method != "crop" && method != "scale" {
		method = "scale"
	}
	return url.Values{"width": {strconv.Itoa(w)}, "height": {strconv.Itoa(h)}, "method": {method}}, true
}

// handleLegacyMediaFetch serves /_matrix/media/{v1,v3,r0}/{download,thumbnail}.
// Local media stays on authenticated endpoints only; other servers' media is
// fetched for clients that present a valid access token.
func handleLegacyMediaFetch(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		hs := GetHomeServer()
		server := c.Param("serverName")
		if hs == nil || server == hs.Config.ServerName {
			handleLegacyMediaDownload(c)
			return
		}
		if c.Query("allow_remote") == "false" {
			c.JSON(http.StatusNotFound, gin.H{"errcode": "M_NOT_FOUND", "error": "Remote media not requested"})
			return
		}
		matrixAuthMiddleware()(c)
		if c.IsAborted() {
			return
		}
		fetchRemoteForClient(c, server, c.Param("mediaId"), kind)
	}
}

func fetchRemoteForClient(c *gin.Context, server, mediaID, kind string) {
	var params url.Values
	if kind == "thumbnail" {
		var ok bool
		if params, ok = thumbnailParams(c); !ok {
			c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_INVALID_PARAM", "error": "Invalid thumbnail dimensions"})
			return
		}
	}
	serveRemoteMatrixMedia(c, server, mediaID, kind, params)
}
