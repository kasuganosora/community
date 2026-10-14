package feed

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestExcludeBackfilledJSONAcrossDialects(t *testing.T) {
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	var conn *gorm.DB
	if pgtest.Enabled() {
		conn = pgtest.Open(t, cfg)
	} else {
		var driver gorm.Dialector = sqlite.Open(":memory:")
		if dsn := os.Getenv("DISCOVERY_TEST_MYSQL_DSN"); dsn != "" {
			driver = mysql.Open(dsn)
		}
		var err error
		conn, err = gorm.Open(driver, cfg)
		require.NoError(t, err)
	}
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	// A connection-local temporary table leaves any application's statuses
	// untouched and exercises the actual database's JSON string semantics.
	require.NoError(t, conn.Exec("CREATE TEMPORARY TABLE statuses (id BIGINT PRIMARY KEY, properties JSON)").Error)
	for id, value := range []any{nil, `{}`, `{"other":"1"}`, `{"backfilled":"1"}`, `{"backfilled":"0"}`} {
		require.NoError(t, conn.Exec("INSERT INTO statuses (id,properties) VALUES (?,?)", id+1, value).Error)
	}
	var ids []uint64
	require.NoError(t, ExcludeBackfilled(conn.Table("statuses")).Order("id").Pluck("id", &ids).Error)
	require.Equal(t, []uint64{1, 2, 3, 5}, ids)
}
