package mention

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupMentionRead(t *testing.T) (*gorm.DB, *models.Account) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.AutoMigrate(append(append([]any{}, integrationModels...), &models.Block{}, &models.MuteAccount{}, &models.Application{}, &models.Attachment{}, &models.Emoji{}, &models.Poll{}, &models.PollOption{})...))
	database.SetTestDB(conn)
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	old := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{client})
	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(client)))
	t.Cleanup(func() { cRedis.SetRedisesForTest(old); database.ResetTestDB(); _ = client.Close(); _ = sqlDB.Close() })
	accounts := []*models.Account{{Id: 1, Username: "recipient"}, {Id: 2, Username: "author"}, {Id: 3, Username: "outsider"}, {Id: 4, Username: "banned", IsBan: true}}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&accounts).Error)
	return conn, accounts[0]
}

func insertMentionRows(t *testing.T, conn *gorm.DB, statuses models.Statuses) {
	t.Helper()
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&statuses).Error)
	rows := make([]*models.StatusToAccount, 0, len(statuses))
	for _, s := range statuses {
		rows = append(rows, &models.StatusToAccount{StatusId: s.Id, AccountId: 1})
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&rows).Error)
}

func TestMentionReadPrivateDirectAndHotPush(t *testing.T) {
	conn, viewer := setupMentionRead(t)
	insertMentionRows(t, conn, models.Statuses{
		{Id: 1, AccountId: 2, Visibility: models.StatusVisibilityPrivate},
		{Id: 2, AccountId: 2, Visibility: models.StatusVisibilityDirect},
		{Id: 3, AccountId: 2, Visibility: models.StatusVisibilityPublic},
	})
	for _, phase := range []string{"cold", "hot"} {
		t.Run(phase, func(t *testing.T) {
			statuses, err := GetTimelineContent(viewer, &feed.ListParam{Limit: 3})
			require.NoError(t, err)
			require.Equal(t, []uint64{3, 2, 1}, statuses.GetIds())
		})
	}
	direct := &models.Status{Id: 4, AccountId: 2, Visibility: models.StatusVisibilityDirect}
	insertMentionRows(t, conn, models.Statuses{direct})
	event.Trigger("status", event.Event{Name: "after_create", Data: direct})
	require.Eventually(t, func() bool {
		return cRedis.GetRedis().ZScore(context.Background(), "feed:mention:1", "4").Err() == nil
	}, 2*time.Second, 5*time.Millisecond, "direct events must reach the authorized recipient hot cache")
	statuses, err := GetTimelineContent(viewer, &feed.ListParam{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []uint64{4}, statuses.GetIds())
	outsider := &models.Account{Id: 3}
	statuses, err = GetTimelineContent(outsider, &feed.ListParam{Limit: 3})
	require.NoError(t, err)
	require.Empty(t, statuses)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Block{AccountId: 2, TargetAccountId: 1, Type: models.BlockTypeAccount}).Error)
	statuses, err = GetTimelineContent(viewer, &feed.ListParam{Limit: 3})
	require.NoError(t, err)
	require.Empty(t, statuses)
}

func TestMentionDeepHistoryAndPublicationPredicates(t *testing.T) {
	conn, viewer := setupMentionRead(t)
	total := feed.MAX_ITEMS/2 + 30
	rows := make(models.Statuses, 0, total+3)
	for id := 1; id <= total; id++ {
		rows = append(rows, &models.Status{Id: uint64(id), AccountId: 2, Visibility: models.StatusVisibilityDirect})
	}
	future := time.Now().Add(time.Hour)
	rows = append(rows, &models.Status{Id: uint64(total + 1), AccountId: 2, Visibility: models.StatusVisibilityPublic, ScheduledAt: &future},
		&models.Status{Id: uint64(total + 2), AccountId: 2, Visibility: models.StatusVisibilityPublic, DeletedAt: &future},
		&models.Status{Id: uint64(total + 3), AccountId: 4, Visibility: models.StatusVisibilityPublic})
	insertMentionRows(t, conn, rows)
	timeline := &MentionTimeline{Account: viewer}
	require.NoError(t, timeline.RebuildContext(context.Background()))
	var candidateIDs []uint64
	require.NoError(t, mentionCandidates(conn, viewer.Id).Order("statuses.id DESC").Limit(1).Pluck("statuses.id", &candidateIDs).Error)
	require.Equal(t, []uint64{uint64(total)}, candidateIDs, "unpublished/banned entries must be removed before LIMIT")
	statuses, err := GetTimelineContent(viewer, &feed.ListParam{MaxId: 30, Limit: 20})
	require.NoError(t, err)
	expected := make([]uint64, 20)
	for i := range expected {
		expected[i] = uint64(29 - i)
	}
	require.Equal(t, expected, statuses.GetIds(), fmt.Sprintf("must read beyond newest %d mention links", feed.MAX_ITEMS/2))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = GetTimelineContent(viewer, &feed.ListParam{Context: ctx, Limit: 20})
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, timeline.RebuildContext(ctx), context.Canceled)
}

func TestMentionReadRespectsReblogAggregationSetting(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		t.Run(fmt.Sprint(aggregate), func(t *testing.T) {
			conn, viewer := setupMentionRead(t)
			viewer.User = &models.User{Settings: &models.UserSetting{Data: map[string]any{"aggregates_reblogs": aggregate}}}
			original := uint64(10)
			insertMentionRows(t, conn, models.Statuses{
				{Id: original, AccountId: 2, Visibility: models.StatusVisibilityPublic},
				{Id: 20, AccountId: 2, Visibility: models.StatusVisibilityPublic, ReblogId: &original},
			})
			statuses, err := GetTimelineContent(viewer, &feed.ListParam{Limit: 20})
			require.NoError(t, err)
			expected := []uint64{20, 10}
			if aggregate {
				expected = []uint64{20}
			}
			require.Equal(t, expected, statuses.GetIds())
		})
	}
}
