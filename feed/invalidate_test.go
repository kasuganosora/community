package feed

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestInvalidateTimelineClearsAggregationAndNegativeCache(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()
	key := "feed:home:91"
	require.NoError(t, client.ZAdd(ctx, key, redis.Z{Score: 1, Member: "1"}).Err())
	require.NoError(t, client.ZAdd(ctx, key+":reblogs", redis.Z{Score: 1, Member: "2"}).Err())
	require.NoError(t, client.SAdd(ctx, key+":reblogs:2", "3").Err())
	require.NoError(t, client.Set(ctx, key+":empty", "1", 0).Err())
	require.NoError(t, client.Set(ctx, "unrelated", "keep", 0).Err())
	service := NewFeedService(NewRedisClient(client))
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "91"}
	require.NoError(t, service.InvalidateTimelineContext(ctx, timeline))
	for _, suffix := range []string{"", ":reblogs", ":reblogs:2", ":empty"} {
		require.False(t, server.Exists(key+suffix))
	}
	require.True(t, server.Exists("unrelated"))
}

func TestInvalidateWaitsForRebuildAndHonorsCancellation(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	service := NewFeedService(NewRedisClient(client))
	key := "feed:home:92"
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "92"}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- coalesceRebuildContext(context.Background(), key, func() error {
			close(started)
			<-release
			return client.Set(context.Background(), key+":empty", "1", 0).Err()
		})
	}()
	<-started
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, service.InvalidateTimelineContext(canceled, timeline), context.Canceled)
	cleaned := make(chan error, 1)
	go func() { cleaned <- service.InvalidateTimelineContext(context.Background(), timeline) }()
	close(release)
	require.NoError(t, <-finished)
	require.NoError(t, <-cleaned)
	require.False(t, server.Exists(key+":empty"))
}
