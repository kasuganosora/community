package writebuffer

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type wbRow struct {
	ID     uint64 `gorm:"primaryKey"`
	Status string
	Note   string
}

func (wbRow) TableName() string { return "writebuffer_rows" }

func countUpdates(conn *gorm.DB) *int64 {
	var n int64
	_ = conn.Callback().Update().After("gorm:update").Register("test:count", func(tx *gorm.DB) {
		if tx.Statement.Table == "writebuffer_rows" && tx.RowsAffected > 0 {
			atomic.AddInt64(&n, tx.RowsAffected)
		}
	})
	return &n
}

func exercise(t *testing.T, conn *gorm.DB) {
	t.Helper()
	require.NoError(t, conn.Migrator().DropTable(&wbRow{}))
	require.NoError(t, conn.AutoMigrate(&wbRow{}))
	require.NoError(t, conn.Create(&[]wbRow{{ID: 1, Status: "processing"}, {ID: 2, Status: "processing"}, {ID: 3, Status: "pending"}}).Error)
	writes := countUpdates(conn)

	b := New(func() *gorm.DB { return conn }, time.Hour)
	b.Add("writebuffer_rows", 1, map[string]any{"status": "pending", "note": "retry"}, "status = ?", "processing")
	b.Add("writebuffer_rows", 1, map[string]any{"status": "delivered"}, "status = ?", "processing") // coalesced, last wins
	b.Add("writebuffer_rows", 2, map[string]any{"status": "processing"}, "")                        // unchanged: no write
	b.Add("writebuffer_rows", 3, map[string]any{"status": "delivered"}, "status = ?", "processing") // guard fails: untouched
	require.Equal(t, 3, b.Pending())
	require.NoError(t, b.Flush(context.Background()))
	require.Zero(t, b.Pending())

	var rows []wbRow
	require.NoError(t, conn.Order("id").Find(&rows).Error)
	require.Equal(t, "delivered", rows[0].Status)
	require.Equal(t, "retry", rows[0].Note)
	require.Equal(t, "processing", rows[1].Status)
	require.Equal(t, "pending", rows[2].Status)
	require.EqualValues(t, 1, atomic.LoadInt64(writes), "one real row write for four queued updates")
}

func TestBufferCoalescesAndSkipsSQLite(t *testing.T) {
	conn, err := gorm.Open(dialect.OpenSQLite(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	exercise(t, conn)
}

func TestBufferCoalescesAndSkipsPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	conn, err := gorm.Open(dialect.OpenPostgres(dsn), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Migrator().DropTable(&wbRow{}) })
	exercise(t, conn) // also proves SET LOCAL synchronous_commit runs inside the batch
}

func TestUpdateWritesImmediatelyWithoutRunningBuffer(t *testing.T) {
	conn, err := gorm.Open(dialect.OpenSQLite(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&wbRow{}))
	require.NoError(t, conn.Create(&wbRow{ID: 1, Status: "processing"}).Error)
	require.NoError(t, Update(conn, "writebuffer_rows", 1, map[string]any{"status": "failed"}, ""))
	var row wbRow
	require.NoError(t, conn.First(&row, 1).Error)
	require.Equal(t, "failed", row.Status)
}

func TestRunningBufferFlushesOnTickAndStop(t *testing.T) {
	conn, err := gorm.Open(dialect.OpenSQLite(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&wbRow{}))
	require.NoError(t, conn.Create(&[]wbRow{{ID: 1, Status: "processing"}, {ID: 2, Status: "processing"}}).Error)
	b := Start(New(func() *gorm.DB { return conn }, 20*time.Millisecond))
	require.NoError(t, Update(conn, "writebuffer_rows", 1, map[string]any{"status": "delivered"}, ""))
	require.Eventually(t, func() bool {
		var row wbRow
		return conn.First(&row, 1).Error == nil && row.Status == "delivered"
	}, 2*time.Second, 10*time.Millisecond)
	b.Add("writebuffer_rows", 2, map[string]any{"status": "delivered"}, "")
	b.Stop()
	var row wbRow
	require.NoError(t, conn.First(&row, 2).Error)
	require.Equal(t, "delivered", row.Status, "Stop flushes what is pending")
}

func TestFailedFlushKeepsBatchWithoutOverwritingNewer(t *testing.T) {
	conn, err := gorm.Open(dialect.OpenSQLite(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	b := New(func() *gorm.DB { return conn }, time.Hour)
	b.Add("writebuffer_rows", 1, map[string]any{"status": "old", "note": "n"}, "")
	require.Error(t, b.Flush(context.Background()), "table does not exist yet")
	b.Add("writebuffer_rows", 1, map[string]any{"status": "new"}, "")
	require.NoError(t, conn.AutoMigrate(&wbRow{}))
	require.NoError(t, conn.Create(&wbRow{ID: 1}).Error)
	require.NoError(t, b.Flush(context.Background()))
	var row wbRow
	require.NoError(t, conn.First(&row, 1).Error)
	require.Equal(t, "new", row.Status)
	require.Equal(t, "n", row.Note)
}
