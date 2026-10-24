package streaming

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestFeedHintSubscriptionSharedAndContentFree(t *testing.T) {
	client := setupRedisForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	timeline := &home.HomeTimeline{Account: &models.Account{Id: 42}}
	channel := feed.ChangedChannel(timeline)
	first, stopFirst, err := subscribeFeedHint(ctx, channel)
	require.NoError(t, err)
	defer stopFirst()
	second, stopSecond, err := subscribeFeedHint(ctx, channel)
	require.NoError(t, err)
	defer stopSecond()
	counts, err := client.PubSubNumSub(ctx, channel).Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), counts[channel], "two tabs share a Redis subscription")
	require.NoError(t, client.Publish(ctx, channel, `{"type":"feed.changed","payload":{"epoch":"e","revision":"r","content":"private body","id":"secret-id"}}`).Err())
	for _, input := range []<-chan feedNotice{first, second} {
		select {
		case notice := <-input:
			encoded, err := json.Marshal(notice.Hint)
			require.NoError(t, err)
			require.JSONEq(t, `{"epoch":"e","revision":"r"}`, string(encoded))
			require.Empty(t, notice.Event)
		case <-ctx.Done():
			t.Fatal("missing hint")
		}
	}
	stopFirst()
	require.NoError(t, feed.PublishChanged(ctx, timeline, "e", "next"))
	select {
	case notice := <-second:
		require.Equal(t, "next", notice.Hint.Revision)
	case <-ctx.Done():
		t.Fatal("remaining subscriber was closed")
	}
}

func TestFeedStreamingAuthenticatedHTTPAndTimeoutExemption(t *testing.T) {
	conn, _, viewer := streamPrivacyDB(t)
	auth := feedAuthFixture(t, conn, viewer)
	setupRedisForTest(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// A normal request would expire before the subsequent event. Stream
	// handlers instead enforce their own cancellation/write deadlines.
	router.Use(middleware.TimeoutMiddleware(10 * time.Millisecond))
	router.GET("/api/v1/streaming/feed", func(c *gin.Context) { auth(c); FeedStreaming(c) })
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/streaming/feed?stream=user", nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	scanner := bufio.NewScanner(response.Body)
	skipSSEPadding(t, scanner)
	require.True(t, scanner.Scan())
	require.Equal(t, "event: feed.changed", scanner.Text())
	require.True(t, scanner.Scan())
	require.Contains(t, scanner.Text(), `"epoch":"subscribed"`)
	require.True(t, scanner.Scan())
	timer := time.NewTimer(25 * time.Millisecond)
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal("unexpected timeout")
	}
	timeline := &home.HomeTimeline{Account: viewer}
	require.NoError(t, feed.PublishChanged(ctx, timeline, "new-epoch", "opaque-revision"))
	var got string
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			got = strings.TrimPrefix(scanner.Text(), "data: ")
			break
		}
	}
	require.JSONEq(t, `{"epoch":"new-epoch","revision":"opaque-revision"}`, got)
	cancel()
}

func TestFeedHintKeepsStatusIDAndDropsBody(t *testing.T) {
	client := setupRedisForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	timeline := &home.HomeTimeline{Account: &models.Account{Id: 42}}
	notices, stop, err := subscribeFeedHint(ctx, feed.GetRedisChannelName(timeline))
	require.NoError(t, err)
	defer stop()
	require.NoError(t, client.Publish(ctx, feed.GetRedisChannelName(timeline), `{"type":"update","payload":{"id":19687,"content":"secret body","spoiler_text":"hidden"}}`).Err())
	select {
	case notice := <-notices:
		require.Equal(t, "update", notice.Event)
		require.Equal(t, "19687", notice.ID)
		require.Empty(t, notice.Hint.Epoch)
		encoded, err := json.Marshal(notice)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "secret")
		require.NotContains(t, string(encoded), "hidden")
	case <-ctx.Done():
		t.Fatal("missing status id")
	}
}

func TestFeedNotificationPayloadIsNotAHomeStatus(t *testing.T) {
	client := setupRedisForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	timeline := &home.HomeTimeline{Account: &models.Account{Id: 42}}
	channel := feed.GetRedisChannelName(timeline)
	notices, stop, err := subscribeFeedHint(ctx, channel)
	require.NoError(t, err)
	defer stop()

	publishes := []string{
		`{"type":"notification","payload":{"id":"9","type":"favourite","status":{"id":"100","content":"old post"}}}`,
		`{"type":"update","payload":{"id":"9","type":"favourite","status":{"id":"100","content":"old post"}}}`,
		`{"type":"update","payload":{"id":"8","type":"update","status":{"id":"55","content":"edited long ago"}}}`,
	}
	for _, body := range publishes {
		require.NoError(t, client.Publish(ctx, channel, body).Err())
		select {
		case notice := <-notices:
			require.Empty(t, notice.Event)
			require.Empty(t, notice.ID)
			require.Equal(t, "legacy", notice.Hint.Epoch)
			encoded, err := json.Marshal(notice)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "old post")
			require.NotContains(t, string(encoded), "edited long ago")
			require.NotContains(t, string(encoded), "100")
			require.NotContains(t, string(encoded), "55")
		case <-ctx.Done():
			t.Fatal("missing hint")
		}
	}

	require.NoError(t, client.Publish(ctx, channel, `{"type":"update","payload":{"id":19687,"content":"secret body"}}`).Err())
	select {
	case notice := <-notices:
		require.Equal(t, "update", notice.Event)
		require.Equal(t, "19687", notice.ID)
	case <-ctx.Done():
		t.Fatal("missing status id")
	}
}

func TestFeedStreamingForwardsStatusIDWithoutBody(t *testing.T) {
	conn, _, viewer := streamPrivacyDB(t)
	auth := feedAuthFixture(t, conn, viewer)
	client := setupRedisForTest(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/streaming/feed", func(c *gin.Context) { auth(c); FeedStreaming(c) })
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/streaming/feed?stream=user", nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	scanner := bufio.NewScanner(response.Body)
	skipSSEPadding(t, scanner)
	require.True(t, scanner.Scan())
	require.Equal(t, "event: feed.changed", scanner.Text())
	timeline := &home.HomeTimeline{Account: viewer}
	require.NoError(t, client.Publish(ctx, feed.GetRedisChannelName(timeline), `{"type":"delete","payload":{"id":42,"content":"secret body"}}`).Err())
	var eventLine, dataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") && line != "event: feed.changed" {
			eventLine = line
			continue
		}
		if eventLine != "" && strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	require.Equal(t, "event: delete", eventLine)
	require.JSONEq(t, `{"id":"42"}`, dataLine)
	require.NotContains(t, dataLine, "secret")
	cancel()
}

func TestFeedStreamingRejectsMissingAuthenticationAndPrivateList(t *testing.T) {
	conn, author, viewer := streamPrivacyDB(t)
	item := &models.List{Id: 77, AccountId: author.Id, Title: "Private group", Visibility: models.ListVisibilityPrivate}
	require.NoError(t, conn.Create(item).Error)
	router := gin.New()
	router.GET("/anonymous", FeedStreaming)
	router.GET("/private", func(c *gin.Context) { c.Set("account", viewer); FeedStreaming(c) })
	for _, path := range []string{"/anonymous", "/private?stream=list&list=77"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if path == "/anonymous" {
			require.Equal(t, 401, response.Code)
		} else {
			require.Equal(t, 403, response.Code)
		}
		require.NotContains(t, response.Body.String(), "Private group")
	}
}
