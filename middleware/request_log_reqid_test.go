package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/reqid"
)

func TestRequestLogPutsIDInRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestLogMiddleware())
	var seen string
	r.GET("/x", func(c *gin.Context) { seen = reqid.From(c.Request.Context()) })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-ID", "incoming-1")
	r.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "incoming-1" {
		t.Fatalf("got %q", seen)
	}
}
