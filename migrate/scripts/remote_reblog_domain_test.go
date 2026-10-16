package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRemoteReblogDomainScriptBackfillsDomain(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.Exec(`CREATE TABLE accounts (id integer primary key, domain text)`).Error)
	require.NoError(t, conn.Exec(`CREATE TABLE statuses (id integer primary key, account_id integer, domain text)`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO accounts VALUES (1, NULL), (2, 'maid.example')`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO statuses VALUES (10, 1, NULL), (11, 2, NULL), (12, 2, 'maid.example')`).Error)
	for range 2 { // idempotent
		require.NoError(t, RemoteReblogDomainScript{}.Up(conn))
	}
	type row struct {
		Id     uint64
		Domain *string
	}
	var rows []row
	require.NoError(t, conn.Raw(`SELECT id, domain FROM statuses ORDER BY id`).Scan(&rows).Error)
	require.Nil(t, rows[0].Domain, "local status stays local")
	require.Equal(t, "maid.example", *rows[1].Domain)
	require.Equal(t, "maid.example", *rows[2].Domain)
}
