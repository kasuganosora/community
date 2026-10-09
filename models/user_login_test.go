package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFindUserForLogin(t *testing.T) {
	db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&User{}))

	qa := User{UUID: "u1", UserName: "qa", EmailHash: GetEmailHash("qa@c.hime.at")}
	mixed := User{UUID: "u2", UserName: "Alice", EmailHash: GetEmailHash("Alice@Example.com")}
	require.NoError(t, db.Create(&qa).Error)
	require.NoError(t, db.Create(&mixed).Error)

	for _, tc := range []struct {
		in   string
		want uint64
	}{
		{"qa", qa.Id},
		{"QA", qa.Id},
		{"qa@c.hime.at", qa.Id},
		{" QA@C.HIME.AT ", qa.Id},
		{"Alice", mixed.Id},
		{"alice", mixed.Id},
		{"Alice@Example.com", mixed.Id},
	} {
		u, err := FindUserForLogin(db, tc.in)
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, u.Id, tc.in)
	}

	for _, in := range []string{"", "nobody", "nobody@c.hime.at"} {
		_, err := FindUserForLogin(db, in)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound, in)
	}
}
