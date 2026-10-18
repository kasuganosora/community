package writebuffer

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type wbCounter struct {
	AccountID uint64 `gorm:"primaryKey"`
	Followers int64
	Statuses  int64
}

func (wbCounter) TableName() string { return "writebuffer_counters" }

func counterDB(t *testing.T) *gorm.DB {
	conn, err := gorm.Open(dialect.OpenSQLite(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.AutoMigrate(&wbCounter{}))
	require.NoError(t, conn.Create(&[]wbCounter{{AccountID: 1, Followers: 10}, {AccountID: 2}}).Error)
	return conn
}

func TestIncrementCoalescesDeltas(t *testing.T) {
	conn := counterDB(t)
	b := Start(New(func() *gorm.DB { return conn }, time.Hour))
	defer b.Stop()
	for i := 0; i < 50; i++ {
		require.NoError(t, Increment(conn, "writebuffer_counters", "account_id", 1, "followers", 1))
		require.NoError(t, Increment(conn, "writebuffer_counters", "account_id", 1, "statuses", 2))
	}
	require.NoError(t, Increment(conn, "writebuffer_counters", "account_id", 1, "followers", -5))
	require.NoError(t, Increment(conn, "writebuffer_counters", "account_id", 2, "statuses", 1))
	require.Equal(t, 2, b.Pending(), "one pending op per row")

	var row wbCounter
	require.NoError(t, conn.First(&row, "account_id = ?", 1).Error)
	require.EqualValues(t, 10, row.Followers, "nothing written before the flush")

	updates := int64(0)
	require.NoError(t, conn.Callback().Update().After("gorm:update").Register("test:count_counters", func(tx *gorm.DB) {
		if tx.Statement.Table == "writebuffer_counters" {
			updates++
		}
	}))
	require.NoError(t, b.Flush(t.Context()))
	require.EqualValues(t, 2, updates, "one UPDATE per row per flush")
	require.NoError(t, conn.First(&row, "account_id = ?", 1).Error)
	require.EqualValues(t, 55, row.Followers)
	require.EqualValues(t, 100, row.Statuses)
	var row2 wbCounter
	require.NoError(t, conn.First(&row2, "account_id = ?", 2).Error)
	require.EqualValues(t, 1, row2.Statuses)
}

func TestIncrementWithoutBufferIsImmediate(t *testing.T) {
	conn := counterDB(t)
	require.NoError(t, Increment(conn, "writebuffer_counters", "account_id", 2, "followers", 3))
	var row wbCounter
	require.NoError(t, conn.First(&row, "account_id = ?", 2).Error)
	require.EqualValues(t, 3, row.Followers)
}
