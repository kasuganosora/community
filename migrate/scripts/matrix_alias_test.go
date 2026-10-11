package scripts

import (
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestMatrixAliasMigrationBackfillsWithoutChangingAccounts(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.Exec("CREATE TABLE accounts (id INTEGER PRIMARY KEY, username TEXT, domain TEXT)").Error)
	require.NoError(t, conn.Exec("INSERT INTO accounts(id,username,domain) VALUES (1,'luna','maid.lat'),(2,'alice',NULL),(3,'a.b','example.org'),(4,'a_b','example.org')").Error)
	for i := 0; i < 2; i++ {
		require.NoError(t, (MatrixAliasScript{}).Up(conn))
	}
	require.True(t, conn.Migrator().HasIndex(&models.Account{}, "idx_account_matrix_alias"))
	var rows []models.Account
	require.NoError(t, conn.Unscoped().Order("id").Find(&rows).Error)
	require.Len(t, rows, 4)
	require.Equal(t, "luna_maid_lat_ap", rows[0].MatrixAlias)
	require.Equal(t, "alice", rows[1].MatrixAlias)
	require.Equal(t, "a.b", rows[2].Username)
	require.Equal(t, rows[2].MatrixAlias, rows[3].MatrixAlias)
}
