package scripts

import (
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestCustomEmojiMetadataMigrationPreservesRowsAndScopesUniqueness(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.Exec(`CREATE TABLE emojis (id integer PRIMARY KEY AUTOINCREMENT,shortcode varchar(36) NOT NULL,url varchar(255) NOT NULL,static_url varchar(255) NOT NULL,visible_in_picker numeric,domain varchar(255) NOT NULL,category text,created_at integer,updated_at integer,deleted_at datetime,CONSTRAINT uni_emojis_shortcode UNIQUE(shortcode))`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO emojis(id,shortcode,url,static_url,domain,visible_in_picker) VALUES(1,'wave','https://local/a.png','https://local/a.png','',1)`).Error)
	migration := CustomEmojiMetadataScript{}
	require.NoError(t, migration.Up(conn))
	require.NoError(t, migration.Up(conn))
	var original models.Emoji
	require.NoError(t, conn.First(&original, 1).Error)
	require.Equal(t, "wave", original.Shortcode)
	require.Equal(t, "https://local/a.png", original.Url)
	require.False(t, original.Disabled)
	require.NoError(t, conn.Create(&models.Emoji{Id: 2, Shortcode: "wave", Domain: "remote.example", Url: "https://remote/a.png", StaticUrl: "https://remote/a.png"}).Error)
	require.Error(t, conn.Create(&models.Emoji{Id: 3, Shortcode: "wave", Domain: "", Url: "x", StaticUrl: "x"}).Error)
	require.True(t, conn.Migrator().HasTable(&models.EmojiUpload{}))
}
