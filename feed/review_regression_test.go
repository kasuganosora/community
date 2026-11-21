package feed_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/feed/tag"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectionRemovalOfAbsentStatusPreservesReadyCache(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	service := feed.NewFeedService(feed.NewRedisClient(client))
	timeline := &feed.TestMockTimeline{KeyFormat: "feed:public:%s", TimelineId: "remote"}
	ctx := context.Background()

	lease, err := service.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	require.NoError(t, service.PublishRebuild(ctx, timeline, lease, models.Statuses{{Id: 100}}, true))
	key := feed.TimelineKey(timeline)
	require.Equal(t, "1", client.HGet(ctx, key+":meta", "ready").Val())

	// Removing a status that was never present in this variant must keep ready=1.
	err = service.ApplyProjection(ctx, timeline, feed.Projection{
		Epoch:           lease.Epoch,
		BatchID:         1,
		LeaseGeneration: 1,
		LeaseToken:      "tok",
		RemainingLease:  5 * time.Second,
		StatusID:        999,
		ObjectVersion:   1,
		Remove:          true,
	})
	require.NoError(t, err)
	require.Equal(t, "1", client.HGet(ctx, key+":meta", "ready").Val())

	// Removing an item actually present in the ZSet marks ready=0 for SQL re-evaluation.
	err = service.ApplyProjection(ctx, timeline, feed.Projection{
		Epoch:           lease.Epoch,
		BatchID:         2,
		LeaseGeneration: 1,
		LeaseToken:      "tok2",
		RemainingLease:  5 * time.Second,
		StatusID:        100,
		ObjectVersion:   1,
		Remove:          true,
	})
	require.NoError(t, err)
	require.Equal(t, "0", client.HGet(ctx, key+":meta", "ready").Val())
}

func TestForwardMinIdReturnsImmediatelyNewerPageAndEscapesWindow(t *testing.T) {
	conn, client := setupTimelineOrder(t)
	author := &models.Account{Id: 1, Username: "author"}
	blockedAuthor := &models.Account{Id: 2, Username: "blocked"}
	viewer := &models.Account{Id: 3, Username: "viewer", FeedLastSeenAt: ptrTime(time.Now().UTC())}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Account{author, blockedAuthor, viewer}).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Block{
		AccountId:       viewer.Id,
		Type:            models.BlockTypeAccount,
		TargetAccountId: blockedAuthor.Id,
	}).Error)

	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	insertStatus(t, conn, &models.Status{Id: 10, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}, base)
	insertStatus(t, conn, &models.Status{Id: 20, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}, base.Add(time.Minute))
	insertStatus(t, conn, &models.Status{Id: 30, AccountId: blockedAuthor.Id, Visibility: models.StatusVisibilityPublic}, base.Add(2*time.Minute))
	insertStatus(t, conn, &models.Status{Id: 40, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}, base.Add(3*time.Minute))
	insertStatus(t, conn, &models.Status{Id: 50, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}, base.Add(4*time.Minute))

	// Warm the public timeline cache.
	initial, err := public.GetTimelineContentAll(&feed.ListParam{Viewer: viewer, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []uint64{50, 40, 20, 10}, initial.GetIds())

	// Forward pagination above 10 with Limit=2 must return immediately-newer visible items [40, 20], not [50, 40].
	forward, err := public.GetTimelineContentAll(&feed.ListParam{Viewer: viewer, MinId: 10, Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []uint64{40, 20}, forward.GetIds())

	// Simulate MinId older than the cached Redis window (evict 10 and 20 from ZSet while keeping ready=1).
	pubKey := feed.TimelineKey(&public.PublicTimeline{})
	require.NoError(t, client.ZRem(context.Background(), pubKey, "10", "20").Err())
	escaped, err := public.GetTimelineContentAll(&feed.ListParam{Viewer: viewer, MinId: 10, Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []uint64{40, 20}, escaped.GetIds(), "min_id older than Redis window must fall back to SQL instead of skipping post 20")
}

func TestExactIDLookupAuthorizesLateFederatedPost(t *testing.T) {
	conn, _ := setupTimelineOrder(t)
	domain := "remote.example"
	localAuthor := &models.Account{Id: 1, Username: "local"}
	remoteAuthor := &models.Account{Id: 2, Username: "remote", Domain: &domain}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create([]*models.Account{localAuthor, remoteAuthor}).Error)

	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	insertStatus(t, conn, &models.Status{Id: 100, AccountId: localAuthor.Id, Visibility: models.StatusVisibilityPublic}, now)
	// Late-ingested federated status gets higher ID (101) but older publication timestamp than ID 100.
	insertStatus(t, conn, &models.Status{Id: 101, AccountId: remoteAuthor.Id, Domain: &domain, Visibility: models.StatusVisibilityPublic}, now.Add(-5*time.Minute))

	got, err := public.GetTimelineContentAll(&feed.ListParam{
		Limit: 1,
		Other: map[string]any{"database_only": true, "exact_id": uint64(101)},
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{101}, got.GetIds())
}

func TestEmptyAndShortTimelinesRespectCompleteSentinelWithoutSQLFallback(t *testing.T) {
	conn, client := setupTimelineOrder(t)
	ctx := context.Background()
	pubKey := feed.TimelineKey(&public.PublicTimeline{})

	// 1. Cold read on an empty timeline populates ready=1, count=0, complete=1.
	emptyRes, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 20})
	require.NoError(t, err)
	require.Empty(t, emptyRes)
	require.Equal(t, "1", client.HGet(ctx, pubKey+":meta", "ready").Val())
	require.Equal(t, "1", client.HGet(ctx, pubKey+":meta", "complete").Val())

	// Seed a row directly in SQL without touching Redis: warm empty cache must NOT penetrate to SQL.
	author := &models.Account{Id: 1, Username: "author"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(author).Error)
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	insertStatus(t, conn, &models.Status{Id: 10, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}, base)

	warmEmpty, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 20})
	require.NoError(t, err)
	require.Empty(t, warmEmpty, "ready empty timeline with complete=1 must serve from Redis without SQL penetration")

	// 2. Rebuild short timeline (3 items < Limit=20) and verify complete=1 prevents exhaustion SQL fallback.
	insertStatus(t, conn, &models.Status{Id: 20, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}, base.Add(time.Minute))
	insertStatus(t, conn, &models.Status{Id: 30, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}, base.Add(2*time.Minute))
	require.NoError(t, client.Del(ctx, pubKey, pubKey+":meta").Err())

	shortWarm, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 20})
	require.NoError(t, err)
	require.Equal(t, []uint64{30, 20, 10}, shortWarm.GetIds())
	require.Equal(t, "1", client.HGet(ctx, pubKey+":meta", "complete").Val())

	// Seed older status 5 directly in SQL without invalidating Redis: warm short timeline must NOT fall back to full SQL scan.
	insertStatus(t, conn, &models.Status{Id: 5, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}, base.Add(-time.Minute))
	shortCached, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 20})
	require.NoError(t, err)
	require.Equal(t, []uint64{30, 20, 10}, shortCached.GetIds())
}

func TestDurableFilteredPublicAndTagTimelinesPopulateRedisCaches(t *testing.T) {
	conn, client := setupTimelineOrder(t)
	require.NoError(t, conn.AutoMigrate(&models.Tag{}, &models.StatusTag{}))
	require.NoError(t, feed.ConfigureRuntime("durable"))

	author := &models.Account{Id: 1, Username: "local"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(author).Error)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	insertStatus(t, conn, &models.Status{Id: 200, AccountId: author.Id, Visibility: models.StatusVisibilityPublic}, now)
	tagRow := &models.Tag{Id: 1, Name: "golang", NameKey: "golang"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(tagRow).Error)
	require.NoError(t, conn.Exec("INSERT INTO status_tags (status_id, tag_id) VALUES (?, ?)", 200, tagRow.Id).Error)
	statusID := uint64(200)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.MediaAttachment{
		Id:       1,
		StatusId: &statusID,
		FileName: "pic.png",
	}).Error)

	localStatuses, err := public.GetTimelineContentAll(&feed.ListParam{Limit: 10, Other: map[string]any{"local": true}})
	require.NoError(t, err)
	require.Equal(t, []uint64{200}, localStatuses.GetIds())
	localKey := feed.TimelineKey(&public.PublicTimeline{OnlyLocal: true})
	require.Equal(t, "1", client.HGet(context.Background(), localKey+":meta", "ready").Val())

	tagStatuses, err := tag.GetTimelineContent("golang", nil, &feed.ListParam{Limit: 10, Other: map[string]any{"only_media": true}})
	require.NoError(t, err)
	require.Equal(t, []uint64{200}, tagStatuses.GetIds())
	tagKey := feed.TimelineKey(&tag.TagTimeline{Tag: "golang", OnlyMedia: true})
	require.Equal(t, "1", client.HGet(context.Background(), tagKey+":meta", "ready").Val())
}

func ptrTime(v time.Time) *time.Time { return &v }
