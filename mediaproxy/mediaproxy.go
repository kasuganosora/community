// Package mediaproxy signs URLs of remote media so browsers fetch them from
// this server (see mediaproxy/serve) instead of from the remote one. The
// signature binds the kind and the URL to a server secret, which keeps the
// endpoint from working as an open proxy.
package mediaproxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/systemsettings"
)

// Kinds a proxied URL can be requested as. They select cache and size policy
// and are part of the signature.
const (
	KindAvatar  = "avatar"
	KindHeader  = "header"
	KindMedia   = "media"
	KindEmoji   = "emoji"
	KindPreview = "preview"
)

// PathPrefix is where the proxy is mounted.
const PathPrefix = "/proxy/media/"

const (
	maxSourceURL   = 2000
	sigBytes       = 16
	defaultMaxSize = 20 << 20
	defaultStream  = 512 << 20
)

// ValidKind reports whether kind is one of the known kinds.
func ValidKind(kind string) bool {
	switch kind {
	case KindAvatar, KindHeader, KindMedia, KindEmoji, KindPreview:
		return true
	}
	return false
}

// Enabled reports whether remote media is proxied (MEDIA_PROXY_ENABLED,
// default true).
func Enabled() bool {
	v := strings.ToLower(strings.TrimSpace(config.C.MediaProxyEnabled))
	return v == "" || !(v == "false" || v == "0" || v == "off" || v == "no")
}

func intSetting(raw string, def int64) int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && n > 0 {
		return n
	}
	return def
}

// MaxBytes is the largest image the proxy fetches and caches.
func MaxBytes() int64 { return intSetting(config.C.MediaProxyMaxBytes, defaultMaxSize) }

// MaxStreamBytes is the largest video or audio file streamed through.
func MaxStreamBytes() int64 { return intSetting(config.C.MediaProxyMaxStreamBytes, defaultStream) }

func secret() []byte {
	mac := hmac.New(sha256.New, []byte(config.C.EncryptedKey))
	mac.Write([]byte("community media proxy v1"))
	return mac.Sum(nil)
}

// Sign returns the signature for kind and rawURL.
func Sign(kind, rawURL string) string {
	mac := hmac.New(sha256.New, secret())
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(rawURL))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:sigBytes])
}

// Verify checks a signature in constant time.
func Verify(kind, rawURL, sig string) bool {
	want := Sign(kind, rawURL)
	return hmac.Equal([]byte(want), []byte(sig))
}

// Decode turns the path segment back into the source URL.
func Decode(encoded string) (string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(b) == 0 || len(b) > maxSourceURL {
		return "", false
	}
	return string(b), true
}

// IsLocal reports whether raw already points at this server (or its public
// storage host) or is not an absolute http(s) URL, in which case it is served
// as it is.
func IsLocal(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return true
	}
	host := strings.ToLower(u.Host)
	if host == config.AppBaseHost() {
		return true
	}
	if base, err := url.Parse(systemsettings.GetBaseURL()); err == nil && strings.EqualFold(base.Host, host) {
		return true
	}
	if pub, err := url.Parse(storage.Default().PublicURL("x")); err == nil && pub.Host != "" && strings.EqualFold(pub.Host, host) {
		return true
	}
	return false
}

// URL returns the proxied form of a remote media URL. Empty, local,
// non-http(s) and overlong URLs, and everything when the proxy is disabled,
// are returned unchanged.
func URL(kind, raw string) string {
	if raw == "" || !Enabled() || !ValidKind(kind) || len(raw) > maxSourceURL || IsLocal(raw) {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" {
		return raw
	}
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	return base + PathPrefix + Sign(kind, raw) + "/" + base64.RawURLEncoding.EncodeToString([]byte(raw)) + "?kind=" + kind
}

// URLPtr is URL for optional strings.
func URLPtr(kind string, raw *string) *string {
	if raw == nil {
		return nil
	}
	v := URL(kind, *raw)
	return &v
}
