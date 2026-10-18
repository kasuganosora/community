package models

import (
	"context"
	"errors"
	"testing"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func counterTestDB(t *testing.T) *gorm.DB {
	conn, err := gorm.Open(dialect.OpenSQLite(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.AutoMigrate(&AccountExtra{}))
	require.NoError(t, conn.Create(&AccountExtra{AccountId: 7}).Error)
	return conn
}

func followers(t *testing.T, conn *gorm.DB) int64 {
	var e AccountExtra
	require.NoError(t, conn.Where("account_id = ?", 7).Take(&e).Error)
	return int64(e.FollowersCount)
}

func TestDeferredCountersApplyOnlyAfterCommit(t *testing.T) {
	conn := counterTestDB(t)
	ctx, apply := BeginDeferredCounters(context.Background())
	require.NoError(t, conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		require.NoError(t, BumpAccountCounter(tx, 7, "followers_count", 1))
		require.NoError(t, BumpAccountCounter(tx, 7, "followers_count", 1))
		require.EqualValues(t, 0, followers(t, tx), "no row lock taken inside the transaction")
		return nil
	}))
	apply(conn)
	require.EqualValues(t, 2, followers(t, conn))

	// A bump through the scoped context after apply is written directly.
	require.NoError(t, BumpAccountCounter(conn.WithContext(ctx), 7, "followers_count", 1))
	require.EqualValues(t, 3, followers(t, conn))
}

func TestDeferredCountersDroppedOnRollback(t *testing.T) {
	conn := counterTestDB(t)
	ctx, _ := BeginDeferredCounters(context.Background())
	err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		require.NoError(t, BumpAccountCounter(tx, 7, "followers_count", 5))
		return errors.New("handler failed")
	})
	require.Error(t, err)
	require.EqualValues(t, 0, followers(t, conn))
}

func TestCountersWithoutScopeAreImmediate(t *testing.T) {
	conn := counterTestDB(t)
	require.NoError(t, BumpAccountCounter(conn, 7, "followers_count", 4))
	require.EqualValues(t, 4, followers(t, conn))
}
