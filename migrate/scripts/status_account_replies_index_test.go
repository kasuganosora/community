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
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1) // keep the session search_path
	require.NoError(t, conn.Exec(`DROP SCHEMA IF EXISTS idx_test CASCADE`).Error)
	require.NoError(t, conn.Exec(`CREATE SCHEMA idx_test`).Error)
	require.NoError(t, conn.Exec(`SET search_path TO idx_test`).Error)
	t.Cleanup(func() { conn.Exec(`DROP SCHEMA idx_test CASCADE`) })
	checkStatusAccountRepliesIndex(t, conn)

	// Must refuse to run inside a transaction (CONCURRENTLY would fail).
	require.NoError(t, conn.Exec(`CREATE TABLE statuses (id bigint primary key, account_id bigint, in_reply_to_id bigint)`).Error)
	require.Error(t, conn.Transaction(func(tx *gorm.DB) error { return StatusAccountRepliesIndexScript{}.Up(tx) }))

	// An interrupted concurrent build leaves an INVALID index of that name;
	// IF NOT EXISTS alone would keep it. Provoke one with a failing unique
	// build over duplicate rows.
	require.NoError(t, conn.Exec(`INSERT INTO statuses VALUES (1, 7, NULL), (2, 7, NULL)`).Error)
	require.Error(t, conn.Exec(`CREATE UNIQUE INDEX CONCURRENTLY idx_statuses_account_reply_id ON statuses (account_id)`).Error)
	require.Equal(t, []bool{false}, pgIndexValidity(t, conn))

	for range 2 {
		require.NoError(t, StatusAccountRepliesIndexScript{}.Up(conn))
	}
	require.Equal(t, []bool{true}, pgIndexValidity(t, conn))
	var def string
	require.NoError(t, conn.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname = 'idx_test' AND indexname = ?`, statusAccountRepliesIndex).Scan(&def).Error)
	require.Contains(t, def, "(account_id, in_reply_to_id, id)")
	require.NotContains(t, def, "UNIQUE")
}

func pgIndexValidity(t *testing.T, conn *gorm.DB) []bool {
	t.Helper()
	var valid []bool
	require.NoError(t, conn.Raw(`SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'idx_test' AND c.relname = ?`, statusAccountRepliesIndex).Scan(&valid).Error)
	return valid
}
