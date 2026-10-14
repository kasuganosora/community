package systemsettings

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDiscoveryConfigurationReadsPrimaryAndFailsClosed(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.SystemSetting{}))
	old := config.C.Other
	config.C.Other = map[string]string{}
	t.Cleanup(func() { config.C.Other = old })
	ctx := context.Background()
	settings, err := ReadDiscoveryConfiguration(ctx, conn)
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.True(t, settings.TrendsEnabled)
	require.Equal(t, "auto", settings.Mode)
	require.False(t, settings.RequireManualApproval)
	row := models.SystemSetting{Key: KeyRecommendationEnabled, Val: true}
	require.NoError(t, conn.Create(&row).Error)
	settings, err = ReadDiscoveryConfiguration(ctx, conn)
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	row.Val = false
	require.NoError(t, conn.Save(&row).Error)
	settings, err = ReadDiscoveryConfiguration(ctx, conn)
	require.NoError(t, err)
	require.False(t, settings.Enabled, "another replica's write must take effect without process-cache refresh")
	config.C.Other[KeyRecommendationEnabled] = "true"
	settings, err = ReadDiscoveryConfiguration(ctx, conn)
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	manual := models.SystemSetting{Key: KeyRecommendationManualApproval, Val: true}
	require.NoError(t, conn.Create(&manual).Error)
	settings, err = ReadDiscoveryConfiguration(ctx, conn)
	require.NoError(t, err)
	require.True(t, settings.RequireManualApproval, "manual approval must be read from the primary database")
	require.False(t, settings.ApprovesIndex("built-version"), "manual review must not approve an unreviewed version")
	manual.Val = false
	require.NoError(t, conn.Save(&manual).Error)
	settings, err = ReadDiscoveryConfiguration(ctx, conn)
	require.NoError(t, err)
	require.False(t, settings.RequireManualApproval, "another replica's change must take effect without a cache refresh")
	config.C.Other[KeyRecommendationManualApproval] = "true"
	settings, err = ReadDiscoveryConfiguration(ctx, conn)
	require.NoError(t, err)
	require.True(t, settings.RequireManualApproval, "deployment configuration takes precedence over the database")
	require.NoError(t, conn.Migrator().DropTable(&models.SystemSetting{}))
	_, err = ReadDiscoveryConfiguration(ctx, conn)
	require.Error(t, err)
	require.False(t, DiscoveryEnabled(ctx, conn), "unavailable authoritative state must never enable recommendations")
}

func TestDiscoveryPolicyEpochNeverRevivesOnReenable(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.SystemSetting{}))
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	oldValues, oldLoaded := settings, loaded
	settings = map[string]any{}
	loaded = true
	t.Cleanup(func() { settings, loaded = oldValues, oldLoaded })
	oldConfig := config.C.Other
	config.C.Other = map[string]string{}
	t.Cleanup(func() { config.C.Other = oldConfig })
	ctx := context.Background()
	input := map[string]any{KeyRecommendationEnabled: true}
	require.NoError(t, SaveChanges(input))
	require.Len(t, input, 1, "settings writes must not mutate caller-owned input")
	first, err := DiscoveryPolicyVersion(ctx, conn)
	require.NoError(t, err)
	require.NoError(t, SaveChanges(map[string]any{KeyRecommendationEnabled: false}))
	require.NoError(t, SaveChanges(map[string]any{KeyRecommendationEnabled: true}))
	second, err := DiscoveryPolicyVersion(ctx, conn)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.NoError(t, SaveChanges(map[string]any{KeyDiscoveryTrendsEnabled: false}))
	third, err := DiscoveryPolicyVersion(ctx, conn)
	require.NoError(t, err)
	require.Equal(t, second, third, "independent trends setting should not discard a feed window")

	require.NoError(t, SaveChanges(map[string]any{KeyRecommendationManualApproval: false}))
	beforeReview, err := DiscoveryPolicyVersion(ctx, conn)
	require.NoError(t, err)
	require.NoError(t, SaveChanges(map[string]any{KeyRecommendationManualApproval: true}))
	manual, err := ReadDiscoveryConfiguration(ctx, conn)
	require.NoError(t, err)
	require.True(t, manual.RequireManualApproval)
	duringReview, err := DiscoveryPolicyVersion(ctx, conn)
	require.NoError(t, err)
	require.NotEqual(t, beforeReview, duringReview, "enabling manual review must invalidate an automatic-mode feed window")
	require.NoError(t, SaveChanges(map[string]any{KeyRecommendationManualApproval: false}))
	automatic, err := ReadDiscoveryConfiguration(ctx, conn)
	require.NoError(t, err)
	require.False(t, automatic.RequireManualApproval)
	afterReview, err := DiscoveryPolicyVersion(ctx, conn)
	require.NoError(t, err)
	require.NotEqual(t, beforeReview, afterReview, "turning manual review off must not revive an older automatic-mode feed window")
	require.NotEqual(t, duringReview, afterReview)
}
