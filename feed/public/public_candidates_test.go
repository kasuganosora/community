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
		"CREATE TABLE statuses (id INTEGER PRIMARY KEY, account_id INTEGER, visibility INTEGER, scheduled_at DATETIME, deleted_at DATETIME, domain TEXT, properties TEXT)",
		"CREATE TABLE media_attachments (id INTEGER PRIMARY KEY, status_id INTEGER)",
		"INSERT INTO accounts VALUES (1, false), (2, true)",
		"INSERT INTO statuses VALUES (1,1,0,NULL,NULL,NULL,NULL), (2,1,0,NULL,NULL,NULL,'{}'), (3,1,0,NULL,NULL,'remote.test','{\"other\":\"1\"}'), (4,2,0,NULL,NULL,NULL,NULL), (5,1,0,'2030-01-01',NULL,NULL,NULL), (6,1,0,NULL,'2020-01-01',NULL,NULL), (7,1,3,NULL,NULL,NULL,NULL), (8,1,0,NULL,NULL,'remote.test','{\"backfilled\":\"1\"}')",
		"INSERT INTO media_attachments VALUES (2,2),(3,3),(4,4),(5,5),(6,6),(7,7),(8,2),(9,3),(10,8)",
	} {
		require.NoError(t, conn.Exec(sql).Error)
	}
	// Publication time is absent, so rank falls back to the serial id.
	require.NoError(t, conn.Exec("ALTER TABLE statuses ADD COLUMN created_at DATETIME").Error)
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
		ids, err := client.ZRevRange(context.Background(), feed.TimelineKey(&tc.timeline), 0, -1).Result()
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

// A post imported late (id 8 here, but "published" long ago) is skipped by the
// database path and the Redis rebuild alike, and the posts around it paginate
// without gaps or duplicates.
func TestBackfilledStatusesStayOutOfPublicTimeline(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.Exec("CREATE TABLE accounts (id INTEGER PRIMARY KEY, is_ban BOOLEAN)").Error)
	require.NoError(t, conn.Exec("CREATE TABLE statuses (id INTEGER PRIMARY KEY, account_id INTEGER, visibility INTEGER, scheduled_at DATETIME, deleted_at DATETIME, domain TEXT, properties TEXT)").Error)
	require.NoError(t, conn.Exec("INSERT INTO accounts VALUES (1, false)").Error)
	for id := 1; id <= 12; id++ {
		props := "NULL"
		if id%4 == 0 {
			props = `'{"backfilled":"1"}'`
		}
		require.NoError(t, conn.Exec("INSERT INTO statuses VALUES (?,1,0,NULL,NULL,'remote.test',"+props+")", id).Error)
	}
	timeline := PublicTimeline{}
	var seen []uint64
	maxID := uint64(1 << 40)
	for {
		var page models.Statuses
		require.NoError(t, timeline.candidates(conn.Model(&models.Status{})).Where("statuses.id < ?", maxID).Order("statuses.id DESC").Limit(3).Find(&page).Error)
		if len(page) == 0 {
			break
		}
		for _, status := range page {
			seen = append(seen, status.Id)
		}
		maxID = page[len(page)-1].Id
	}
	require.Equal(t, []uint64{11, 10, 9, 7, 6, 5, 3, 2, 1}, seen)
}

func TestSkipsTimelinesForBackfilled(t *testing.T) {
	status := &models.Status{}
	require.False(t, feed.SkipsTimelines(status))
	status.MarkBackfilled()
	require.True(t, feed.SkipsTimelines(status))
}
