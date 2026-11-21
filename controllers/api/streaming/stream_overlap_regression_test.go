package streaming

import (
	"context"
	"testing"
	"time"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/feed/notification"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestUserAndNotificationStreamsShareChannelUntilBothUnsubscribe(t *testing.T) {
	client := setupRedisForTest(t)
	useUniversalRedis(t, client)
	account := &models.Account{Id: 42}
	ctx := context.Background()

	pub, err := subscribe(nil, account, &StreamingRequest{Type: "subscribe", Stream: "user"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() })
	pub, err = subscribe(pub, account, &StreamingRequest{Type: "subscribe", Stream: "user:notification"})
	require.NoError(t, err)

	homeCh := feed.GetRedisChannelName(&home.HomeTimeline{Account: account})
	noteCh := feed.GetRedisChannelName(&notification.NotificationTimeline{Account: account})

	pub, err = unsubscribe(pub, account, &StreamingRequest{Type: "unsubscribe", Stream: "user:notification"})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		counts, err := client.PubSubNumSub(ctx, homeCh, noteCh).Result()
		return err == nil && counts[homeCh] == 1 && counts[noteCh] == 1
	}, time.Second, 10*time.Millisecond)

	pub, err = subscribe(pub, account, &StreamingRequest{Type: "subscribe", Stream: "user:notification"})
	require.NoError(t, err)
	pub, err = unsubscribe(pub, account, &StreamingRequest{Type: "unsubscribe", Stream: "user"})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		counts, err := client.PubSubNumSub(ctx, homeCh, noteCh).Result()
		return err == nil && counts[homeCh] == 0 && counts[noteCh] == 1
	}, time.Second, 10*time.Millisecond)

	receivers, err := client.Publish(ctx, noteCh, `{"type":"notification","payload":{"id":"7"}}`).Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), receivers)

	_, err = unsubscribe(pub, account, &StreamingRequest{Type: "unsubscribe", Stream: "user:notification"})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		counts, err := client.PubSubNumSub(ctx, noteCh).Result()
		return err == nil && counts[noteCh] == 0
	}, time.Second, 10*time.Millisecond)
}
