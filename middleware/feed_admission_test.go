package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed"
	"github.com/stretchr/testify/require"
)

func TestFeedAdmissionStopsBusinessHandlerBeforeSaving(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/api/v1/statuses", "/api/v2/statuses", "/api/v1/statuses/12/replace", "/api/v2/statuses/12/reblog"} {
		t.Run(path, func(t *testing.T) {
			saved := 0
			engine := gin.New()
			engine.Use(feedAdmissionMiddleware(func(context.Context) error { return feed.ErrQueueCapacity }))
			engine.POST(path, func(c *gin.Context) { saved++; c.Status(200) })
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
			require.Equal(t, 503, response.Code)
			require.Equal(t, "5", response.Header().Get("Retry-After"))
			require.Zero(t, saved)
		})
	}
}
func TestFeedAdmissionDoesNotBlockDeletionOrReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	checked := 0
	saved := 0
	engine := gin.New()
	engine.Use(feedAdmissionMiddleware(func(context.Context) error { checked++; return feed.ErrQueueCapacity }))
	handler := func(c *gin.Context) { saved++; c.Status(200) }
	engine.GET("/api/v1/statuses", handler)
	engine.DELETE("/api/v1/statuses/12", handler)
	engine.POST("/api/v1/statuses/12/unreblog", handler)
	for _, request := range [][2]string{{"GET", "/api/v1/statuses"}, {"DELETE", "/api/v1/statuses/12"}, {"POST", "/api/v1/statuses/12/unreblog"}} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(request[0], request[1], nil))
		require.Equal(t, 200, response.Code)
	}
	require.Equal(t, 3, saved)
	require.Zero(t, checked)
}
