package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDropDiscoveryAttemptedIndexScriptIsIdempotent(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.Exec("CREATE TABLE accounts (id integer primary key, remote_discovery_attempted_at datetime)").Error)
	require.NoError(t, conn.Exec("CREATE INDEX "+discoveryAttemptedIndex+" ON accounts(remote_discovery_attempted_at)").Error)
	s := DropDiscoveryAttemptedIndexScript{}
	require.NoError(t, s.Up(conn))
	require.False(t, conn.Migrator().HasIndex("accounts", discoveryAttemptedIndex))
	require.NoError(t, s.Up(conn))
	require.NoError(t, s.Down(conn))
	require.True(t, conn.Migrator().HasIndex("accounts", discoveryAttemptedIndex))
}
