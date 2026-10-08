package scripts

import (
	"fmt"
	"strings"

	"github.com/kasuganosora/community/adminwebhook"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// AdminManagementScript is additive. Existing accounts, announcements and
// webhook delivery receipts survive upgrades and repeated invocations.
type AdminManagementScript struct{}

func (AdminManagementScript) Up(conn *gorm.DB) error {
	if err := conn.AutoMigrate(&models.AdminAuditLog{}, &models.Announcement{}, &models.AnnouncementDismissal{}, &models.AvatarDecoration{}, &models.Advertisement{}, &adminwebhook.Subscription{}, &adminwebhook.Delivery{}); err != nil {
		return err
	}
	if !conn.Migrator().HasColumn(&models.Account{}, "AvatarDecorationID") {
		if err := conn.Migrator().AddColumn(&models.Account{}, "AvatarDecorationID"); err != nil {
			return err
		}
	}
	if database.IsXmlMode() && conn.Dialector.Name() != "sqlite" {
		return nil
	}
	// accounts(created_at) normally already exists from performance_indexes.go.
	// Check column prefixes, not just names, to avoid duplicating equivalent
	// administrator-created indexes or a wider suitable composite index.
	if err := ensureAdminAnalyticsIndex(conn, "accounts", "idx_accounts_created_at", []string{"created_at"}); err != nil {
		return err
	}
	return ensureAdminAnalyticsIndex(conn, "statuses", "idx_statuses_account_created_at", []string{"account_id", "created_at"})
}

func ensureAdminAnalyticsIndex(conn *gorm.DB, table, name string, columns []string) error {
	indexes, err := conn.Migrator().GetIndexes(table)
	if err != nil {
		return fmt.Errorf("inspect analytics index on %s: %w", table, err)
	}
	for _, index := range indexes {
		actual := index.Columns()
		if len(actual) < len(columns) {
			continue
		}
		matches := true
		for i, column := range columns {
			if !strings.EqualFold(actual[i], column) {
				matches = false
				break
			}
		}
		if matches {
			return nil
		}
	}
	if conn.Migrator().HasIndex(table, name) {
		return fmt.Errorf("analytics index %s exists with incompatible columns", name)
	}
	// Identifiers are fixed literals controlled exclusively by this migration.
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = dialect.Quote(conn, column)
	}
	return conn.Exec("CREATE INDEX " + dialect.Quote(conn, name) + " ON " + dialect.Quote(conn, table) + " (" + strings.Join(quoted, ",") + ")").Error
}

func (AdminManagementScript) Down(*gorm.DB) error {
	return fmt.Errorf("admin management migration is irreversible: audit and delivery history must be preserved")
}
