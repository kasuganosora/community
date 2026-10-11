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
	key := "feed:v3:{home:91}:items"
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

func TestInvalidateFencesRebuildAndHonorsCancellation(t *testing.T) {
	_, client, service, timeline := protocolFixture(t)
	ctx := context.Background()
	lease, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, service.InvalidateTimelineContext(canceled, timeline), context.Canceled)
	require.NoError(t, service.InvalidateTimelineContext(ctx, timeline))
	require.ErrorIs(t, service.PublishRebuild(ctx, timeline, lease, nil, false), ErrFeedRegenerating)
	require.Equal(t, "0", client.HGet(ctx, TimelineKey(timeline)+":meta", "ready").Val())
}
