package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReactionActivityHistoryBackfillPreservesIdentityAndTombstone(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sql, err := conn.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sql.Close() })
	require.NoError(t, conn.Exec("CREATE TABLE accounts(id INTEGER PRIMARY KEY, domain TEXT)").Error)
	require.NoError(t, conn.Exec("INSERT INTO accounts(id,domain) VALUES(77,'remote.example'),(88,NULL)").Error)
	require.NoError(t, conn.AutoMigrate(&models.StatusReaction{}))
	rows := []models.StatusReaction{{ID: 901, AccountID: 77, StatusID: 1234, Name: "😀", ActivityURI: "https://remote.example/likes/seed"}, {ID: 902, AccountID: 88, StatusID: 1235, Name: "🔥", ActivityURI: ""}}
	require.NoError(t, conn.Create(&rows).Error)
	migration := ReactionActivityHistoryScript{}
	require.NoError(t, migration.Up(conn))
	var records []models.RemoteReactionActivity
	require.NoError(t, conn.Find(&records).Error)
	require.Len(t, records, 1)
	require.EqualValues(t, 77, records[0].AccountID)
	require.EqualValues(t, 1234, records[0].StatusID)
	require.Equal(t, "😀", records[0].Name)
	require.Equal(t, rows[0].ActivityURI, records[0].ActivityURI)
	require.NoError(t, conn.Model(&models.RemoteReactionActivity{}).Where("account_id = ?", 77).UpdateColumn("undone", true).Error)
	require.NoError(t, migration.Up(conn))
	records = nil
	require.NoError(t, conn.Find(&records).Error)
	require.Len(t, records, 1)
	require.True(t, records[0].Undone, "rerunning migration must not reactivate a tombstone")
}
