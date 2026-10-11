package streaming

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	redisdb "github.com/kasuganosora/community/db/redis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func resetFeedLifecycle(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { CloseFeedTopics(); feedTopics.Lock(); feedTopics.closed = false; feedTopics.Unlock() })
}

func TestFeedPendingSubscriptionDoesNotBlockCleanupOrCanceledWaiter(t *testing.T) {
	resetFeedLifecycle(t)
	setupRedisForTest(t)
	_, stop, err := subscribeFeedHint(context.Background(), "working")
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	stalled := redis.NewClient(&redis.Options{Addr: listener.Addr().String(), ContextTimeoutEnabled: true, ReadTimeout: time.Second})
	defer stalled.Close()
	redisdb.SetUniversalRedis(stalled)
	defer redisdb.SetUniversalRedis(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, cleanup, err := subscribeFeedHint(ctx, "pending")
		if cleanup != nil {
			cleanup()
		}
		done <- err
	}()
	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-time.After(time.Second):
		t.Fatal("subscription did not start")
	}
	cleaned := make(chan struct{})
	go func() { stop(); close(cleaned) }()
	select {
	case <-cleaned:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("network wait holds registry lock")
	}
	canceled, stopCanceled := context.WithCancel(context.Background())
	stopCanceled()
	_, _, err = subscribeFeedHint(canceled, "pending")
	require.ErrorIs(t, err, context.Canceled)
	waiting, stopWaiting := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() { _, _, err := subscribeFeedHint(waiting, "pending"); waiterDone <- err }()
	stopWaiting()
	select {
	case err := <-waiterDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(300 * time.Millisecond):
		t.Fatal("waiter ignored cancellation")
	}
	CloseFeedTopics()
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(4 * time.Second):
		t.Fatal("initializer did not stop")
	}
	feedTopics.Lock()
	remaining := len(feedTopics.entries)
	feedTopics.Unlock()
	require.Zero(t, remaining)
}

func TestFeedShutdownEndsActiveHTTPStream(t *testing.T) {
	resetFeedLifecycle(t)
	conn, _, viewer := streamPrivacyDB(t)
	auth := feedAuthFixture(t, conn, viewer)
	setupRedisForTest(t)
	router := gin.New()
	router.GET("/feed", func(c *gin.Context) { auth(c); FeedStreaming(c) })
	server := httptest.NewServer(router)
	defer server.Close()
	response, err := http.Get(server.URL + "/feed")
	require.NoError(t, err)
	defer response.Body.Close()
	finished := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, response.Body); finished <- err }()
	CloseFeedTopics()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("active SSE prevented graceful shutdown")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, server.Config.Shutdown(ctx))
	_, _, err = subscribeFeedHint(context.Background(), "late-request")
	require.ErrorIs(t, err, context.Canceled)
}
