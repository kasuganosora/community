package scripts

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/adminwebhook"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func adminMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	raw, err := conn.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	require.NoError(t, conn.Exec("CREATE TABLE accounts (id INTEGER PRIMARY KEY, username TEXT, created_at DATETIME)").Error)
	require.NoError(t, conn.Exec("CREATE TABLE statuses (id INTEGER PRIMARY KEY, account_id INTEGER, created_at DATETIME)").Error)
	require.NoError(t, conn.Exec("INSERT INTO accounts(id,username) VALUES (1,'preserved')").Error)
	return conn
}

func TestAdminManagementMigrationIdempotentPreservesData(t *testing.T) {
	conn := adminMigrationDB(t)
	require.Contains(t, GetScripts(), "AdminManagementScript")
	migration := AdminManagementScript{}
	require.NoError(t, migration.Up(conn))
	for _, model := range []any{&models.AdminAuditLog{}, &models.Announcement{}, &models.AnnouncementDismissal{}, &models.AvatarDecoration{}, &models.Advertisement{}, &adminwebhook.Subscription{}, &adminwebhook.Delivery{}} {
		require.True(t, conn.Migrator().HasTable(model))
	}
	now := time.Now().UTC()
	announcement := models.Announcement{Text: "preserve publication", Published: true}
	require.NoError(t, conn.Create(&announcement).Error)
	require.NoError(t, conn.Create(&models.AnnouncementDismissal{AccountID: 1, AnnouncementID: announcement.Id, ReadAt: now}).Error)
	decoration := models.AvatarDecoration{Name: "preserve", ImageURL: "/files/local.png", Enabled: true}
	require.NoError(t, conn.Create(&decoration).Error)
	require.NoError(t, conn.Exec("UPDATE accounts SET avatar_decoration_id = ? WHERE id = 1", decoration.ID).Error)
	sub := adminwebhook.Subscription{URL: "https://hooks.example/receiver", Secret: "test-only-secret", Enabled: true}
	require.NoError(t, conn.Create(&sub).Error)
	delivery := adminwebhook.Delivery{ID: "receipt-preserved", SubscriptionID: sub.ID, EventID: 17, State: "delivered", Attempts: 1}
	require.NoError(t, conn.Create(&delivery).Error)
	require.NoError(t, migration.Up(conn))
	var account struct {
		Username           string
		AvatarDecorationID uint64
	}
	require.NoError(t, conn.Table("accounts").First(&account, "id = ?", 1).Error)
	require.Equal(t, "preserved", account.Username)
	require.Equal(t, decoration.ID, account.AvatarDecorationID)
	var loaded models.Announcement
	require.NoError(t, conn.First(&loaded, announcement.Id).Error)
	require.Equal(t, announcement.Text, loaded.Text)
	require.True(t, loaded.Published)
	var count int64
	require.NoError(t, conn.Model(&models.AnnouncementDismissal{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var receipt adminwebhook.Delivery
	require.NoError(t, conn.First(&receipt, "id = ?", delivery.ID).Error)
	require.Equal(t, "delivered", receipt.State)
	require.True(t, conn.Migrator().HasIndex("accounts", "idx_accounts_created_at"))
	require.True(t, conn.Migrator().HasIndex("statuses", "idx_statuses_account_created_at"))
	require.Error(t, migration.Down(conn))
	// Durable event uniqueness and dismissal composite identity remain enforced.
	duplicate := adminwebhook.Delivery{ID: "another-id", SubscriptionID: sub.ID, EventID: 17}
	require.Error(t, conn.Create(&duplicate).Error)
	require.Error(t, conn.Create(&models.AnnouncementDismissal{AccountID: 1, AnnouncementID: announcement.Id, ReadAt: now}).Error)
}

func TestAdminAnalyticsIndexesReuseEquivalentPrefixes(t *testing.T) {
	conn := adminMigrationDB(t)
	require.NoError(t, conn.Exec("CREATE INDEX custom_accounts_dates ON accounts(created_at,id)").Error)
	require.NoError(t, conn.Exec("CREATE INDEX custom_status_history ON statuses(account_id,created_at,id)").Error)
	require.NoError(t, (AdminManagementScript{}).Up(conn))
	require.NoError(t, (AdminManagementScript{}).Up(conn))
	require.False(t, conn.Migrator().HasIndex("accounts", "idx_accounts_created_at"))
	require.False(t, conn.Migrator().HasIndex("statuses", "idx_statuses_account_created_at"))
}

func TestAdminAnalyticsIndexesRejectNameCollision(t *testing.T) {
	conn := adminMigrationDB(t)
	require.NoError(t, conn.Exec("CREATE INDEX idx_statuses_account_created_at ON statuses(created_at,account_id)").Error)
	require.ErrorContains(t, (AdminManagementScript{}).Up(conn), "incompatible columns")
}
