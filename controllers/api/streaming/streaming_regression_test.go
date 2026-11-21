package streaming

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestProcessUserMsgMalformedJSONPreservesPubSub(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	existing := client.Subscribe(context.Background(), "subscribed:feed:public:all")
	defer existing.Close()

	got, err := processUserMsg(nil, []byte("{not-valid-json"), nil, existing)
	require.Error(t, err)
	require.Same(t, existing, got, "malformed WebSocket client JSON must not discard or leak existing PubSub")
}

func TestCurrentTimelineStatusAuthorizesLateIngestedFederatedPost(t *testing.T) {
	conn, _, viewer := streamPrivacyDB(t)
	setupRedisForTest(t)
	domain := "remote.example"
	remote := &models.Account{Id: 88, Username: "remote", Domain: &domain}
	require.NoError(t, conn.Create(remote).Error)

	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-10 * time.Minute)
	require.NoError(t, conn.Create(&models.Status{
		Id:         500,
		AccountId:  viewer.Id,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		Content:    models.StatusContent{Content: "local newer"},
	}).Error)
	// Federated post ingested later (higher serial ID 501) with an older remote timestamp.
	require.NoError(t, conn.Create(&models.Status{
		Id:         501,
		AccountId:  remote.Id,
		Domain:     &domain,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &earlier,
		Content:    models.StatusContent{Content: "federated older timestamp"},
	}).Error)

	rows, ctxName, err := currentTimelineStatus(viewer, 501, "subscribed:feed:public:all")
	require.NoError(t, err)
	require.Equal(t, "public", ctxName)
	require.Len(t, rows, 1)
	require.Equal(t, uint64(501), rows[0].Id)
}

func TestStreamHubMultiplexesChannelsOntoBoundedPubSubShards(t *testing.T) {
	resetFeedLifecycle(t)
	client := setupRedisForTest(t)
	ctx := context.Background()

	channels := make([]string, 24)
	for i := range channels {
		channels[i] = "subscribed:feed:test:chan-" + time.Duration(i).String()
	}

	hintSub, stopHints, err := subscribeFeedHints(ctx, channels)
	require.NoError(t, err)
	rawSub, stopRaws, err := subscribeRawChannels(ctx, channels[:8])
	require.NoError(t, err)

	shardPool.mu.Lock()
	shardCount := len(shardPool.shards)
	shardPool.mu.Unlock()
	require.LessOrEqual(t, shardCount, numPubSubShards, "24 channels must multiplex onto <= %d PubSub shards", numPubSubShards)
	require.Equal(t, 24, activeTopicCount())

	require.NoError(t, client.Publish(ctx, channels[3], `{"type":"update","payload":{"id":"777"}}`).Err())
	select {
	case notice := <-hintSub.ch:
		require.Equal(t, "update", notice.Event)
		require.Equal(t, "777", notice.ID)
	case <-time.After(time.Second):
		t.Fatal("did not receive multiplexed hint notice")
	}
	select {
	case msg := <-rawSub.ch:
		require.Equal(t, channels[3], msg.Channel)
		require.Contains(t, msg.Payload, "777")
	case <-time.After(time.Second):
		t.Fatal("did not receive multiplexed raw SSE message")
	}

	stopRaws()
	stopHints()
	require.Zero(t, activeTopicCount())

	shardPool.mu.Lock()
	remainingShards := len(shardPool.shards)
	shardPool.mu.Unlock()
	require.Zero(t, remainingShards)
}
