package serve

import (
	"bytes"
	"image"
	"image/jpeg"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/kasuganosora/community/avatarcache"
	"github.com/stretchr/testify/require"
)

func TestAvatarsAreResizedAndCachedOnDisk(t *testing.T) {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 640, 480)), nil)
	var hits atomic.Int32
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(b.Bytes())
	}))
	cache, err := avatarcache.New(t.TempDir(), 0)
	require.NoError(t, err)
	old := avatarCache
	avatarCache = func() *avatarcache.Cache { return cache }
	t.Cleanup(func() { avatarCache = old })

	p := proxyPath("avatar", srv.URL+"/a.jpg") + "?size=90"
	for range 3 {
		w := do(r, "GET", p, nil)
		require.Equal(t, 200, w.Code)
		require.Equal(t, "image/jpeg", w.Header().Get("Content-Type"))
		require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		cfg, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		require.NoError(t, err)
		require.Equal(t, 96, cfg.Width)
		require.Equal(t, 96, cfg.Height)
	}
	require.EqualValues(t, 1, hits.Load(), "later requests are disk hits")

	// Non-avatar kinds are still relayed byte for byte.
	w := do(r, "GET", proxyPath("media", srv.URL+"/a.jpg"), nil)
	require.Equal(t, b.Len(), w.Body.Len())
}

func TestCachedAvatarRejectsNonImages(t *testing.T) {
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("<html><script>alert(1)</script></html>"))
	}))
	cache, _ := avatarcache.New(t.TempDir(), 0)
	old := avatarCache
	avatarCache = func() *avatarcache.Cache { return cache }
	t.Cleanup(func() { avatarCache = old })
	require.Equal(t, http.StatusUnsupportedMediaType, do(r, "GET", proxyPath("avatar", srv.URL+"/x.png"), nil).Code)
}
