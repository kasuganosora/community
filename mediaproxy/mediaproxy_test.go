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
			c, err := Open(tok)
			require.NoError(t, err)
			require.Equal(t, kind, c.Kind)
			require.Equal(t, raw, c.URL)
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
			_, err := Open(base64.RawURLEncoding.EncodeToString(c))
			require.ErrorIs(t, err, ErrInvalid, "byte %d bit %d", i, bit)
		}
	}
	for _, bad := range []string{"", "!!!", tok[:len(tok)-1], tok + "A", base64.RawURLEncoding.EncodeToString(raw[:10])} {
		_, err := Open(bad)
		require.ErrorIs(t, err, ErrInvalid, bad)
	}
	v := append([]byte(nil), raw...)
	v[0] = 2
	_, err := Open(base64.RawURLEncoding.EncodeToString(v))
	require.ErrorIs(t, err, ErrInvalid, "unknown version")
	_, err = Open(strings.Repeat("A", maxToken+1))
	require.ErrorIs(t, err, ErrInvalid, "oversize token")
}

func TestKeysAndRotation(t *testing.T) {
	setup(t)
	tok, _ := Seal(KindEmoji, "https://r.example/e.png", 0)

	config.C.EncryptedKey = "a-completely-different-secret-key"
	_, err := Open(tok)
	require.ErrorIs(t, err, ErrInvalid, "wrong key")

	config.C.MediaProxyPreviousKey = "0123456789abcdef-test-key"
	c, err := Open(tok)
	require.NoError(t, err, "previous key is accepted")
	require.Equal(t, KindEmoji, c.Kind)
	require.Equal(t, "https://r.example/e.png", c.URL)
	fresh, _ := Seal(KindEmoji, "https://r.example/e.png", 0)
	require.NotEqual(t, tok, fresh, "always sealed with the current key")
	config.C.MediaProxyPreviousKey = ""
	_, err = Open(fresh)
	require.NoError(t, err)

	config.C.MediaProxyKey = "dedicated-proxy-key-dedicated-key"
	_, err = Open(fresh)
	require.ErrorIs(t, err, ErrInvalid, "MEDIA_PROXY_KEY overrides the server key")
	own, _ := Seal(KindEmoji, "https://r.example/e.png", 0)
	_, err = Open(own)
	require.NoError(t, err)
}

func at(sec int64) { Now = func() time.Time { return time.Unix(sec, 0) } }

func TestExpiryIsEnforced(t *testing.T) {
	setup(t)
	defer func() { Now = time.Now }()
	tok, _ := Seal(KindMedia, "https://r.example/a.png", 1000)
	at(999)
	c, err := Open(tok)
	require.NoError(t, err)
	require.EqualValues(t, 1000, c.Expiry)
	at(1000)
	_, err = Open(tok)
	require.NoError(t, err, "valid through the expiry second")
	at(1001)
	c, err = Open(tok)
	require.ErrorIs(t, err, ErrExpired)
	require.Equal(t, "https://r.example/a.png", c.URL)
	forever, _ := Seal(KindMedia, "https://r.example/a.png", 0)
	_, err = Open(forever)
	require.NoError(t, err)

	// A forged expiry cannot be authenticated.
	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	raw[2+nonceBytes] ^= 0xff
	_, err = Open(base64.RawURLEncoding.EncodeToString(raw))
	require.ErrorIs(t, err, ErrInvalid)
}

func TestLinksLiveBetween3000And3600SecondsAndAreStablePerBucket(t *testing.T) {
	setup(t)
	defer func() { Now = time.Now }()
	const raw = "https://r.example/a.png"
	base := int64(1_800_000_000) // multiple of 600
	require.Zero(t, base%600)
	var first string
	for off := int64(0); off < 600; off += 37 {
		at(base + off)
		u := URL(KindMedia, raw)
		if first == "" {
			first = u
		}
		require.Equal(t, first, u, "same bucket, same token (offset %d)", off)
		c, err := Open(strings.TrimPrefix(u, "https://c.test/proxy/media/"))
		require.NoError(t, err)
		life := c.Expiry - (base + off)
		require.Greater(t, life, int64(3000)-1)
		require.LessOrEqual(t, life, int64(3600))
	}
	at(base + 600)
	require.NotEqual(t, first, URL(KindMedia, raw), "next bucket mints a new token")
	// Valid for at least 3000s, expired after 3600s.
	at(base)
	u := URL(KindMedia, raw)
	tok := strings.TrimPrefix(u, "https://c.test/proxy/media/")
	at(base + 2999)
	_, err := Open(tok)
	require.NoError(t, err)
	at(base + 3601)
	_, err = Open(tok)
	require.ErrorIs(t, err, ErrExpired)
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
	c, err := Open(strings.TrimPrefix(got, "https://c.test/proxy/media/"))
	require.NoError(t, err)
	require.Equal(t, KindMedia, c.Kind)
	require.Equal(t, remote, c.URL)
	require.NotZero(t, c.Expiry, "API links always carry an expiry")
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
