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
	"github.com/kasuganosora/community/feed/direct"
	"github.com/stretchr/testify/require"
)

// The first frame is sent only after Redis confirmed every SUBSCRIBE and is a
// unique "subscribed" hint, so a DM published the instant it arrives (with no
// settling delay) still reaches the stream, and the client always recounts.
func TestFeedStreamingUnreadHintAfterSubscribeConfirmed(t *testing.T) {
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

	firstHints := map[string]bool{}
	for i := 0; i < 2; i++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/streaming/feed?stream=notification&unread=1", nil)
		require.NoError(t, err)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode)
		scanner := bufio.NewScanner(response.Body)
		skipSSEPadding(t, scanner)
		require.True(t, scanner.Scan())
		require.Equal(t, "event: feed.changed", scanner.Text())
		require.True(t, scanner.Scan())
		first := scanner.Text()
		require.Contains(t, first, `"epoch":"subscribed"`)
		require.NotContains(t, first, `"revision":""`)
		firstHints[first] = true
		require.True(t, scanner.Scan()) // blank line

		// No sleep: the subscription must already be live.
		dm := feed.GetRedisChannelName(&direct.DirectTimeline{Account: viewer})
		require.NoError(t, db.GetUniversalRedis().Publish(ctx, dm, `{"type":"conversation","payload":{"id":1}}`).Err())
		got := ""
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "data: ") {
				got = scanner.Text()
				break
			}
		}
		require.Contains(t, got, `"epoch":"legacy"`, "DM hint published right after the first frame must arrive")
		response.Body.Close()
	}
	require.Len(t, firstHints, 2, "each connection's subscribed hint has a fresh revision")
}
