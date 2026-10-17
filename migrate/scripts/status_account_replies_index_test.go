package scripts

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func checkStatusAccountRepliesIndex(t *testing.T, conn *gorm.DB) {
	t.Helper()
	require.NoError(t, conn.Exec(`DROP TABLE IF EXISTS statuses`).Error)
	require.NoError(t, conn.Exec(`CREATE TABLE statuses (id bigint primary key, account_id bigint, in_reply_to_id bigint)`).Error)
	for range 2 { // idempotent
		require.NoError(t, StatusAccountRepliesIndexScript{}.Up(conn))
	}
	require.True(t, conn.Migrator().HasIndex(&statusAccountRepliesRow{}, statusAccountRepliesIndex))
	require.NoError(t, conn.Exec(`DROP TABLE statuses`).Error)
}

func TestStatusAccountRepliesIndexSQLite(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	checkStatusAccountRepliesIndex(t, conn)
	require.NoError(t, StatusAccountRepliesIndexScript{}.Up(conn), "no statuses table: nothing to do")
}

func TestStatusAccountRepliesIndexPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	conn, err := gorm.Open(dialect.OpenPostgres(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.Exec(`CREATE SCHEMA IF NOT EXISTS idx_test`).Error)
	require.NoError(t, conn.Exec(`SET search_path TO idx_test`).Error)
	t.Cleanup(func() { conn.Exec(`DROP SCHEMA idx_test CASCADE`) })
	checkStatusAccountRepliesIndex(t, conn)
}
