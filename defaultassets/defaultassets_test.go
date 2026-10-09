package defaultassets

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestHandlerServesPNG(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET(Prefix+":name", Handler)

	for _, n := range []string{"avatar.png", "header.png", "thumbnail.png", "thumbnail@2x.png", "icon-64.png", "icon-192.png", "icon-512.png"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, Path(n), nil))
		assert.Equal(t, 200, w.Code, n)
		assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
		assert.Equal(t, "\x89PNG", w.Body.String()[:4])
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, Path("nope.png"), nil))
	assert.Equal(t, 404, w.Code)
}
