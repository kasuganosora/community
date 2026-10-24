package serializer

import (
	"encoding/json"
	"github.com/kasuganosora/community/models"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func TestProxiedCardRewritesRemoteImageOnly(t *testing.T) {
	card := map[string]any{"url": "https://r.example/p", "image": "https://r.example/i.png"}
	out := proxiedCard(card)
	require.Equal(t, mediaproxy.URL(mediaproxy.KindPreview, "https://r.example/i.png"), out["image"])
	require.NotEqual(t, "https://r.example/i.png", out["image"])
	require.Equal(t, "https://r.example/i.png", card["image"], "stored card is untouched")
	require.Equal(t, "https://r.example/p", out["url"])

	local := map[string]any{"url": "https://r.example/p", "image": "/card-images/a.png"}
	require.Equal(t, systemsettings.GetBaseURL()+"/card-images/a.png", proxiedCard(local)["image"])
	require.Equal(t, "https://r.example/p", proxiedCard(map[string]any{"url": "https://r.example/p"})["url"])
}

// Redis stores model JSON, never serialized API output, so every response
// (including one built from a cached account) mints a fresh token.
func TestProxiedLinksAreMintedAtResponseTimeEvenFromCache(t *testing.T) {
	defer func() { mediaproxy.Now = time.Now }()
	domain := "wxw.moe"
	cached, err := json.Marshal(&models.Account{Id: 1, Username: "a", Domain: &domain, Avatar: "https://wxw.moe/a.png", Header: "https://wxw.moe/h.png"})
	require.NoError(t, err)
	var fromCache models.Account
	require.NoError(t, json.Unmarshal(cached, &fromCache))
	require.NotContains(t, string(cached), "/proxy/media/")

	expiry := func(u string) int64 {
		c, err := mediaproxy.Open(u[strings.LastIndex(u, "/")+1:])
		require.NoError(t, err, u)
		return c.Expiry
	}
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	start := time.Date(2026, 10, 24, 16, 0, 0, 0, shanghai)
	mediaproxy.Now = func() time.Time { return start }
	first := ToAccount(&fromCache).Avatar
	require.Contains(t, first, "/proxy/media/")
	e1 := expiry(first)
	require.Equal(t, time.Date(2026, 10, 25, 23, 59, 59, 0, shanghai).Unix(), e1)

	// Later the same Shanghai day the cached entity is sealed again and the
	// token does not change, so browsers can keep the cached avatar.
	later := start.Add(2 * time.Hour)
	mediaproxy.Now = func() time.Time { return later }
	second := ToAccount(&fromCache).Avatar
	require.Equal(t, first, second)
	_, err = mediaproxy.Open(first[strings.LastIndex(first, "/")+1:])
	require.NoError(t, err)

	// The next Shanghai day mints a new token that is valid at that moment.
	nextDay := time.Date(2026, 10, 25, 0, 0, 0, 0, shanghai)
	mediaproxy.Now = func() time.Time { return nextDay }
	third := ToAccount(&fromCache).Avatar
	require.NotEqual(t, first, third)
	require.Equal(t, time.Date(2026, 10, 26, 23, 59, 59, 0, shanghai).Unix(), expiry(third))

	// The day-D token is rejected once D+1 23:59:59 has passed.
	mediaproxy.Now = func() time.Time { return time.Date(2026, 10, 26, 0, 0, 0, 0, shanghai) }
	_, err = mediaproxy.Open(first[strings.LastIndex(first, "/")+1:])
	require.ErrorIs(t, err, mediaproxy.ErrExpired)
}

func TestLocalCardImagesKeepTheirImage(t *testing.T) {
	for _, img := range []string{
		"https://c.test/card-images/abc.png",
		"http://localhost/card-images/abc.png",         // saved before the domain was configured
		"https://old-name.example/card-images/abc.png", // site address changed since
		"/card-images/abc.png",
	} {
		out := proxiedCard(map[string]any{"url": "https://x.example/p", "image": img, "title": "t"})
		want := img
		if want[0] == '/' {
			want = systemsettings.GetBaseURL() + img
		}
		require.Equal(t, want, out["image"], img)
		require.Equal(t, "t", out["title"])
	}
	// A remote image is proxied and the other fields survive; a missing one stays missing.
	out := proxiedCard(map[string]any{"url": "https://x.example/p", "image": "https://r.example/i.png", "width": 10})
	require.Contains(t, out["image"], "/proxy/media/")
	require.Equal(t, 10, out["width"])
	out = proxiedCard(map[string]any{"url": "https://x.example/p", "image": nil, "title": "t"})
	require.Nil(t, out["image"])
	_, has := out["image"]
	require.True(t, has, "image key is preserved")
}
