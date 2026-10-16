package scripts

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
)

func TestInstanceIconBackfillMarksFreshRowsDue(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.Migrator().DropTable("instance_metadata"))
	require.NoError(t, gdb.AutoMigrate(&models.InstanceMetadata{}))
	future := time.Now().Add(20 * time.Hour)
	require.NoError(t, gdb.Create(&models.InstanceMetadata{Domain: "fresh.example", NextAttemptAt: &future}).Error)
	require.NoError(t, gdb.Create(&models.InstanceMetadata{Domain: "failing.example", FailCount: 3, NextAttemptAt: &future}).Error)

	require.NoError(t, InstanceIconBackfillScript{}.Up(gdb))
	require.NoError(t, InstanceIconBackfillScript{}.Up(gdb), "idempotent")

	var fresh, failing models.InstanceMetadata
	require.NoError(t, gdb.Take(&fresh, "domain = ?", "fresh.example").Error)
	require.NoError(t, gdb.Take(&failing, "domain = ?", "failing.example").Error)
	require.False(t, fresh.NextAttemptAt.After(time.Now()), "fresh rows become due")
	require.True(t, failing.NextAttemptAt.After(time.Now().Add(time.Hour)), "backoff kept")
}
