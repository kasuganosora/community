package defaultavatar

import (
	"bytes"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdentityAndRendering(t *testing.T) {
	require.Equal(t, Seed("Alice", "EXAMPLE.COM"), Seed("Alice", "example.com"))
	require.NotEqual(t, Seed("Alice", "example.com"), Seed("alice", "example.com"))
	require.NotEqual(t, Seed("Alice", "example.com"), Seed("Alice", "other.test"))
	b, e := PNG(strings.Repeat("0", 64))
	require.NoError(t, e)
	img, e := png.Decode(bytes.NewReader(b))
	require.NoError(t, e)
	require.Equal(t, 128, img.Bounds().Dx())
	require.NotEqual(t, img.At(0, 0), img.At(64, 64))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			require.Equal(t, img.At(x, y), img.At(127-x, y))
		}
	}
}
func TestPublicEndpointValidationAndCache(t *testing.T) {
	r := gin.New()
	r.GET("/api/v1/avatars/default/:seed", Serve)
	url := Path(Seed("a", "example.test"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "cross-origin", w.Header().Get("Cross-Origin-Resource-Policy"))
	etag := w.Header().Get("ETag")
	req := httptest.NewRequest("GET", url, nil)
	req.Header.Set("If-None-Match", etag)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 304, w.Code)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/avatars/default/private.png", nil))
	require.Equal(t, 404, w.Code)
}
