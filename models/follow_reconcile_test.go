package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReconcileFollowCounts(t *testing.T) {
	db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&Account{}, &AccountExtra{}, &Follow{}))

	raw := db.Session(&gorm.Session{SkipHooks: true})
	remote := "remote.example"
	require.NoError(t, raw.Create(&[]Account{
		{Id: 1, Username: "a"}, {Id: 2, Username: "b"}, {Id: 3, Username: "c"},
		{Id: 4, Username: "r", Domain: &remote},
	}).Error)
	require.NoError(t, raw.Create(&[]AccountExtra{
		{AccountId: 1, FollowersCount: 9, FollowingCount: 9},
		{AccountId: 2, FollowersCount: 9, FollowingCount: 9},
		{AccountId: 3, FollowersCount: 9, FollowingCount: 9},
		{AccountId: 4, FollowersCount: 500, FollowingCount: 40},
	}).Error)
	require.NoError(t, raw.Create(&[]Follow{
		{AccountId: 1, FollowId: 2, Status: FollowStatusFollowed},
		{AccountId: 3, FollowId: 2, Status: FollowStatusFollowed},
		{AccountId: 2, FollowId: 1, Status: FollowStatusFollowed + 1},
		{AccountId: 1, FollowId: 4, Status: FollowStatusFollowed},
	}).Error)

	require.NoError(t, ReconcileFollowCounts(db))

	var rows []AccountExtra
	require.NoError(t, db.Order("account_id").Find(&rows).Error)
	require.Len(t, rows, 4)
	got := map[uint64][2]int{}
	for _, r := range rows {
		got[r.AccountId] = [2]int{r.FollowingCount, r.FollowersCount}
	}
	require.Equal(t, map[uint64][2]int{1: {2, 0}, 2: {0, 2}, 3: {1, 0}, 4: {40, 500}}, got)
}
