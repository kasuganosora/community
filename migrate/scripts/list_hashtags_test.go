package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestListHashtagsScriptIsIdempotentAndUnique(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	s := ListHashtagsScript{}
	require.NoError(t, s.Up(conn))
	require.NoError(t, s.Up(conn))
	require.True(t, conn.Migrator().HasIndex("list_hashtags", "uidx_list_hashtags_list_tag"))
	require.True(t, conn.Migrator().HasIndex(&listHashtagRow{}, "TagId"))
	require.NoError(t, conn.Create(&listHashtagRow{ListId: 1, TagId: 2}).Error)
	require.Error(t, conn.Create(&listHashtagRow{ListId: 1, TagId: 2}).Error)
	require.NoError(t, s.Down(conn))
	require.False(t, conn.Migrator().HasTable("list_hashtags"))
}
