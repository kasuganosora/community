package notification

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type privacyRedis struct {
	*feed.MockRedisClient
	publications int
}

func (r *privacyRedis) Publish(ctx context.Context, channel string, message any) *redis.IntCmd {
	r.publications++
	return r.MockRedisClient.Publish(ctx, channel, message)
}
func TestNotificationStreamSkipsBlockedPayload(t *testing.T) {
	env := setupNotifEnv(t)
	require.NoError(t, env.gormDB.AutoMigrate(&models.Block{}))
	writer := env.gormDB.Session(&gorm.Session{SkipHooks: true})
	author, viewer := &models.Account{Id: 1, Username: "author"}, &models.Account{Id: 2, Username: "viewer"}
	require.NoError(t, writer.Omit("Extra").Create(author).Error)
	require.NoError(t, writer.Omit("Extra").Create(viewer).Error)
	require.NoError(t, writer.Create(&models.Block{AccountId: 1, TargetAccountId: 2, Type: models.BlockTypeAccount}).Error)
	observed := &privacyRedis{MockRedisClient: env.mockRedis}
	feed.SetDefaultService(feed.NewFeedService(observed))
	feed.ResetSubscriptionCache()
	env.mockRedis.SetChannel("subscribed:feed:notification:2", true)
	n := &models.Notification{Id: 40, AccountId: 2, FromAccountId: 1, Type: models.NotificationTypeFollow}
	env.mock.ExpectZAdd("feed:notification:2", redis.Z{Score: 40, Member: uint64(40)}).SetVal(1)
	timeline := &NotificationTimeline{Account: viewer}
	require.NoError(t, timeline.push(n, true, false))
	require.Zero(t, observed.publications, "blocked payload must not publish even JSON null")
	require.NoError(t, env.mock.ExpectationsWereMet())
}
