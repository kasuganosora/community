package commands

import (
	"context"
	"fmt"
	"github.com/urfave/cli/v2"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBuildFeedsTest(t *testing.T) (*gorm.DB, *redis.Client) {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.AccountExtra{}, &models.User{}, &models.UserSetting{}, &models.UserSecretData{}, &models.Status{}, &models.StatusToAccount{}, &models.Follow{}, &models.List{}, &models.ListAccount{}))
	database.SetTestDB(conn)
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	old := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{client})
	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(client)))
	t.Cleanup(func() { cRedis.SetRedisesForTest(old); database.ResetTestDB(); _ = client.Close(); _ = sqlDB.Close() })
	return conn.Session(&gorm.Session{SkipHooks: true}), client
}

func TestBuildAccountFeedsUsesCanonicalIDsAndMembership(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		t.Run(fmt.Sprint(aggregate), func(t *testing.T) {
			conn, client := setupBuildFeedsTest(t)
			accounts := []*models.Account{{Id: 1, Username: "viewer"}, {Id: 2, Username: "followed"}, {Id: 3, Username: "pending"}, {Id: 4, Username: "otherauthor"}, {Id: 5, Username: "otherowner"}}
			require.NoError(t, conn.Create(&accounts).Error)
			accounts[0].User = &models.User{Settings: &models.UserSetting{Data: map[string]any{"aggregates_reblogs": aggregate}}}
			require.NoError(t, conn.Create(&[]models.Follow{{AccountId: 1, FollowId: 2, Status: models.FollowStatusFollowed}, {AccountId: 1, FollowId: 3, Status: models.FollowStatusRequested}}).Error)
			original := uint64(10)
			statuses := models.Statuses{
				{Id: 10, AccountId: 2, Visibility: models.StatusVisibilityPublic},
				{Id: 20, AccountId: 2, Visibility: models.StatusVisibilityPrivate},
				{Id: 30, AccountId: 2, Visibility: models.StatusVisibilityDirect},
				{Id: 40, AccountId: 2, Visibility: models.StatusVisibilityDirect},
				{Id: 50, AccountId: 3, Visibility: models.StatusVisibilityPrivate},
				{Id: 60, AccountId: 3, Visibility: models.StatusVisibilityPublic},
				{Id: 70, AccountId: 4, Visibility: models.StatusVisibilityPublic},
				{Id: 80, AccountId: 1, Visibility: models.StatusVisibilityPublic},
				{Id: 90, AccountId: 2, Visibility: models.StatusVisibilityPublic, ReblogId: &original},
			}
			require.NoError(t, conn.Create(&statuses).Error)
			require.NoError(t, conn.Create(&[]models.StatusToAccount{{StatusId: 30, AccountId: 1}, {StatusId: 40, AccountId: 5}}).Error)
			lists := []*models.List{{Id: 21, AccountId: 1, Title: "same"}, {Id: 22, AccountId: 5, Title: "same"}}
			require.NoError(t, conn.Create(&lists).Error)
			require.NoError(t, conn.Create(&[]models.ListAccount{{ListId: 21, AccountId: 2}, {ListId: 22, AccountId: 4}}).Error)
			ctx := context.Background()
			ht := &home.HomeTimeline{Account: accounts[0]}
			key := feed.TimelineKey(ht)
			require.NoError(t, client.ZAdd(ctx, key, redis.Z{Score: 1700000000, Member: "10"}, redis.Z{Score: 1700000001, Member: "50"}, redis.Z{Score: 123, Member: "malformed"}).Err())
			for _, entry := range lists {
				lt := &list.ListTimeline{List: entry}
				lk := feed.TimelineKey(lt)
				require.NoError(t, client.ZAdd(ctx, lk, redis.Z{Score: 1700000000, Member: "70"}, redis.Z{Score: 123, Member: "malformed"}).Err())
			}
			count, err := buildAccountFeeds(accounts[0])
			require.NoError(t, err)
			expected := []string{"90", "80", "30", "20", "10"}
			if aggregate {
				expected = expected[:4]
			}
			require.Equal(t, len(expected), count)
			members, err := client.ZRevRange(ctx, key, 0, -1).Result()
			require.NoError(t, err)
			require.Equal(t, expected, members)
			for _, id := range []uint64{90, 80, 30, 20} {
				score, err := client.ZScore(ctx, key, fmt.Sprint(id)).Result()
				require.NoError(t, err)
				require.Equal(t, float64(id), score)
			}
			_, err = buildAccountFeeds(accounts[4])
			require.NoError(t, err)
			for i, want := range [][]string{{"90"}, {"70"}} {
				lt := &list.ListTimeline{List: lists[i]}
				listKey := feed.TimelineKey(lt)
				got, err := client.ZRevRange(ctx, listKey, 0, -1).Result()
				require.NoError(t, err)
				require.Equal(t, want, got)
				for _, member := range got {
					score, err := client.ZScore(ctx, listKey, member).Result()
					require.NoError(t, err)
					require.Equal(t, member, fmt.Sprintf("%.0f", score))
				}
			}
		})
	}
}

func TestBuildAccountFeedsPropagatesFailures(t *testing.T) {
	t.Run("list-query", func(t *testing.T) {
		conn, _ := setupBuildFeedsTest(t)
		require.NoError(t, conn.Migrator().DropTable(&models.List{}))
		_, err := buildAccountFeeds(&models.Account{Id: 1})
		require.ErrorContains(t, err, "failed to query account lists")
	})
	t.Run("redis", func(t *testing.T) {
		_, client := setupBuildFeedsTest(t)
		require.NoError(t, client.Close())
		_, err := buildAccountFeeds(&models.Account{Id: 1})
		require.ErrorContains(t, err, "failed to rebuild home timeline")
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := buildAccountFeedsContext(ctx, &models.Account{Id: 1})
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestClearV3FeedsInvalidatesWithoutLosingTombstone(t *testing.T) {
	_, client := setupBuildFeedsTest(t)
	ctx := context.Background()
	timeline := &home.HomeTimeline{Account: &models.Account{Id: 91}}
	lease, err := feed.BeginRebuild(ctx, timeline)
	require.NoError(t, err)
	require.NoError(t, feed.PublishRebuild(ctx, timeline, lease, models.Statuses{{Id: 10}}, false))
	require.NoError(t, feed.ApplyProjection(ctx, timeline, feed.Projection{Epoch: lease.Epoch, BatchID: 1, LeaseGeneration: 1, LeaseToken: "delete", RemainingLease: time.Second, StatusID: 9, ObjectVersion: 2, Remove: true}))
	require.NoError(t, client.Set(ctx, "unrelated:fixture", "keep", 0).Err())
	commandContext := cli.NewContext(cli.NewApp(), nil, nil)
	commandContext.Context = ctx
	require.NoError(t, clearFeeds(commandContext))
	key := feed.TimelineKey(timeline)
	require.Zero(t, client.ZCard(ctx, key).Val())
	require.Equal(t, "0", client.HGet(ctx, key+":meta", "ready").Val())
	require.Equal(t, "2:1", client.HGet(ctx, key+":versions", "9").Val())
	require.Equal(t, "keep", client.Get(ctx, "unrelated:fixture").Val())
	stats, err := GetFeedStats()
	require.NoError(t, err)
	require.Equal(t, 1, stats["home_feeds"])
}

func TestFeedStatsRealCluster(t *testing.T) {
	addresses := os.Getenv("FEED_CLUSTER_TEST_ADDRS")
	if addresses == "" {
		t.Skip("requires the isolated real Redis Cluster")
	}
	client := redis.NewClusterClient(&redis.ClusterOptions{Addrs: strings.Split(addresses, ","), ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, MaxRetries: 1})
	defer client.Close()
	var prior redis.UniversalClient
	if cRedis.HasUniversalRedis() {
		prior = cRedis.GetUniversalRedis()
	}
	cRedis.SetUniversalRedis(client)
	defer cRedis.SetUniversalRedis(prior)
	before, err := GetFeedStats()
	require.NoError(t, err)
	service := feed.NewFeedService(feed.NewRedisClient(client))
	ctx := context.Background()
	id := uint64(time.Now().UnixNano())
	for _, timeline := range []feed.Timeline{&home.HomeTimeline{Account: &models.Account{Id: id}}, &list.ListTimeline{List: &models.List{Id: id}}} {
		lease, err := service.BeginRebuild(ctx, timeline)
		require.NoError(t, err)
		require.NoError(t, service.PublishRebuild(ctx, timeline, lease, models.Statuses{{Id: 1}}, false))
	}
	after, err := GetFeedStats()
	require.NoError(t, err)
	require.Equal(t, before["home_feeds"].(int)+1, after["home_feeds"])
	require.Equal(t, before["list_feeds"].(int)+1, after["list_feeds"])
	t.Logf("actual CLI stats scanned Cluster primaries: home=%v list=%v", after["home_feeds"], after["list_feeds"])
}
