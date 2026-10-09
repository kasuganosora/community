package home

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Posts imported late from followed accounts stay out of the home timeline,
// while everything else paginates by id without gaps. Raw tables keep the
// status event handlers (which need the global DB) out of the way.
func TestHomeCandidatesSkipBackfilledStatuses(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, sql := range []string{
		"CREATE TABLE accounts (id INTEGER PRIMARY KEY, is_ban BOOLEAN)",
		"CREATE TABLE follows (id INTEGER PRIMARY KEY, account_id INTEGER, follow_id INTEGER, status INTEGER)",
		"CREATE TABLE status_to_accounts (id INTEGER PRIMARY KEY, status_id INTEGER, account_id INTEGER)",
		"CREATE TABLE statuses (id INTEGER PRIMARY KEY, account_id INTEGER, visibility INTEGER, scheduled_at DATETIME, deleted_at DATETIME, domain TEXT, properties TEXT)",
		"INSERT INTO accounts VALUES (1, false), (2, false)",
	} {
		require.NoError(t, conn.Exec(sql).Error)
	}
	require.NoError(t, conn.Exec("INSERT INTO follows (account_id, follow_id, status) VALUES (1, 2, ?)", models.FollowStatusFollowed).Error)
	var want []uint64
	for id := uint64(1); id <= 9; id++ {
		props := "NULL"
		if id%3 == 0 {
			props = `'{"backfilled":"1"}'`
		} else {
			want = append([]uint64{id}, want...)
		}
		require.NoError(t, conn.Exec("INSERT INTO statuses VALUES (?,2,0,NULL,NULL,'remote.example',"+props+")", id).Error)
	}
	// A backfilled post that mentions me is still delivered.
	require.NoError(t, conn.Exec("INSERT INTO status_to_accounts (status_id, account_id) VALUES (3, 1)").Error)
	want = []uint64{8, 7, 5, 4, 3, 2, 1}

	var got []uint64
	maxID := uint64(1 << 40)
	for {
		var page models.Statuses
		require.NoError(t, homeCandidates(conn, 1).Where("statuses.id < ?", maxID).Order("statuses.id DESC").Limit(2).Find(&page).Error)
		if len(page) == 0 {
			break
		}
		for _, s := range page {
			got = append(got, s.Id)
		}
		maxID = page[len(page)-1].Id
	}
	require.Equal(t, want, got)
}
