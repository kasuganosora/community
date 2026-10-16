package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/kasuganosora/community/tests/pgtest"
)

// legacyInstanceMetadataRow is instance_metadata before suspension_state.
type legacyInstanceMetadataRow struct {
	Domain string `gorm:"type:varchar(255);primaryKey"`
	Name   string `gorm:"type:varchar(255);default:''"`
}

func (legacyInstanceMetadataRow) TableName() string { return "instance_metadata" }

func TestInstanceModerationMigrationAddsSuspensionState(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.Migrator().DropTable("instance_metadata"))
	require.NoError(t, gdb.AutoMigrate(&legacyInstanceMetadataRow{}))
	require.NoError(t, gdb.Create(&legacyInstanceMetadataRow{Domain: "old.example"}).Error)

	require.NoError(t, InstanceModerationScript{}.Up(gdb))
	require.NoError(t, InstanceModerationScript{}.Up(gdb), "idempotent")
	var state string
	require.NoError(t, gdb.Raw("SELECT suspension_state FROM instance_metadata WHERE domain = ?", "old.example").Scan(&state).Error)
	require.Equal(t, "none", state, "existing rows get the default")

	require.NoError(t, InstanceModerationScript{}.Down(gdb))
	require.False(t, gdb.Migrator().HasColumn(&instanceSuspensionRow{}, "suspension_state"))
}
