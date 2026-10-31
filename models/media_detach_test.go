package models

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestClearDetachedMediaClearsUnlinkTime(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&MediaAttachment{}))

	unlinked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	row := &MediaAttachment{
		AccountId: 1, FileName: "a.png", FilePath: "2026/10/01/a.png",
		ContentType: "image/png", Type: MediaTypeImage, DetachedAt: &unlinked,
	}
	require.NoError(t, conn.Create(row).Error)

	require.NoError(t, clearDetachedMedia(conn, Attachments{row}))
	var got MediaAttachment
	require.NoError(t, conn.First(&got, row.Id).Error)
	require.Nil(t, got.DetachedAt)
	require.Nil(t, row.DetachedAt)
}
