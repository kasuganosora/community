package serve

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasuganosora/community/avatarcache"
	"github.com/stretchr/testify/require"
)

func TestAvatarUpstreamFailuresDegradeToBoundedPlaceholderWhileMediaKeepsContract(t *testing.T) {
	t.Run("avatar_timeout_and_5xx_return_placeholder_in_bounded_time", func(t *testing.T) {
		cache, err := avatarcache.New(t.TempDir(), 0)
		require.NoError(t, err)
		oldCache := avatarCache
		avatarCache = func() *avatarcache.Cache { return cache }
		t.Cleanup(func() { avatarCache = oldCache })

		oldAvatarTimeout := avatarFetchTimeout
		avatarFetchTimeout = 100 * time.Millisecond
		t.Cleanup(func() { avatarFetchTimeout = oldAvatarTimeout })

		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		var slowHits atomic.Int32
		srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			switch req.URL.Path {
			case "/slow-avatar.png":
				slowHits.Add(1)
				select {
				case <-release:
				case <-req.Context().Done():
				}
			case "/err500-avatar.png":
				w.WriteHeader(http.StatusInternalServerError)
			case "/gone-avatar.png":
				w.WriteHeader(http.StatusNotFound)
			}
		}))

		start := time.Now()
		w := do(r, "GET", proxyPath("avatar", srv.URL+"/slow-avatar.png"), nil)
		elapsed := time.Since(start)
		require.Less(t, elapsed, 1500*time.Millisecond, "avatar timeout must be bounded")
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "image/png", w.Header().Get("Content-Type"))
		require.Equal(t, "placeholder", w.Header().Get("X-Media-Proxy-Fallback"))
		require.Equal(t, classTimeout, w.Header().Get("X-Media-Proxy-Error"))
		_, err = png.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		require.NoError(t, err)

		// Memoised repeat still serves placeholder immediately without re-hitting upstream.
		wRepeat := do(r, "GET", proxyPath("avatar", srv.URL+"/slow-avatar.png"), nil)
		require.Equal(t, http.StatusOK, wRepeat.Code)
		require.Equal(t, "placeholder", wRepeat.Header().Get("X-Media-Proxy-Fallback"))
		require.Equal(t, "negative_cache; timeout", wRepeat.Header().Get("X-Media-Proxy-Error"))
		require.EqualValues(t, 1, slowHits.Load())

		// Upstream 500 on avatar returns placeholder PNG (200), not 502.
		w500 := do(r, "GET", proxyPath("avatar", srv.URL+"/err500-avatar.png"), nil)
		require.Equal(t, http.StatusOK, w500.Code)
		require.Equal(t, "image/png", w500.Header().Get("Content-Type"))
		require.Equal(t, "placeholder", w500.Header().Get("X-Media-Proxy-Fallback"))
		require.Equal(t, classUpstream, w500.Header().Get("X-Media-Proxy-Error"))
		_, err = png.DecodeConfig(bytes.NewReader(w500.Body.Bytes()))
		require.NoError(t, err)

		// HEAD on failing avatar returns 200 headers with empty body.
		wHead := do(r, "HEAD", proxyPath("avatar", srv.URL+"/err500-avatar.png"), nil)
		require.Equal(t, http.StatusOK, wHead.Code)
		require.Empty(t, wHead.Body.Bytes())

		// Upstream 404 on avatar keeps its 404 contract.
		w404 := do(r, "GET", proxyPath("avatar", srv.URL+"/gone-avatar.png"), nil)
		require.Equal(t, http.StatusNotFound, w404.Code)
	})

	t.Run("stale_cached_avatar_is_served_on_first_upstream_5xx", func(t *testing.T) {
		var jpg bytes.Buffer
		require.NoError(t, jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 64, 64)), nil))

		var failUpstream atomic.Bool
		srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if failUpstream.Load() {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpg.Bytes())
		}))
		cache, err := avatarcache.New(t.TempDir(), 0)
		require.NoError(t, err)
		oldCache := avatarCache
		avatarCache = func() *avatarcache.Cache { return cache }
		t.Cleanup(func() { avatarCache = oldCache })

		p := proxyPath("avatar", srv.URL+"/stale.jpg") + "?size=90"
		w1 := do(r, "GET", p, nil)
		require.Equal(t, http.StatusOK, w1.Code)
		require.Equal(t, "image/jpeg", w1.Header().Get("Content-Type"))

		// Age the cached file past RemoteTTL so refresh is attempted and fails with 502.
		entry, _, found := cache.Peek("remote|"+srv.URL+"/stale.jpg", 96, 0)
		require.True(t, found)
		past := time.Now().Add(-48 * time.Hour)
		require.NoError(t, os.Chtimes(entry.Path, past, past))

		failUpstream.Store(true)
		w2 := do(r, "GET", p, nil)
		require.Equal(t, http.StatusOK, w2.Code)
		require.Equal(t, "image/jpeg", w2.Header().Get("Content-Type"))
		require.Empty(t, w2.Header().Get("X-Media-Proxy-Fallback"), "stale cached rendition should be preferred over synthetic placeholder")
	})

	t.Run("non_avatar_media_keeps_distinct_timeout_5xx_and_404_semantics", func(t *testing.T) {
		oldHeaderTimeout := headerTimeout
		headerTimeout = 100 * time.Millisecond
		t.Cleanup(func() { headerTimeout = oldHeaderTimeout })

		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			switch req.URL.Path {
			case "/slow.mp4":
				select {
				case <-release:
				case <-req.Context().Done():
				}
			case "/err500.mp4":
				w.WriteHeader(http.StatusInternalServerError)
			case "/gone.mp4":
				w.WriteHeader(http.StatusNotFound)
			}
		}))

		start := time.Now()
		wTimeout := do(r, "GET", proxyPath("media", srv.URL+"/slow.mp4"), nil)
		require.Less(t, time.Since(start), 1500*time.Millisecond)
		require.Equal(t, http.StatusGatewayTimeout, wTimeout.Code, "arbitrary media timeout must not be turned into 404")

		w500 := do(r, "GET", proxyPath("media", srv.URL+"/err500.mp4"), nil)
		require.Equal(t, http.StatusBadGateway, w500.Code, "arbitrary media 5xx must not be turned into 404")

		w404 := do(r, "GET", proxyPath("media", srv.URL+"/gone.mp4"), nil)
		require.Equal(t, http.StatusNotFound, w404.Code)
	})
}
