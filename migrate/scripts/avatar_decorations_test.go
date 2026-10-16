package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
)

type legacyAccountRow struct {
	Id                 uint64 `gorm:"primaryKey"`
	Username           string `gorm:"type:varchar(64)"`
	AvatarDecorationID uint64 `gorm:"not null;default:0"`
}

func (legacyAccountRow) TableName() string { return "accounts" }

func TestAvatarDecorationsScriptAddsNullableColumnsIdempotently(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, AvatarDecorationsScript{}.Up(gdb)) // no accounts table yet
	require.NoError(t, gdb.AutoMigrate(&legacyAccountRow{}))
	require.NoError(t, gdb.Create(&legacyAccountRow{Id: 1, Username: "a", AvatarDecorationID: 4}).Error)

	require.NoError(t, AvatarDecorationsScript{}.Up(gdb))
	require.NoError(t, AvatarDecorationsScript{}.Up(gdb))
	require.True(t, gdb.Migrator().HasColumn("accounts", "avatar_decorations"))
	require.True(t, gdb.Migrator().HasColumn("accounts", "avatar_decorations_fetched_at"))

	list := models.AvatarDecorationSelections{{ID: "4", Angle: 0.25, FlipH: true}}
	require.NoError(t, gdb.Table("accounts").Where("id = ?", 1).UpdateColumn("avatar_decorations", list).Error)
	var got struct {
		AvatarDecorations  models.AvatarDecorationSelections `gorm:"type:text"`
		AvatarDecorationID uint64
	}
	require.NoError(t, gdb.Table("accounts").Select("avatar_decorations", "avatar_decoration_id").Where("id = ?", 1).Scan(&got).Error)
	require.Equal(t, list, got.AvatarDecorations)
	require.EqualValues(t, 4, got.AvatarDecorationID)
}
