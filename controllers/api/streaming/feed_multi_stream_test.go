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
	"github.com/kasuganosora/community/feed/notification"
	"github.com/stretchr/testify/require"
)

type testFeedStream struct {
	cancel context.CancelFunc
	hints  chan string
}

func openTestFeedStream(t *testing.T, url string) *testFeedStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode)
	scanner := bufio.NewScanner(response.Body)
	for i := 0; i < 3; i++ { // the "subscribed" frame
		require.True(t, scanner.Scan())
	}
	s := &testFeedStream{cancel: cancel, hints: make(chan string, 64)}
	go func() {
		defer response.Body.Close()
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "data: ") {
				s.hints <- scanner.Text()
			}
		}
	}()
	return s
}

func (s *testFeedStream) expectHint(t *testing.T, label string) {
	t.Helper()
	select {
	case <-s.hints:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: no hint", label)
	}
	// Coalesced follow-ups from the same publish are fine; drain them.
	for {
		select {
		case <-s.hints:
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

// Several concurrent streams of one account share the per-process Redis
// subscription; every one of them must get every hint, and closing (or
// churning) one must not starve the others.
func TestFeedStreamingConcurrentStreamsOfOneAccountAllReceiveHints(t *testing.T) {
	conn, _, viewer := streamPrivacyDB(t)
	auth := feedAuthFixture(t, conn, viewer)
	setupRedisForTest(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/streaming/feed", func(c *gin.Context) { auth(c); FeedStreaming(c) })
	server := httptest.NewServer(router)
	defer server.Close()
	url := server.URL + "/api/v1/streaming/feed?stream=notification&unread=1"
	ctx := context.Background()
	rdb := db.GetUniversalRedis()
	dm := feed.GetRedisChannelName(&direct.DirectTimeline{Account: viewer})
	note := feed.GetRedisChannelName(&notification.NotificationTimeline{Account: viewer})

	streams := []*testFeedStream{}
	for i := 0; i < 4; i++ {
		streams = append(streams, openTestFeedStream(t, url))
	}
	defer func() {
		for _, s := range streams {
			s.cancel()
		}
	}()
	publishAll := func(label string, live []*testFeedStream) {
		require.NoError(t, rdb.Publish(ctx, dm, `{"type":"conversation","payload":{"id":1}}`).Err())
		require.NoError(t, rdb.Publish(ctx, note, `{"type":"notification","payload":{"id":"n"}}`).Err())
		for i, s := range live {
			s.expectHint(t, label+" stream "+string(rune('A'+i)))
		}
	}
	publishAll("all open", streams)

	// Close the first (the topic creator) and one in the middle.
	streams[0].cancel()
	streams[2].cancel()
	time.Sleep(100 * time.Millisecond)
	live := []*testFeedStream{streams[1], streams[3]}
	publishAll("after closing two", live)

	// Churn: short-lived streams come and go while the long-lived ones stay.
	for i := 0; i < 5; i++ {
		s := openTestFeedStream(t, url)
		s.cancel()
	}
	time.Sleep(100 * time.Millisecond)
	late := openTestFeedStream(t, url)
	streams = append(streams, late)
	publishAll("after churn", append(live, late))
}
