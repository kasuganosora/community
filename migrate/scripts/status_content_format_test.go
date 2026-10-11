package scripts

import (
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStatusContentFormatMigrationPreservesPlain(t *testing.T) {
	db := adminMigrationDB(t)
	require.NoError(t, db.Exec("CREATE TABLE status_contents (status_id INTEGER PRIMARY KEY, content TEXT NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO status_contents(status_id,content) VALUES(1,'<b>literal</b>')").Error)
	m := StatusContentFormatScript{}
	require.NoError(t, m.Up(db))
	require.NoError(t, m.Up(db))
	var c models.StatusContent
	require.NoError(t, db.First(&c, 1).Error)
	require.Equal(t, "plain", c.ContentFormat)
	require.Equal(t, "<b>literal</b>", c.Content)
}
