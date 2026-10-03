package public

import (
	"context"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFilteredRebuildExcludesIneligibleCandidatesBeforeLimit(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sqlDB.Close() })
	// This test exercises the candidate SQL and the actual rebuilt Redis key;
	// unrelated associations are deliberately absent from its small schema.
	for _, sql := range []string{
		"CREATE TABLE accounts (id INTEGER PRIMARY KEY, is_ban BOOLEAN)",
		"CREATE TABLE statuses (id INTEGER PRIMARY KEY, account_id INTEGER, visibility INTEGER, scheduled_at DATETIME, deleted_at DATETIME, domain TEXT)",
		"CREATE TABLE media_attachments (id INTEGER PRIMARY KEY, status_id INTEGER)",
		"INSERT INTO accounts VALUES (1, false), (2, true)",
		"INSERT INTO statuses VALUES (1,1,0,NULL,NULL,NULL), (2,1,0,NULL,NULL,NULL), (3,1,0,NULL,NULL,'remote.test'), (4,2,0,NULL,NULL,NULL), (5,1,0,'2030-01-01',NULL,NULL), (6,1,0,NULL,'2020-01-01',NULL), (7,1,3,NULL,NULL,NULL)",
		"INSERT INTO media_attachments VALUES (2,2),(3,3),(4,4),(5,5),(6,6),(7,7),(8,2),(9,3)",
	} {
		require.NoError(t, conn.Exec(sql).Error)
	}
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close(); feed.SetDefaultService(feed.NewFeedService(feed.NewMockRedisClient())) })
	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(client)))
	for _, tc := range []struct {
		timeline PublicTimeline
		want     []string
	}{
		{PublicTimeline{}, []string{"3", "2", "1"}},
		{PublicTimeline{OnlyMedia: true}, []string{"3", "2"}},
		{PublicTimeline{OnlyMedia: true, OnlyLocal: true}, []string{"2"}},
		{PublicTimeline{OnlyMedia: true, OnlyRemote: true}, []string{"3"}},
	} {
		require.NoError(t, tc.timeline.RebuildContext(context.Background()))
		ids, err := client.ZRevRange(context.Background(), "feed:public:"+tc.timeline.GetTimelineId(), 0, -1).Result()
		require.NoError(t, err)
		require.Equal(t, tc.want, ids)
		var candidates models.Statuses
		require.NoError(t, tc.timeline.candidates(conn.Model(&models.Status{})).Order("statuses.id DESC").Limit(1).Find(&candidates).Error)
		require.Len(t, candidates, 1)
		require.Equal(t, tc.want[0], strconv.FormatUint(candidates[0].Id, 10))
		var deeper models.Statuses
		require.NoError(t, tc.timeline.candidates(conn.Model(&models.Status{})).Where("statuses.id < ?", candidates[0].Id).Order("statuses.id DESC").Find(&deeper).Error)
		actual := []string{}
		for _, status := range deeper {
			actual = append(actual, strconv.FormatUint(status.Id, 10))
		}
		require.Equal(t, tc.want[1:], actual, "multiple attachments do not duplicate paginated statuses")
	}
}
