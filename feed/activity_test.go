package feed

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	redisdb "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func activityFixture(t *testing.T) (*gorm.DB, *miniredis.Miniredis, *redis.Client, *models.Account) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	pool, err := conn.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	oldDB := database.GetDB()
	database.SetTestDB(conn)
	server, client, service, _ := protocolFixture(t)
	var oldRedis redis.UniversalClient
	if redisdb.HasUniversalRedis() {
		oldRedis = redisdb.GetUniversalRedis()
	}
	redisdb.SetUniversalRedis(client)
	oldService, oldOverride := defaultService, defaultServiceOverride
	SetDefaultService(service)
	t.Cleanup(func() {
		database.SetTestDB(oldDB)
		redisdb.SetUniversalRedis(oldRedis)
		defaultService = oldService
		defaultServiceOverride = oldOverride
		pool.Close()
	})
	require.NoError(t, conn.AutoMigrate(&models.Account{}))
	old := time.Now().Add(-11 * 24 * time.Hour)
	account := &models.Account{Id: 42, Username: "returning-reader", FeedLastSeenAt: &old}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(account).Error)
	return conn, server, client, account
}
func activityStamp(t *testing.T, conn *gorm.DB, id uint64) *time.Time {
	var account models.Account
	require.NoError(t, conn.Select("feed_last_seen_at").First(&account, id).Error)
	return account.FeedLastSeenAt
}
func TestReturningReaderRedisFailureDoesNotAdvanceActivity(t *testing.T) {
	conn, _, client, account := activityFixture(t)
	before := *activityStamp(t, conn, account.Id)
	require.NoError(t, client.Close())
	require.Error(t, RecordActivity(context.Background(), account))
	require.WithinDuration(t, before, *activityStamp(t, conn, account.Id), time.Microsecond)
}
func TestReturningReaderPendingClaimIsNotSuccessAndRecoveryFencesWarmIndexes(t *testing.T) {
	conn, server, client, account := activityFixture(t)
	ctx := context.Background()
	key := "feed:activity:v2:42"
	require.NoError(t, client.Set(ctx, key, "unfinished-owner", 2*time.Second).Err())
	require.ErrorIs(t, RecordActivity(ctx, account), ErrActivityPending)
	require.True(t, activityStamp(t, conn, account.Id).Before(time.Now().Add(-10*24*time.Hour)))
	for _, kind := range []string{"home", "mention"} {
		timelineKey := TimelineKey(activityTimeline{kind, "42"})
		require.NoError(t, client.HSet(ctx, timelineKey+":meta", "epoch", "old", "generation", 1, "ready", 1).Err())
		require.NoError(t, client.ZAdd(ctx, timelineKey, redis.Z{Score: 1, Member: "1"}).Err())
	}
	server.FastForward(3 * time.Second)
	require.NoError(t, RecordActivity(ctx, account))
	require.Equal(t, "done", client.Get(ctx, key).Val())
	require.True(t, activityStamp(t, conn, account.Id).After(time.Now().Add(-time.Minute)))
	for _, kind := range []string{"home", "mention"} {
		timelineKey := TimelineKey(activityTimeline{kind, "42"})
		require.Equal(t, "0", client.HGet(ctx, timelineKey+":meta", "ready").Val())
		require.Zero(t, client.ZCard(ctx, timelineKey).Val())
	}
	// The authentication account may still contain an old cached timestamp.
	// Once the throttle expires, consult SQL rather than invalidating every5min.
	server.FastForward(6 * time.Minute)
	timelineKey := TimelineKey(activityTimeline{"home", "42"})
	require.NoError(t, client.HSet(ctx, timelineKey+":meta", "ready", 1).Err())
	require.NoError(t, RecordActivity(ctx, account))
	require.Equal(t, "1", client.HGet(ctx, timelineKey+":meta", "ready").Val())
}
func TestReturningReaderInvalidationErrorLeavesNoDoneMarker(t *testing.T) {
	conn, _, client, account := activityFixture(t)
	timelineKey := TimelineKey(activityTimeline{"home", "42"})
	require.NoError(t, client.Set(context.Background(), timelineKey+":meta", "wrongtype", 0).Err())
	require.Error(t, RecordActivity(context.Background(), account))
	require.True(t, activityStamp(t, conn, account.Id).Before(time.Now().Add(-10*24*time.Hour)))
	require.Zero(t, client.Exists(context.Background(), "feed:activity:v2:42").Val())
}
func TestActivationPendingUsesSQLInsteadOfWarmIncompleteWindow(t *testing.T) {
	conn, _, client, account := activityFixture(t)
	require.NoError(t, conn.AutoMigrate(&models.Status{}, &models.AccountExtra{}, &models.StatusContent{}, &models.Application{}, &models.Attachment{}, &models.Emoji{}, &models.Poll{}, &models.PollOption{}, &models.Tag{}, &models.DomainBlock{}, &models.Block{}, &models.Follow{}, &models.MuteAccount{}))
	write := conn.Session(&gorm.Session{SkipHooks: true})
	require.NoError(t, write.Create(&models.AccountExtra{AccountId: account.Id}).Error)
	require.NoError(t, write.Create(&models.Status{Id: 10, AccountId: account.Id, Visibility: models.StatusVisibilityPublic}).Error)
	ctx := context.Background()
	timeline := activityTimeline{"home", "42"}
	key := TimelineKey(timeline)
	require.NoError(t, client.Set(ctx, "feed:activity:v2:42", "in-progress", time.Second).Err())
	require.NoError(t, client.HSet(ctx, key+":meta", "epoch", "old", "generation", 1, "ready", 1).Err())
	got, err := GetStatusTimelineContent(timeline, &ListParam{Context: ctx, Limit: 20, Viewer: account}, func(q *gorm.DB) (*gorm.DB, error) { return q, nil }, nil)
	require.NoError(t, err)
	require.Equal(t, []uint64{10}, got.GetIds(), "an old ready empty index is not authoritative during returning-reader activation")
}
func TestAdmissionFullQueuesAndRequestCancellation(t *testing.T) {
	conn, _, _, _ := activityFixture(t)
	require.NoError(t, conn.AutoMigrate(&models.FeedEvent{}, &models.FeedBatch{}))
	reset := func() {
		admissionState.Lock()
		admissionState.checked = time.Time{}
		admissionState.err = nil
		admissionState.Unlock()
	}
	reset()
	t.Cleanup(reset)
	t.Setenv("FEED_MAX_PENDING_EVENTS", "1")
	t.Setenv("FEED_MAX_PENDING_BATCHES", "1")
	require.NoError(t, admitWrite(context.Background()))
	event := models.FeedEvent{EventKey: "budget", Kind: models.FeedStatusCreate, Metadata: models.FeedEventMetadata{}}
	require.NoError(t, conn.Create(&event).Error)
	reset()
	require.ErrorIs(t, admitWrite(context.Background()), ErrQueueCapacity)
	require.NoError(t, conn.Model(&event).UpdateColumn("state", "complete").Error)
	require.NoError(t, conn.Create(&models.FeedBatch{EventID: event.ID, TargetKind: "home", Recipients: []models.FeedRecipient{}}).Error)
	reset()
	require.ErrorIs(t, admitWrite(context.Background()), ErrQueueCapacity)
	require.NoError(t, conn.Model(&models.FeedBatch{}).Where("event_id = ?", event.ID).UpdateColumn("state", "cancelled").Error)
	reset()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, admitWrite(cancelled), context.Canceled)
	require.NoError(t, admitWrite(context.Background()), "a cancelled client must not poison the shared budget cache")
	reset()
	require.NoError(t, conn.Migrator().DropTable(&models.FeedBatch{}))
	require.Error(t, admitWrite(context.Background()))
	require.False(t, errors.Is(admissionState.err, ErrQueueCapacity), "database failure must fail closed rather than masquerade as available capacity")
}
