package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMediaDetachedAtScriptAddsColumn(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.Exec(`CREATE TABLE media_attachments (id integer primary key)`).Error)

	require.NoError(t, MediaDetachedAtScript{}.Up(conn))
	require.True(t, conn.Migrator().HasColumn(&models.MediaAttachment{}, "detached_at"))
	require.True(t, conn.Migrator().HasIndex(&models.MediaAttachment{}, "DetachedAt") || conn.Migrator().HasIndex(&models.MediaAttachment{}, "idx_media_attachments_detached_at"))

	require.NoError(t, MediaDetachedAtScript{}.Up(conn))
}
