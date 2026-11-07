package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMediaProcessingScriptAddsTaskTable(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.Exec(`CREATE TABLE media_attachments (
		id integer primary key,
		account_id integer not null default 0,
		file_name text not null default '',
		file_path text not null default '',
		file_size integer not null default 0,
		content_type text not null default '',
		type text not null default 'unknown',
		sensitive numeric not null default 0
	)`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO media_attachments (id, file_name) VALUES (1, 'old.png')`).Error)

	require.NoError(t, MediaProcessingScript{}.Up(conn))
	require.True(t, conn.Migrator().HasColumn(&models.MediaAttachment{}, "process_state"))
	require.True(t, conn.Migrator().HasTable(&models.MediaProcessTask{}))
	var state string
	require.NoError(t, conn.Raw(`SELECT process_state FROM media_attachments WHERE id = 1`).Scan(&state).Error)
	require.Equal(t, "ready", state)
	require.NoError(t, MediaProcessingScript{}.Up(conn))
}
