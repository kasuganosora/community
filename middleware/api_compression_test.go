package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func compressionRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(APICompression())
	big := strings.Repeat(`{"id":"1","content":"hello world"},`, 200)
	r.GET("/api/v1/timelines/public", func(c *gin.Context) { c.Data(200, "application/json; charset=utf-8", []byte("["+big+"{}]")) })
	r.GET("/api/v1/small", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r.GET("/api/v1/png", func(c *gin.Context) { c.Data(200, "image/png", bytes.Repeat([]byte{1}, 4096)) })
	r.GET("/api/v1/pre", func(c *gin.Context) {
		c.Header("Content-Encoding", "gzip")
		c.Data(200, "application/json", bytes.Repeat([]byte{1}, 4096))
	})
	r.GET("/api/v1/streaming/user", func(c *gin.Context) { c.Data(200, "application/json", []byte(big)) })
	return r
}

func get(r http.Handler, path, ae string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if ae != "" {
		req.Header.Set("Accept-Encoding", ae)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAPICompressionBrotliAndGzip(t *testing.T) {
	r := compressionRouter()
	w := get(r, "/api/v1/timelines/public", "gzip, deflate, br")
	require.Equal(t, "br", w.Header().Get("Content-Encoding"))
	require.Contains(t, w.Header().Values("Vary"), "Accept-Encoding")
	plain, err := io.ReadAll(brotli.NewReader(w.Body))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(plain), `[{"id":"1"`))
	require.Less(t, w.Body.Len(), len(plain)/5)

	w = get(r, "/api/v1/timelines/public", "gzip")
	require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	zr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	unz, _ := io.ReadAll(zr)
	require.Equal(t, plain, unz)

	w = get(r, "/api/v1/timelines/public", "")
	require.Empty(t, w.Header().Get("Content-Encoding"))
	require.Equal(t, plain, w.Body.Bytes())
	w = get(r, "/api/v1/timelines/public", "br;q=0, gzip;q=0")
	require.Empty(t, w.Header().Get("Content-Encoding"))
}

func TestAPICompressionSkips(t *testing.T) {
	r := compressionRouter()
	for _, p := range []string{"/api/v1/small", "/api/v1/png", "/api/v1/streaming/user"} {
		w := get(r, p, "br, gzip")
		require.Empty(t, w.Header().Get("Content-Encoding"), p)
	}
	w := get(r, "/api/v1/pre", "br, gzip")
	require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	require.Equal(t, 4096, w.Body.Len(), "already encoded body is untouched")
}
