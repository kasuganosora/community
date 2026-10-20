package serve

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/log"
)

// Failure classes of GET /proxy/media/:token. They appear in the warn log
// ("media proxy: failed ... class=<class>") and in the X-Media-Proxy-Error
// response header, so a 502 can be explained without the token or URL.
const (
	classDecrypt       = "decrypt"         // token does not open with the current or previous key
	classExpired       = "expired"         // token past its expiry
	classBadURL        = "bad_url"         // sealed URL is not absolute http(s)
	classBlocked       = "blocked_address" // SSRF guard: internal address, non-HTTP redirect, lab peer rule
	classDNS           = "dns"             // host does not resolve
	classTLS           = "tls"             // handshake or certificate failure
	classTimeout       = "timeout"         // no response headers in time (connect, TLS or server)
	classConnect       = "connect"         // connection refused, reset or unreachable
	classRedirects     = "redirects"       // too many redirects
	classFetch         = "fetch"           // any other transport or read error
	classUpstream      = "upstream_status" // origin answered a non-success status
	classContentType   = "content_type"    // not a safe raster image / audio / video
	classSizeLimit     = "size_limit"      // above MEDIA_PROXY_MAX_BYTES / stream limit
	classNegativeCache = "negative_cache"  // answered from the failure memo, origin not contacted
	classRateLimited   = "rate_limited"    // per-client or per-host request limit
	classBusy          = "busy"            // all proxy slots taken
)

// Failure memo lifetimes. A permanent answer (gone, wrong type, too large)
// is remembered long enough to shield the origin; a transient one (timeout,
// connection, DNS hiccup, 5xx, 429) only briefly, so a short outage does not
// keep media broken for minutes after the origin recovers.
const (
	permanentFailureTTL = 5 * time.Minute
	transientFailureTTL = 30 * time.Second
)

// failure is how a request ended when it did not relay media.
type failure struct {
	status   int           // status answered to the client
	class    string        // one of the class* constants
	upstream int           // origin status, for classUpstream
	ttl      time.Duration // failure memo lifetime; 0 = not memoised
}

// upstreamStatusError carries an origin status out of fetchImage.
type upstreamStatusError struct {
	code       int
	retryAfter string // origin Retry-After, for 429/503
}

func (e upstreamStatusError) Error() string { return fmt.Sprintf("upstream status %d", e.code) }

// upstreamFailure maps an origin status to the answer and memo lifetime.
// A missing or refused object (404/410, and the 401/403 that S3-style stores
// send for deleted keys) is a 404 for the browser, not a gateway error; 410
// is kept for our own expired links, which the web client renews.
func upstreamFailure(code int) failure {
	switch {
	case code == http.StatusNotFound || code == http.StatusGone || code == http.StatusUnauthorized || code == http.StatusForbidden:
		return failure{status: http.StatusNotFound, class: classUpstream, upstream: code, ttl: permanentFailureTTL}
	case code == http.StatusTooManyRequests:
		// The origin throttles us, the object itself is fine: never memoise
		// it per URL (that would keep every emoji of a busy profile broken);
		// the host cooldown honours the origin's Retry-After instead.
		return failure{status: http.StatusServiceUnavailable, class: classUpstream, upstream: code}
	case code >= 500:
		return failure{status: http.StatusBadGateway, class: classUpstream, upstream: code, ttl: transientFailureTTL}
	default:
		return failure{status: http.StatusBadGateway, class: classUpstream, upstream: code, ttl: permanentFailureTTL}
	}
}

// classifyFetchError names a transport error of the outbound request.
// timedOut reports that our own header deadline cancelled the request.
func classifyFetchError(err error, timedOut bool) failure {
	transient := func(class string) failure {
		status := http.StatusBadGateway
		if class == classTimeout {
			status = http.StatusGatewayTimeout
		}
		return failure{status: status, class: class, ttl: transientFailureTTL}
	}
	permanent := func(class string) failure {
		return failure{status: http.StatusBadGateway, class: class, ttl: permanentFailureTTL}
	}
	var upstream upstreamStatusError
	if errors.As(err, &upstream) {
		return upstreamFailure(upstream.code)
	}
	msg := err.Error()
	var dnsErr *net.DNSError
	switch {
	case strings.Contains(msg, "ssrf: blocked") || strings.Contains(msg, "ssrf: no addresses") ||
		strings.Contains(msg, "ssrf: lab") || strings.Contains(msg, "ssrf: invalid address") || strings.Contains(msg, "non-HTTP scheme"):
		return permanent(classBlocked)
	case strings.Contains(msg, "stopped after") && strings.Contains(msg, "redirects"):
		return permanent(classRedirects)
	case errors.As(err, &dnsErr):
		if dnsErr.IsNotFound {
			return permanent(classDNS)
		}
		return transient(classDNS)
	case timedOut || errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return transient(classTimeout)
	case isTLSError(err):
		return transient(classTLS)
	case isConnectError(err):
		return transient(classConnect)
	}
	return transient(classFetch)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func isTLSError(err error) bool {
	var (
		header   tls.RecordHeaderError
		verify   *tls.CertificateVerificationError
		alert    tls.AlertError
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
		invalid  x509.CertificateInvalidError
	)
	return errors.As(err, &header) || errors.As(err, &verify) || errors.As(err, &alert) ||
		errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &invalid) ||
		strings.Contains(err.Error(), "tls: ")
}

func isConnectError(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// Failure logging: one warn line per failure, at most once per host and
// class per window; the next line reports how many were suppressed. Only the
// hostname is logged, never the token, the key or the URL (paths and query
// strings of remote media can carry signatures).
const (
	failureLogWindow  = time.Minute
	maxFailureLogKeys = 10000
)

type failureLogEntry struct {
	last       time.Time
	suppressed int
}

var (
	failureLogMu sync.Mutex
	failureLog   = map[string]*failureLogEntry{}
	// logWarn is the sink; overridable in tests.
	logWarn = log.Warn
)

// shouldLogFailure applies the per host+class window. It returns whether to
// log now and how many lines were suppressed since the last one.
func shouldLogFailure(host, class string) (bool, int) {
	failureLogMu.Lock()
	defer failureLogMu.Unlock()
	key := host + "\x00" + class
	t := now()
	e := failureLog[key]
	if e == nil {
		if len(failureLog) >= maxFailureLogKeys {
			for k, v := range failureLog {
				if t.Sub(v.last) >= failureLogWindow {
					delete(failureLog, k)
				}
			}
			if len(failureLog) >= maxFailureLogKeys {
				return false, 0
			}
		}
		failureLog[key] = &failureLogEntry{last: t}
		return true, 0
	}
	if t.Sub(e.last) < failureLogWindow {
		e.suppressed++
		return false, 0
	}
	n := e.suppressed
	e.last, e.suppressed = t, 0
	return true, n
}

// answerFailure writes the failure response and logs it (rate limited).
// cached is the memo record when the answer came from the failure memo.
func answerFailure(c *gin.Context, host, kind string, started time.Time, f failure, cached *failureRecord) {
	header := f.class
	if cached != nil {
		header = classNegativeCache + "; " + cached.class
	}
	c.Header("X-Media-Proxy-Error", header)
	c.Header("Cache-Control", "no-store")
	if f.status == http.StatusServiceUnavailable || f.status == http.StatusTooManyRequests {
		if c.Writer.Header().Get("Retry-After") == "" {
			c.Header("Retry-After", "5")
		}
	}
	c.Status(f.status)
	logFailure(host, kind, started, f, cached)
}

func logFailure(host, kind string, started time.Time, f failure, cached *failureRecord) {
	if host == "" {
		host = "-"
	}
	class := f.class
	if cached != nil {
		class = classNegativeCache
	}
	ok, suppressed := shouldLogFailure(host, class)
	if !ok {
		return
	}
	line := "media proxy: failed host=" + host + " class=" + class + " status=" + strconv.Itoa(f.status)
	if cached != nil {
		line += " cached_class=" + cached.class
		if cached.upstream != 0 {
			line += " upstream_status=" + strconv.Itoa(cached.upstream)
		}
		line += " ttl_left=" + cached.ttlLeft().Round(time.Second).String()
	} else if f.upstream != 0 {
		line += " upstream_status=" + strconv.Itoa(f.upstream)
	}
	if kind != "" {
		line += " kind=" + kind
	}
	line += " duration_ms=" + strconv.FormatInt(now().Sub(started).Milliseconds(), 10)
	if cached == nil && f.ttl > 0 {
		line += " memo_ttl=" + f.ttl.String()
	}
	if suppressed > 0 {
		line += " suppressed=" + strconv.Itoa(suppressed)
	}
	logWarn("%s", line)
}
