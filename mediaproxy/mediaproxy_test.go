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

// shanghai is Beijing time: UTC+8 with no daylight saving. ExpiryFor must
// use this calendar even when the caller passes a UTC (or other) instant.
var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

func TestExpiryForIsNextShanghaiDayEnd(t *testing.T) {
	// Day D in Asia/Shanghai expires at D+1 23:59:59 in that zone, for every
	// instant on D — including the UTC instant that is still "today" in Beijing
	// and the one that has already become tomorrow there.
	day := time.Date(2026, 10, 24, 0, 0, 0, 0, shanghai)
	want := time.Date(2026, 10, 25, 23, 59, 59, 0, shanghai).Unix()
	for _, ts := range []time.Time{
		day,
		time.Date(2026, 10, 24, 12, 34, 56, 0, shanghai),
		time.Date(2026, 10, 24, 23, 59, 59, 999_999_999, shanghai),
		time.Date(2026, 10, 24, 15, 59, 59, 0, time.UTC), // 23:59:59 Shanghai
	} {
		require.Equal(t, want, ExpiryFor(ts), ts.Format(time.RFC3339Nano))
	}
	// 16:00 UTC is already the next Shanghai calendar day.
	next := time.Date(2026, 10, 25, 0, 0, 0, 0, shanghai)
	require.Equal(t, next, time.Date(2026, 10, 24, 16, 0, 0, 0, time.UTC).In(shanghai))
	wantNext := time.Date(2026, 10, 26, 23, 59, 59, 0, shanghai).Unix()
	require.Equal(t, wantNext, ExpiryFor(time.Date(2026, 10, 24, 16, 0, 0, 0, time.UTC)))
	require.Equal(t, wantNext, ExpiryFor(next))
	require.NotEqual(t, want, wantNext)

	// Month, year, and leap-day boundaries stay on the calendar, not +N hours.
	require.Equal(t, time.Date(2026, 11, 1, 23, 59, 59, 0, shanghai).Unix(), ExpiryFor(time.Date(2026, 10, 31, 8, 0, 0, 0, shanghai)))
	require.Equal(t, time.Date(2027, 1, 1, 23, 59, 59, 0, shanghai).Unix(), ExpiryFor(time.Date(2026, 12, 31, 23, 0, 0, 0, shanghai)))
	require.Equal(t, time.Date(2024, 2, 29, 23, 59, 59, 0, shanghai).Unix(), ExpiryFor(time.Date(2024, 2, 28, 0, 0, 0, 0, shanghai)))
	require.Equal(t, time.Date(2024, 3, 1, 23, 59, 59, 0, shanghai).Unix(), ExpiryFor(time.Date(2024, 2, 29, 23, 59, 59, 0, shanghai)))

	// Minted at 00:00:00 the link lives 48h-1s; minted at 23:59:59 it lives exactly 24h.
	require.Equal(t, int64(48*60*60-1), ExpiryFor(day)-day.Unix())
	last := time.Date(2026, 10, 24, 23, 59, 59, 0, shanghai)
	require.Equal(t, int64(24*60*60), ExpiryFor(last)-last.Unix())
}

func TestSameShanghaiDaySameInputsSameToken(t *testing.T) {
	setup(t)
	defer func() { Now = time.Now }()
	const raw = "https://r.example/a.png"
	day := time.Date(2026, 10, 24, 0, 0, 0, 0, shanghai)
	expiry := time.Date(2026, 10, 25, 23, 59, 59, 0, shanghai)

	var first string
	for _, ts := range []time.Time{
		day,
		time.Date(2026, 10, 24, 0, 0, 1, 0, shanghai),
		time.Date(2026, 10, 24, 15, 0, 0, 0, shanghai),
		time.Date(2026, 10, 24, 23, 59, 59, 0, shanghai),
		time.Date(2026, 10, 24, 15, 59, 59, 0, time.UTC),
	} {
		at(ts.Unix())
		u := URL(KindMedia, raw)
		if first == "" {
			first = u
		}
		require.Equal(t, first, u, "same Shanghai day, same token (%s)", ts.Format(time.RFC3339))
		c, err := Open(strings.TrimPrefix(u, "https://c.test/proxy/media/"))
		require.NoError(t, err)
		require.Equal(t, expiry.Unix(), c.Expiry)
	}

	at(time.Date(2026, 10, 25, 0, 0, 0, 0, shanghai).Unix())
	require.NotEqual(t, first, URL(KindMedia, raw), "next Shanghai day mints a new token")

	// Valid through D+1 23:59:59, rejected at D+2 00:00:00.
	tok := strings.TrimPrefix(first, "https://c.test/proxy/media/")
	at(expiry.Unix())
	_, err := Open(tok)
	require.NoError(t, err, "valid through the expiry second")
	at(expiry.Add(time.Second).Unix())
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
