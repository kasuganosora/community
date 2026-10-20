package serve

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"testing"

	"github.com/kasuganosora/community/avatarcache"
	"github.com/stretchr/testify/require"
)

// fictionalAnimatedWebP is a minimal animated WebP (VP8X + ANIM + ANMF)
// whose frames Go cannot decode.
func fictionalAnimatedWebP() []byte {
	chunk := func(id string, n int) []byte {
		b := append([]byte(id), 0, 0, 0, 0)
		binary.LittleEndian.PutUint32(b[4:], uint32(n))
		return append(b, make([]byte, n)...)
	}
	vp8x := chunk("VP8X", 10)
	vp8x[8] = 0x02                  // animation flag
	vp8x[12], vp8x[13] = 0x8f, 0x01 // width-1 = 399
	vp8x[15], vp8x[16] = 0x8f, 0x01 // height-1 = 399
	body := append([]byte("WEBP"), vp8x...)
	body = append(body, chunk("ANIM", 6)...)
	body = append(body, chunk("ANMF", 40)...)
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	return append(out, body...)
}

func TestCachedAvatarServesAnimatedWebPAsIs(t *testing.T) {
	src := fictionalAnimatedWebP()
	srv, r := setup(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write(src)
	}))
	cache, err := avatarcache.New(t.TempDir(), 0)
	require.NoError(t, err)
	old := avatarCache
	avatarCache = func() *avatarcache.Cache { return cache }
	t.Cleanup(func() { avatarCache = old })
	w := do(r, "GET", proxyPath("avatar", srv.URL+"/fictional.webp")+"?size=96", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Header().Get("X-Media-Proxy-Error"))
	require.Equal(t, "image/webp", w.Header().Get("Content-Type"))
	require.True(t, bytes.Equal(src, w.Body.Bytes()))
}
