package search

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSearchHashtagsIsCaseInsensitive(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, gdb.AutoMigrate(&models.Tag{}))
	prev := database.GetDB()
	database.SetTestDB(gdb)
	t.Cleanup(func() {
		if prev != nil {
			database.SetTestDB(prev)
		} else {
			database.ResetTestDB()
		}
		_ = sqlDB.Close()
	})

	for _, name := range []string{"OSINT", "osint_tools", "Ukraine"} {
		require.NoError(t, gdb.Create(&models.Tag{Name: name}).Error)
	}
	for _, q := range []string{"osint", "#OSInt", "OSINT"} {
		got := searchHashtags(SearchRequest{Q: q, Limit: 10})
		names := make([]string, 0, len(got))
		for _, tag := range got {
			names = append(names, tag.Name)
		}
		assert.ElementsMatch(t, []string{"OSINT", "osint_tools"}, names, q)
	}
	got := searchHashtags(SearchRequest{Q: "UKR", Limit: 10})
	require.Len(t, got, 1)
	assert.Equal(t, "Ukraine", got[0].Name)
}

func TestSearchTagHistoryCountsOnlyPublicUsageOnUTCDay(t *testing.T) {
	conn := sqlTestDB(t)
	if pgtest.Enabled() {
		sqlDB, err := conn.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		require.NoError(t, conn.Exec("SET TIME ZONE 'Asia/Tokyo'").Error)
	}
	require.NoError(t, conn.Create(&models.Account{Username: "writer", Id: 1}).Error)
	tag := &models.Tag{Name: "clock", StatusCount: 999, AccountCount: 99}
	require.NoError(t, conn.Create(tag).Error)
	midnight := time.Now().UTC().Truncate(24 * time.Hour)
	japan := time.FixedZone("JST", 9*60*60)
	createdAt := midnight.Add(-time.Hour).In(japan)
	for i, visibility := range []uint8{models.StatusVisibilityPublic, models.StatusVisibilityPublic, models.StatusVisibilityDirect} {
		id := uint64(i + 1)
		require.NoError(t, conn.Exec("INSERT INTO statuses (id, account_id, visibility, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", id, 1, visibility, createdAt, createdAt).Error)
		require.NoError(t, conn.Create(&models.StatusTag{StatusId: id, TagId: tag.Id}).Error)
	}
	result := runSearch(t, "q=%23clock&type=hashtags", nil)
	require.Len(t, result.Hashtags, 1)
	entity := result.Hashtags[0]
	require.Len(t, entity.History, 7)
	require.Equal(t, serializer.TagHistory{Day: strconv.FormatInt(midnight.AddDate(0, 0, -1).Unix(), 10), Uses: "2", Accounts: "1"}, entity.History[1])
	require.Equal(t, "0", entity.History[0].Uses)

	// The wire entity must not expose the models.Tag lifetime counters,
	// which include non-public posts regardless of the viewer.
	wire, err := json.Marshal(entity)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(wire, &fields))
	require.NotContains(t, fields, "status_count")
	require.NotContains(t, fields, "account_count")
	require.NotContains(t, fields, "following")
	require.NotContains(t, fields, "featuring")
}

func TestSearchTagLimitAlwaysBoundsThePage(t *testing.T) {
	conn := sqlTestDB(t)
	for _, name := range []string{"clockone", "clocktwo", "clockthree"} {
		require.NoError(t, conn.Create(&models.Tag{Name: name}).Error)
	}
	require.Len(t, runSearch(t, "q=%23clock&type=hashtags&limit=-1", nil).Hashtags, 1)
	require.Empty(t, runSearch(t, "q=%23clock&type=hashtags&limit=0", nil).Hashtags)
}
