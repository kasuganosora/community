package community

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicFileRoutesAnswerHEAD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mem := storagetest.NewMemory()
	t.Cleanup(storage.SetDefault(mem))
	name := "0123456789abcdef0123456789abcdef.png"
	emoji := "01234567-89ab-cdef-0123-456789abcdef.png"
	require.NoError(t, mem.Put(context.Background(), "cards/"+name, bytes.NewReader([]byte("\x89PNG\r\n\x1a\nxxxx")), 12, "image/png"))
	require.NoError(t, mem.Put(context.Background(), "emojis/"+emoji, bytes.NewReader([]byte("\x89PNG\r\n\x1a\nxxxx")), 12, "image/png"))

	app := gin.New()
	app.Use(middleware.SecurityHeadersMiddleware())
	registerPublicFileRoutes(app)
	do := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}

	for _, path := range []string{"/card-images/" + name, "/emoji-assets/" + emoji} {
		get, head := do(http.MethodGet, path), do(http.MethodHead, path)
		require.Equal(t, 200, get.Code, path)
		assert.Equal(t, 200, head.Code, path)
		assert.Empty(t, head.Body.Bytes(), path)
		assert.NotEmpty(t, get.Body.Bytes())
		for _, h := range []string{"Content-Type", "Cache-Control", "X-Content-Type-Options", "Cross-Origin-Resource-Policy"} {
			assert.Equal(t, get.Header().Get(h), head.Header().Get(h), path+" "+h)
		}
		assert.Equal(t, "cross-origin", head.Header().Get("Cross-Origin-Resource-Policy"), path)
	}
	assert.Equal(t, 404, do(http.MethodHead, "/card-images/ffffffffffffffffffffffffffffffff.png").Code)

	// Routes whose handlers need a database: HEAD must at least be routed.
	for _, path := range []string{"/media/1/x.png", "/system/media_attachments/files/1/x.png", "/files/x.png"} {
		func() {
			defer func() { _ = recover() }() // no DB in this test
			assert.NotEqual(t, http.StatusMethodNotAllowed, do(http.MethodHead, path).Code, path)
		}()
	}
	assert.Equal(t, http.StatusNotFound, do(http.MethodPost, "/card-images/"+name).Code)
}
