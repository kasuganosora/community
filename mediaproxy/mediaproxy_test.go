package mediaproxy

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) {
	t.Helper()
	old := config.C
	config.C.EncryptedKey = "0123456789abcdef-test-key"
	config.C.AppBaseURL = "https://c.test"
	config.C.MediaProxyEnabled = "true"
	t.Cleanup(func() { config.C = old })
}

func TestSealOpenRoundTrip(t *testing.T) {
	setup(t)
	for _, raw := range []string{"https://r.example/a.png", "https://r.example/a.png?x=1&y=%E4%B8%AD", "https://例え.jp/画像/表情.png?名=值", "http://r.example:8080/" + strings.Repeat("a", 1900)} {
		for _, kind := range kindCodes {
			tok, ok := Seal(kind, raw, 0)
			require.True(t, ok)
			k, u, ok := Open(tok)
			require.True(t, ok)
			require.Equal(t, kind, k)
			require.Equal(t, raw, u)
		}
	}
}

func TestTokenIsDeterministicAndOpaque(t *testing.T) {
	setup(t)
	raw := "https://secret-host.example/private/path/cat.png"
	a, _ := Seal(KindMedia, raw, 0)
	b, _ := Seal(KindMedia, raw, 0)
	require.Equal(t, a, b, "same input, same token: browser caches stay valid")
	other, _ := Seal(KindAvatar, raw, 0)
	require.NotEqual(t, a, other)
	other, _ = Seal(KindMedia, raw+"x", 0)
	require.NotEqual(t, a, other)

	plain, _ := base64.RawURLEncoding.DecodeString(a)
	for _, leak := range []string{"secret-host", "example", "private", "cat.png", "https"} {
		require.NotContains(t, a, leak)
		require.NotContains(t, string(plain), leak)
	}
	require.Regexp(t, `^[A-Za-z0-9_-]+$`, a)
}

func TestTamperedTokensAreRejected(t *testing.T) {
	setup(t)
	tok, _ := Seal(KindMedia, "https://r.example/a.png", 0)
	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	for i := range raw {
		for bit := 0; bit < 8; bit++ {
			c := append([]byte(nil), raw...)
			c[i] ^= 1 << bit
			_, _, ok := Open(base64.RawURLEncoding.EncodeToString(c))
			require.False(t, ok, "byte %d bit %d", i, bit)
		}
	}
	for _, bad := range []string{"", "!!!", tok[:len(tok)-1], tok + "A", base64.RawURLEncoding.EncodeToString(raw[:10])} {
		_, _, ok := Open(bad)
		require.False(t, ok, bad)
	}
	v := append([]byte(nil), raw...)
	v[0] = 2
	_, _, ok := Open(base64.RawURLEncoding.EncodeToString(v))
	require.False(t, ok, "unknown version")
	_, _, ok = Open(strings.Repeat("A", maxToken+1))
	require.False(t, ok, "oversize token")
}

func TestKeysAndRotation(t *testing.T) {
	setup(t)
	tok, _ := Seal(KindEmoji, "https://r.example/e.png", 0)

	config.C.EncryptedKey = "a-completely-different-secret-key"
	_, _, ok := Open(tok)
	require.False(t, ok, "wrong key")

	config.C.MediaProxyPreviousKey = "0123456789abcdef-test-key"
	k, u, ok := Open(tok)
	require.True(t, ok, "previous key is accepted")
	require.Equal(t, KindEmoji, k)
	require.Equal(t, "https://r.example/e.png", u)
	fresh, _ := Seal(KindEmoji, "https://r.example/e.png", 0)
	require.NotEqual(t, tok, fresh, "always sealed with the current key")
	config.C.MediaProxyPreviousKey = ""
	_, _, ok = Open(fresh)
	require.True(t, ok)

	config.C.MediaProxyKey = "dedicated-proxy-key-dedicated-key"
	_, _, ok = Open(fresh)
	require.False(t, ok, "MEDIA_PROXY_KEY overrides the server key")
	own, _ := Seal(KindEmoji, "https://r.example/e.png", 0)
	_, _, ok = Open(own)
	require.True(t, ok)
}

func TestExpiryIsEnforcedOnlyWhenSet(t *testing.T) {
	setup(t)
	tok, _ := Seal(KindMedia, "https://r.example/a.png", 1000)
	now = func() time.Time { return time.Unix(999, 0) }
	defer func() { now = time.Now }()
	_, _, ok := Open(tok)
	require.True(t, ok)
	now = func() time.Time { return time.Unix(1001, 0) }
	_, _, ok = Open(tok)
	require.False(t, ok)
	forever, _ := Seal(KindMedia, "https://r.example/a.png", 0)
	_, _, ok = Open(forever)
	require.True(t, ok)
}

func TestSealRejectsBadInput(t *testing.T) {
	setup(t)
	_, ok := Seal("bogus", "https://r.example/a.png", 0)
	require.False(t, ok)
	_, ok = Seal(KindMedia, "", 0)
	require.False(t, ok)
	_, ok = Seal(KindMedia, "https://r.example/"+strings.Repeat("a", maxSourceURL), 0)
	require.False(t, ok)
}

func TestURLRewritesOnlyRemote(t *testing.T) {
	setup(t)
	remote := "https://ovo.wxw.moe/media/a.jpeg?x=1"
	got := URL(KindMedia, remote)
	require.True(t, strings.HasPrefix(got, "https://c.test/proxy/media/"), got)
	kind, decoded, ok := Open(strings.TrimPrefix(got, "https://c.test/proxy/media/"))
	require.True(t, ok)
	require.Equal(t, KindMedia, kind)
	require.Equal(t, remote, decoded)
	require.NotContains(t, got, "wxw.moe")

	for _, same := range []string{"", "/media/1/a.png", "https://c.test/media/1/a.png", "HTTPS://C.TEST/emoji-assets/x.png", "data:image/png;base64,AAAA", "ftp://x.example/a", strings.Repeat("https://r.example/", 200)} {
		require.Equal(t, same, URL(KindMedia, same), same)
	}
	require.Equal(t, remote, URL("bogus", remote))

	config.C.MediaProxyEnabled = "false"
	require.Equal(t, remote, URL(KindMedia, remote))
}

func TestLimits(t *testing.T) {
	setup(t)
	require.EqualValues(t, 20<<20, MaxBytes())
	require.EqualValues(t, 512<<20, MaxStreamBytes())
	config.C.MediaProxyMaxBytes = "1000"
	require.EqualValues(t, 1000, MaxBytes())
}
