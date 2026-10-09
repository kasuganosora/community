// Package mediaproxy turns URLs of remote media into opaque encrypted tokens
// so browsers fetch them from this server (see mediaproxy/serve) instead of
// from the remote one. Without the key a token can neither be read nor
// forged, which keeps the endpoint from working as an open proxy.
package mediaproxy

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"net/url"
	"strconv"
	"strings"
	"time"

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

// Token layout (URL-safe base64 of):
//
//	version(1) | kind(1) | nonce(12) | AES-256-GCM(expiry(8) | url) | tag(16)
//
// version and kind travel in the clear and are the GCM additional data, so
// they cannot be changed without failing authentication. The URL and the
// expiry are only readable with the key.
const (
	tokenVersion = 1
	nonceBytes   = 12
	// maxToken bounds what Open accepts before doing any work.
	maxToken = 3200
)

var kindCodes = []string{KindAvatar, KindHeader, KindMedia, KindEmoji, KindPreview}

func kindCode(kind string) (byte, bool) {
	for i, k := range kindCodes {
		if k == kind {
			return byte(i + 1), true
		}
	}
	return 0, false
}

type keys struct {
	aead  cipher.AEAD
	nonce []byte // key for the synthetic nonce
}

func deriveKeys(master string) (*keys, error) {
	enc, err := hkdf.Key(sha256.New, []byte(master), []byte("community media proxy"), "aes-256-gcm v1", 32)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, []byte(master), []byte("community media proxy"), "synthetic nonce v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(enc)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &keys{aead: aead, nonce: nonce}, nil
}

// currentSecret is MEDIA_PROXY_KEY, or the server's encryption key.
func currentSecret() string {
	if k := strings.TrimSpace(config.C.MediaProxyKey); k != "" {
		return k
	}
	return config.C.EncryptedKey
}

// Seal returns the opaque token for kind and rawURL. expiry (unix seconds)
// of 0 means the token never expires. With the same inputs the token is
// always the same: the nonce is a keyed hash of kind and URL (SIV style), so
// cached API responses and browser caches keep working. The only thing that
// reveals is that two tokens carry the same URL.
func Seal(kind, rawURL string, expiry int64) (string, bool) {
	code, ok := kindCode(kind)
	if !ok || rawURL == "" || len(rawURL) > maxSourceURL {
		return "", false
	}
	k, err := deriveKeys(currentSecret())
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, k.nonce)
	mac.Write([]byte{tokenVersion, code})
	_ = binary.Write(mac, binary.BigEndian, expiry)
	mac.Write([]byte(rawURL))
	nonce := mac.Sum(nil)[:nonceBytes]

	plain := make([]byte, 8, 8+len(rawURL))
	binary.BigEndian.PutUint64(plain, uint64(expiry))
	plain = append(plain, rawURL...)
	out := append([]byte{tokenVersion, code}, nonce...)
	out = k.aead.Seal(out, nonce, plain, out[:2])
	return base64.RawURLEncoding.EncodeToString(out), true
}

var now = time.Now

// Open decrypts a token with the current key, then MEDIA_PROXY_PREVIOUS_KEY.
// It fails for a token that was altered, made with another key, of an unknown
// version or kind, too long, or expired.
func Open(token string) (kind, rawURL string, ok bool) {
	if token == "" || len(token) > maxToken {
		return "", "", false
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) < 2+nonceBytes+8+16 || b[0] != tokenVersion || int(b[1]) < 1 || int(b[1]) > len(kindCodes) {
		return "", "", false
	}
	for _, secret := range []string{currentSecret(), strings.TrimSpace(config.C.MediaProxyPreviousKey)} {
		if secret == "" {
			continue
		}
		k, err := deriveKeys(secret)
		if err != nil {
			continue
		}
		plain, err := k.aead.Open(nil, b[2:2+nonceBytes], b[2+nonceBytes:], b[:2])
		if err != nil || len(plain) <= 8 {
			continue
		}
		if exp := int64(binary.BigEndian.Uint64(plain)); exp != 0 && now().Unix() > exp {
			return "", "", false
		}
		return kindCodes[b[1]-1], string(plain[8:]), true
	}
	return "", "", false
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
	token, ok := Seal(kind, raw, 0)
	if !ok {
		return raw
	}
	return base + PathPrefix + token
}

// URLPtr is URL for optional strings.
func URLPtr(kind string, raw *string) *string {
	if raw == nil {
		return nil
	}
	v := URL(kind, *raw)
	return &v
}
