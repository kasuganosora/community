package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSuggestionActivityIndexSQLite(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, SuggestionActivityIndexScript{}.Up(conn), "missing table is a no-op")
	require.NoError(t, conn.Exec(`CREATE TABLE statuses (id integer primary key, account_id integer, created_at datetime)`).Error)
	require.NoError(t, SuggestionActivityIndexScript{}.Up(conn))
	require.NoError(t, SuggestionActivityIndexScript{}.Up(conn))
	var name string
	require.NoError(t, conn.Raw(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, "idx_statuses_account_created_at").Scan(&name).Error)
	require.Equal(t, "idx_statuses_account_created_at", name)
}
