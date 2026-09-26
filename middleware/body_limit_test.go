package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestBodyLimitFor(t *testing.T) {
	media := func() int64 { return 40 << 20 }

	req := func(method, path, contentType string) *http.Request {
		r := httptest.NewRequest(method, path, nil)
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}

		return r
	}

	assert.Equal(t, int64(1<<20), bodyLimitFor(req("POST", "/api/v1/statuses", "application/json"), 1<<20, media))
	assert.Equal(t, int64(41<<20), bodyLimitFor(req("POST", "/api/v2/media", "multipart/form-data; boundary=x"), 1<<20, media))
	assert.Equal(t, int64(100<<20), bodyLimitFor(req("POST", "/_matrix/media/v3/upload", "image/png"), 1<<20, media))
	assert.Equal(t, int64(100<<20), bodyLimitFor(req("POST", "/_matrix/client/v1/media/upload", "image/png"), 1<<20, media))
	// A media limit below the default never lowers the limit.
	assert.Equal(t, int64(10<<20), bodyLimitFor(req("POST", "/api/v2/media", "multipart/form-data"), 10<<20, func() int64 { return 1 }))
}

func TestBodySizeLimitMiddleware_RejectsOversizedBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(BodySizeLimitMiddleware(16, func() int64 { return 1 }))
	r.POST("/json", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/json", strings.NewReader(`{"a":"0123456789abcdef"}`)))
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/json", strings.NewReader(`{}`)))
	assert.Equal(t, http.StatusNoContent, w.Code)
}
