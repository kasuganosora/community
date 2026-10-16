package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func streamingTokenContext(target string, headers map[string]string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	return c
}

func TestStreamingAccessTokenSources(t *testing.T) {
	assert.Equal(t, "q1", streamingAccessToken(streamingTokenContext("/api/v1/streaming?stream=user&access_token=q1", nil)))
	assert.Equal(t, "q2", streamingAccessToken(streamingTokenContext("/api/v1/streaming/user?access_token=q2", nil)))
	assert.Equal(t, "a+b/c=", streamingAccessToken(streamingTokenContext("/api/v1/streaming/feed?access_token=a+b/c=", nil)))
	assert.Equal(t, "q3", streamingAccessToken(streamingTokenContext("/api/v2/streaming?access_token=q3", nil)))
	assert.Equal(t, "p1", streamingAccessToken(streamingTokenContext("/api/v1/streaming", map[string]string{"Upgrade": "websocket", "Sec-WebSocket-Protocol": "p1"})))
	assert.Equal(t, "p2", streamingAccessToken(streamingTokenContext("/api/v1/streaming", map[string]string{"Upgrade": "websocket", "Sec-WebSocket-Protocol": "p2, other"})))
	// Ordinary API requests never take tokens from the URL.
	assert.Empty(t, streamingAccessToken(streamingTokenContext("/api/v1/timelines/home?access_token=q", nil)))
	assert.Empty(t, streamingAccessToken(streamingTokenContext("/api/v1/streamingx?access_token=q", nil)))
	// The subprotocol header is only a credential on a WebSocket upgrade.
	assert.Empty(t, streamingAccessToken(streamingTokenContext("/api/v1/streaming", map[string]string{"Sec-WebSocket-Protocol": "p"})))
}

// An access_token query parameter on a streaming endpoint is validated like a
// bearer header: a bad one is rejected instead of being silently ignored.
func TestAuthMiddlewareReadsStreamingAccessToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := func(target string) int {
		w := httptest.NewRecorder()
		_, r := gin.CreateTestContext(w)
		r.Use(AuthMiddleware())
		r.GET("/*path", func(c *gin.Context) { c.Status(http.StatusNoContent) })
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		return w.Code
	}
	assert.Equal(t, http.StatusUnauthorized, run("/api/v1/streaming?stream=user&access_token=invalid"))
	assert.Equal(t, http.StatusNoContent, run("/api/v1/timelines/home?access_token=invalid"))
}
