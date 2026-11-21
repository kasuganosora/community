package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStreamingAccessTokenIgnoresPublicSubprotocol(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("GET", "/api/v1/streaming/feed?stream=user", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Protocol", CommunityTestSubprotocol())
	c.Request = req

	require.Equal(t, "", streamingAccessToken(c), "public community.v1 subprotocol must not mask cookie authentication")

	// Legacy Mastodon raw token subprotocol still works.
	req2 := httptest.NewRequest("GET", "/api/v1/streaming?stream=user", nil)
	req2.Header.Set("Upgrade", "websocket")
	req2.Header.Set("Sec-WebSocket-Protocol", "legacy-oauth-token")
	c.Request = req2
	require.Equal(t, "legacy-oauth-token", streamingAccessToken(c))
}

func CommunityTestSubprotocol() string { return "community.v1" }
