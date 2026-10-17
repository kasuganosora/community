package streaming

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/notification"
	"github.com/stretchr/testify/require"
)

// The web UI subscribes with stream=notification so a new notification of
// any type (e.g. a poll closing) refreshes the badge and list live.
func TestFeedStreamingNotificationStreamWakesOnNotification(t *testing.T) {
	conn, _, viewer := streamPrivacyDB(t)
	auth := feedAuthFixture(t, conn, viewer)
	setupRedisForTest(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/streaming/feed", func(c *gin.Context) { auth(c); FeedStreaming(c) })
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/streaming/feed?stream=notification", nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	scanner := bufio.NewScanner(response.Body)
	for i := 0; i < 3; i++ { // initial hint frame
		require.True(t, scanner.Scan())
	}
	time.Sleep(50 * time.Millisecond)
	timeline := &notification.NotificationTimeline{Account: viewer}
	// What the fan-out worker publishes for a new notification row.
	require.NoError(t, db.GetUniversalRedis().Publish(ctx, feed.GetRedisChannelName(timeline), `{"type":"notification","payload":{"id":"7"}}`).Err())
	got := ""
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			got = scanner.Text()
			break
		}
	}
	require.Contains(t, got, `"revision"`)
	require.NotContains(t, got, `"7"`, "hints never carry notification ids")
}
