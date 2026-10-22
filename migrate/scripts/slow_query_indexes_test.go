package scripts

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createSlowQueryTables(t *testing.T, conn *gorm.DB) {
	t.Helper()
	for _, ddl := range []string{
		`CREATE TABLE accounts (id bigint primary key, username varchar(255), domain varchar(255))`,
		`CREATE TABLE tags (id bigint primary key, name_key varchar(64))`,
		`CREATE TABLE statuses (id bigint primary key, created_at timestamp, deleted_at timestamp)`,
		`CREATE TABLE status_tags (id bigint primary key, status_id bigint, tag_id bigint)`,
		`CREATE TABLE matrix_room_members (id bigint primary key, avatar_url varchar(512))`,
		`CREATE TABLE feed_events (id bigint primary key, state varchar(16), updated_at timestamp)`,
	} {
		require.NoError(t, conn.Exec(ddl).Error)
	}
}

func TestSlowQueryIndexesSQLite(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, SlowQueryIndexesScript{}.Up(conn), "missing tables are skipped")
	createSlowQueryTables(t, conn)
	for range 2 {
		require.NoError(t, SlowQueryIndexesScript{}.Up(conn))
	}
	for _, ix := range slowQueryIndexes {
		require.Equal(t, ix.sqlite != "", conn.Migrator().HasIndex(ix.table, ix.name), ix.name)
	}
}

func TestSlowQueryIndexesPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	conn, err := gorm.Open(dialect.OpenPostgres(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.Exec(`DROP SCHEMA IF EXISTS sq_idx_test CASCADE`).Error)
	require.NoError(t, conn.Exec(`CREATE SCHEMA sq_idx_test`).Error)
	require.NoError(t, conn.Exec(`SET search_path TO sq_idx_test`).Error)
	t.Cleanup(func() { conn.Exec(`DROP SCHEMA sq_idx_test CASCADE`) })
	createSlowQueryTables(t, conn)
	require.Error(t, conn.Transaction(func(tx *gorm.DB) error { return SlowQueryIndexesScript{}.Up(tx) }))
	// An interrupted concurrent build leaves an INVALID index; it is rebuilt.
	require.NoError(t, conn.Exec(`INSERT INTO tags VALUES (1, 'a'), (2, 'a')`).Error)
	require.Error(t, conn.Exec(`CREATE UNIQUE INDEX CONCURRENTLY idx_tags_name_key_pattern ON tags (name_key)`).Error)
	for range 2 {
		require.NoError(t, SlowQueryIndexesScript{}.Up(conn))
	}
	for _, ix := range slowQueryIndexes {
		var valid []bool
		require.NoError(t, conn.Raw(`SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
			WHERE c.relname = ? AND c.relnamespace = to_regnamespace(current_schema())::oid`, ix.name).Scan(&valid).Error)
		require.Equal(t, []bool{true}, valid, ix.name)
	}
}

func TestRetentionIndexesSQLite(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, RetentionIndexesScript{}.Up(conn), "missing tables are skipped")
	require.NoError(t, conn.Exec(`CREATE TABLE activitypub_processed_activities (id bigint primary key, activity_id varchar(255), created_at timestamp)`).Error)
	require.NoError(t, conn.Exec(`CREATE TABLE activitypub_deliveries (id bigint primary key, status varchar(20), updated_at timestamp)`).Error)
	for range 2 {
		require.NoError(t, RetentionIndexesScript{}.Up(conn))
	}
	for _, ix := range retentionIndexes {
		require.True(t, conn.Migrator().HasIndex(ix.table, ix.name), ix.name)
	}
}

func TestRetentionIndexesPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	conn, err := gorm.Open(dialect.OpenPostgres(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.Exec(`DROP SCHEMA IF EXISTS ret_idx_test CASCADE`).Error)
	require.NoError(t, conn.Exec(`CREATE SCHEMA ret_idx_test`).Error)
	require.NoError(t, conn.Exec(`SET search_path TO ret_idx_test`).Error)
	t.Cleanup(func() { conn.Exec(`DROP SCHEMA ret_idx_test CASCADE`) })
	require.NoError(t, conn.Exec(`CREATE TABLE activitypub_processed_activities (id bigint primary key, activity_id varchar(255), created_at timestamptz)`).Error)
	require.NoError(t, conn.Exec(`CREATE TABLE activitypub_deliveries (id bigint primary key, status varchar(20), updated_at timestamptz)`).Error)
	require.Error(t, conn.Transaction(func(tx *gorm.DB) error { return RetentionIndexesScript{}.Up(tx) }))
	for range 2 {
		require.NoError(t, RetentionIndexesScript{}.Up(conn))
	}
	for _, ix := range retentionIndexes {
		var valid []bool
		require.NoError(t, conn.Raw(`SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
			WHERE c.relname = ? AND c.relnamespace = to_regnamespace(current_schema())::oid`, ix.name).Scan(&valid).Error)
		require.Equal(t, []bool{true}, valid, ix.name)
	}
}
