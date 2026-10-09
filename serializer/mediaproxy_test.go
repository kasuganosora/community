package serializer

import (
	"testing"

	"github.com/kasuganosora/community/mediaproxy"
	"github.com/stretchr/testify/require"
)

func TestProxiedCardRewritesRemoteImageOnly(t *testing.T) {
	card := map[string]any{"url": "https://r.example/p", "image": "https://r.example/i.png"}
	out := proxiedCard(card)
	require.Equal(t, mediaproxy.URL(mediaproxy.KindPreview, "https://r.example/i.png"), out["image"])
	require.NotEqual(t, "https://r.example/i.png", out["image"])
	require.Equal(t, "https://r.example/i.png", card["image"], "stored card is untouched")
	require.Equal(t, "https://r.example/p", out["url"])

	local := map[string]any{"image": "/card-images/a.png"}
	require.Equal(t, "/card-images/a.png", proxiedCard(local)["image"])
	require.Equal(t, "https://r.example/p", proxiedCard(map[string]any{"url": "https://r.example/p"})["url"])
}
