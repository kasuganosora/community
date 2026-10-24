package feed_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/feed/mention"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/feed/tag"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A federated post ingested late gets a higher serial id than posts that were
// published after it. Timelines must still show publication time, so the
// 18-hour post sits above the 1-day post.
func TestLateIngestedStatusOrdersByCreatedAt(t *testing.T) {
	conn, client := setupTimelineOrder(t)
	remote := "remote.test"
	seen := time.Now().UTC()
	viewer := &models.Account{Id: 1, Username: "viewer", FeedLastSeenAt: &seen}
	pizza := &models.Account{Id: 2, Username: "pizza", Domain: &remote}
	youon := &models.Account{Id: 3, Username: "youon", Domain: &remote}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Account{viewer, pizza, youon}).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Follow{
		{AccountId: viewer.Id, FollowId: pizza.Id, Status: models.FollowStatusFollowed},
		{AccountId: viewer.Id, FollowId: youon.Id, Status: models.FollowStatusFollowed},
	}).Error)

	newest := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	youonAt := time.Date(2026, 10, 24, 0, 55, 0, 0, time.UTC)
	pizzaAt := time.Date(2026, 10, 23, 11, 31, 0, 0, time.UTC)
	insertStatus(t, conn, &models.Status{Id: 100, AccountId: youon.Id, Visibility: models.StatusVisibilityPublic, Domain: &remote}, newest)
	insertStatus(t, conn, &models.Status{Id: 22464, AccountId: youon.Id, Visibility: models.StatusVisibilityPublic, Domain: &remote}, youonAt)
	insertStatus(t, conn, &models.Status{Id: 22481, AccountId: pizza.Id, Visibility: models.StatusVisibilityPublic, Domain: &remote}, pizzaAt)
	want := []uint64{100, 22464, 22481}

	for _, id := range want {
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.StatusToAccount{StatusId: id, AccountId: viewer.Id}).Error)
	}
	hashtag := &models.Tag{Name: "late"}
	require.NoError(t, conn.Create(hashtag).Error)
	for _, id := range want {
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.StatusTag{StatusId: id, TagId: hashtag.Id}).Error)
	}
	rows := &models.List{Id: 7, Title: "fed", AccountId: viewer.Id, RepliesPolicy: models.ListRepliesPolicyNone}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(rows).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.ListAccount{
		{ListId: rows.Id, AccountId: pizza.Id},
		{ListId: rows.Id, AccountId: youon.Id},
	}).Error)

	publicKey := feed.TimelineKey(&public.PublicTimeline{})
	ctx := context.Background()

	t.Run("cached id scores are re-sorted by publication time", func(t *testing.T) {
		seedTimeline(t, client, publicKey, map[string]float64{"22481": 22481, "22464": 22464, "100": 100}, feed.TimelineRankCreatedAt)
		got, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 10})
		require.NoError(t, err)
		require.Equal(t, want, got.GetIds())
		members, err := client.ZRevRange(ctx, publicKey, 0, -1).Result()
		require.NoError(t, err)
		require.Equal(t, []string{"22481", "22464", "100"}, members, "reading must not rebuild a cache that already carries the publication rank")
	})

	t.Run("cache window shorter than the page continues in the database", func(t *testing.T) {
		seedTimeline(t, client, publicKey, map[string]float64{"100": float64(newest.UnixMicro())}, feed.TimelineRankCreatedAt)
		first, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 2})
		require.NoError(t, err)
		require.Equal(t, []uint64{100, 22464}, first.GetIds())
		second, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 2, MaxId: first[len(first)-1].Id})
		require.NoError(t, err)
		require.Equal(t, []uint64{22481}, second.GetIds())
	})

	t.Run("id-ranked cache rebuilds before it is served", func(t *testing.T) {
		seedTimeline(t, client, publicKey, map[string]float64{"22481": 22481}, "")
		got, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 10})
		require.NoError(t, err)
		require.Equal(t, want, got.GetIds())
		members, err := client.ZRevRange(ctx, publicKey, 0, -1).Result()
		require.NoError(t, err)
		require.Equal(t, []string{"100", "22464", "22481"}, members)
		require.Equal(t, feed.TimelineRankCreatedAt, client.HGet(ctx, publicKey+":meta", "rank").Val())
	})

	t.Run("pages do not skip or repeat", func(t *testing.T) {
		require.Equal(t, want, pageIDs(t, 2, func(p *feed.ListParam) (models.Statuses, error) {
			return home.GetTimeLineContent(viewer, p)
		}))
		require.Equal(t, want, pageIDs(t, 2, func(p *feed.ListParam) (models.Statuses, error) {
			return public.GetTimelineContentAll(p)
		}))
		require.Equal(t, want, pageIDs(t, 2, func(p *feed.ListParam) (models.Statuses, error) {
			return list.GetTimelineContent(rows, p)
		}))
		require.Equal(t, want, pageIDs(t, 2, func(p *feed.ListParam) (models.Statuses, error) {
			return tag.GetTimelineContent("late", nil, p)
		}))
		require.Equal(t, want, pageIDs(t, 2, func(p *feed.ListParam) (models.Statuses, error) {
			return mention.GetTimelineContent(viewer, p)
		}))

		homeKey := feed.TimelineKey(&home.HomeTimeline{Account: viewer})
		members, err := client.ZRevRange(ctx, homeKey, 0, -1).Result()
		require.NoError(t, err)
		require.Equal(t, []string{"100", "22464", "22481"}, members)

		local, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 10, Other: map[string]any{"local": true}})
		require.NoError(t, err)
		require.Empty(t, local, "these posts are remote and belong on the global timeline")

		older, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 10, MaxId: 100, Other: map[string]any{"database_only": true}})
		require.NoError(t, err)
		require.Equal(t, []uint64{22464, 22481}, older.GetIds())
		middle, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 10, MaxId: 22464, Other: map[string]any{"database_only": true}})
		require.NoError(t, err)
		require.Equal(t, []uint64{22481}, middle.GetIds())
		newer, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 10, SinceId: 22464, Other: map[string]any{"database_only": true}})
		require.NoError(t, err)
		require.Equal(t, []uint64{100}, newer.GetIds())
		aboveOldest, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 10, MinId: 22481, Other: map[string]any{"database_only": true}})
		require.NoError(t, err)
		require.Equal(t, []uint64{100, 22464}, aboveOldest.GetIds())

		require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 22464).UpdateColumn("deleted_at", time.Now().UTC()).Error)
		afterDelete, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 10, MaxId: 22464, Other: map[string]any{"database_only": true}})
		require.NoError(t, err)
		require.Equal(t, []uint64{22481}, afterDelete.GetIds(), "a soft-deleted cursor keeps its publication time")
		require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 22464).UpdateColumn("deleted_at", nil).Error)
	})
}

func TestEqualCreatedAtBreaksTiesByID(t *testing.T) {
	conn, _ := setupTimelineOrder(t)
	seen := time.Now().UTC()
	viewer := &models.Account{Id: 1, Username: "viewer", FeedLastSeenAt: &seen}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(viewer).Error)
	same := time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)
	insertStatus(t, conn, &models.Status{Id: 40, AccountId: viewer.Id, Visibility: models.StatusVisibilityPublic}, same)
	insertStatus(t, conn, &models.Status{Id: 50, AccountId: viewer.Id, Visibility: models.StatusVisibilityPublic}, same)
	page, err := home.GetTimeLineContent(viewer, &feed.ListParam{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []uint64{50, 40}, page.GetIds())
	next, err := home.GetTimeLineContent(viewer, &feed.ListParam{Limit: 10, MaxId: 50})
	require.NoError(t, err)
	require.Equal(t, []uint64{40}, next.GetIds())
}

func TestPublicLocalOrdersByCreatedAt(t *testing.T) {
	conn, _ := setupTimelineOrder(t)
	author := &models.Account{Id: 1, Username: "local"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(author).Error)
	older := time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	insertStatus(t, conn, &models.Status{Id: 500, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}, older)
	insertStatus(t, conn, &models.Status{Id: 400, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}, newer)
	got, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 10, Other: map[string]any{"local": true}})
	require.NoError(t, err)
	require.Equal(t, []uint64{400, 500}, got.GetIds())
	next, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 10, MaxId: 400, Other: map[string]any{"local": true}})
	require.NoError(t, err)
	require.Equal(t, []uint64{500}, next.GetIds())
}

func setupTimelineOrder(t *testing.T) (*gorm.DB, *redis.Client) {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.AutoMigrate(
		&models.User{}, &models.UserSetting{}, &models.Account{}, &models.AccountExtra{},
		&models.Status{}, &models.StatusContent{}, &models.StatusToAccount{}, &models.Follow{},
		&models.Block{}, &models.DomainBlock{}, &models.MuteAccount{}, &models.Application{},
		&models.Attachment{}, &models.Emoji{}, &models.StatusEmoji{}, &models.Poll{}, &models.PollOption{},
		&models.Tag{}, &models.StatusTag{}, &models.List{}, &models.ListAccount{}, &models.ListHashtag{},
	))
	previousDB := database.GetDB()
	database.SetTestDB(conn)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousRedises := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{client})
	var previousUniversal redis.UniversalClient
	if cRedis.HasUniversalRedis() {
		previousUniversal = cRedis.GetUniversalRedis()
	}
	cRedis.SetUniversalRedis(client)
	restoreFeed := feed.SwapDefaultService(feed.NewFeedService(feed.NewRedisClient(client)))
	t.Cleanup(func() {
		restoreFeed()
		cRedis.SetUniversalRedis(previousUniversal)
		cRedis.SetRedisesForTest(previousRedises)
		database.SetTestDB(previousDB)
		_ = client.Close()
		_ = sqlDB.Close()
	})
	return conn, client
}

func insertStatus(t *testing.T, conn *gorm.DB, status *models.Status, at time.Time) {
	t.Helper()
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(status).Error)
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", status.Id).UpdateColumn("created_at", at).Error)
}

func seedTimeline(t *testing.T, client *redis.Client, key string, members map[string]float64, rank string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, client.Del(ctx, key, key+":meta", key+":versions", key+":reblogs").Err())
	if len(members) > 0 {
		rows := make([]redis.Z, 0, len(members))
		for member, score := range members {
			rows = append(rows, redis.Z{Score: score, Member: member})
		}
		require.NoError(t, client.ZAdd(ctx, key, rows...).Err())
	}
	meta := map[string]any{
		"ready": "1", "epoch": "seed", "generation": "1", "count": strconv.Itoa(len(members)),
	}
	if rank != "" {
		meta["rank"] = rank
	}
	require.NoError(t, client.HSet(ctx, key+":meta", meta).Err())
	require.NoError(t, client.HSet(ctx, key+":versions", "_epoch", "seed").Err())
}

func pageIDs(t *testing.T, limit int, read func(*feed.ListParam) (models.Statuses, error)) []uint64 {
	t.Helper()
	var all []uint64
	var maxID uint64
	seen := map[uint64]bool{}
	for range 10 {
		params := &feed.ListParam{Limit: uint64(limit)}
		if maxID != 0 {
			params.MaxId = maxID
		}
		page, err := read(params)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		for _, status := range page {
			require.Falsef(t, seen[status.Id], "status %d was returned twice", status.Id)
			seen[status.Id] = true
			all = append(all, status.Id)
		}
		if len(page) < limit {
			break
		}
		maxID = page[len(page)-1].Id
	}
	return all
}
