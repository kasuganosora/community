package streaming

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/middleware"
)

func TestFeedStreamingWebSocket(t *testing.T) {
	conn, _, viewer := streamPrivacyDB(t)
	auth := feedAuthFixture(t, conn, viewer)
	setupRedisForTest(t)
	previous := sseHeartbeatInterval
	sseHeartbeatInterval = 50 * time.Millisecond
	t.Cleanup(func() { sseHeartbeatInterval = previous })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.TimeoutMiddleware(10 * time.Millisecond))
	router.GET("/api/v1/streaming/feed", func(c *gin.Context) { auth(c); FeedStreaming(c) })
	server := httptest.NewServer(router)
	defer server.Close()

	dialer := websocket.Dialer{Subprotocols: []string{"community.v1", "community.bearer.dGVzdA"}}
	ws, response, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/streaming/feed?stream=user", nil)
	require.NoError(t, err)
	defer ws.Close()
	// Only the first, token-free protocol is echoed.
	require.Equal(t, "community.v1", response.Header.Get("Sec-WebSocket-Protocol"))

	read := func() StreamingResponse {
		t.Helper()
		require.NoError(t, ws.SetReadDeadline(time.Now().Add(3*time.Second)))
		var msg StreamingResponse
		require.NoError(t, ws.ReadJSON(&msg))
		return msg
	}
	first := read()
	require.Equal(t, "feed.changed", first.Event)
	require.Contains(t, first.Payload, `"epoch":"subscribed"`)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, feed.PublishChanged(ctx, &home.HomeTimeline{Account: viewer}, "new-epoch", "rev-1"))
	sawHint, sawBeat := false, false
	for !(sawHint && sawBeat) {
		msg := read()
		switch msg.Event {
		case "feed.changed":
			var hint map[string]string
			require.NoError(t, json.Unmarshal([]byte(msg.Payload), &hint))
			require.Equal(t, "rev-1", hint["revision"])
			sawHint = true
		case "heartbeat":
			sawBeat = true
		}
	}
}
