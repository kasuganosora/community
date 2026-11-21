package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/resilience"
	"github.com/stretchr/testify/require"
)

func TestAdaptiveOverloadMiddlewareShedsWhenOverCapacity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	started := make(chan struct{})
	release := make(chan struct{})

	router := gin.New()
	router.Use(AdaptiveOverloadMiddleware(resilience.VegasConfig{
		MinLimit:     1,
		MaxLimit:     2,
		InitialLimit: 1,
	}))
	router.GET("/api/v1/test", func(c *gin.Context) {
		close(started)
		<-release
		c.String(http.StatusOK, "ok")
	})
	router.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "healthy")
	})

	done1 := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		router.ServeHTTP(w, req)
		done1 <- w.Code
	}()

	<-started

	// 2nd concurrent request should be shed with 503.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	router.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusServiceUnavailable, w2.Code)
	require.Equal(t, "1", w2.Header().Get("Retry-After"))

	// Health endpoint remains exempt even when overloaded.
	wHealth := httptest.NewRecorder()
	reqHealth := httptest.NewRequest(http.MethodGet, "/health", nil)
	router.ServeHTTP(wHealth, reqHealth)
	require.Equal(t, http.StatusOK, wHealth.Code)

	close(release)
	require.Equal(t, http.StatusOK, <-done1)
}
