package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// DiscoveryScript only adds data; older deployments can roll back application
// code without losing consent, negative feedback or administrative history.
type DiscoveryScript struct{}

func (DiscoveryScript) Up(conn *gorm.DB) error {
	if err := conn.AutoMigrate(&models.RecommendationFeedback{}); err != nil {
		return err
	}
	if err := models.EnsureRecommendationFeedbackSchema(conn); err != nil {
		return err
	}
	for _, field := range []string{"RemoteDiscoverable", "RemoteIndexable", "RemoteHideCollections", "RemoteDiscoveryCheckedAt", "RemoteDiscoveryAttemptedAt", "RemoteDiscoverySource"} {
		if !conn.Migrator().HasColumn(&models.Account{}, field) {
			if err := conn.Migrator().AddColumn(&models.Account{}, field); err != nil {
				return err
			}
		}
	}
	for _, index := range []string{"idx_accounts_remote_discovery_checked_at", "idx_accounts_remote_discovery_attempted_at"} {
		if !conn.Migrator().HasIndex(&models.Account{}, index) {
			if err := conn.Migrator().CreateIndex(&models.Account{}, index); err != nil {
				return err
			}
		}
	}
	if !conn.Migrator().HasColumn(&models.AdminAuditLog{}, "Details") {
		return conn.Migrator().AddColumn(&models.AdminAuditLog{}, "Details")
	}
	return nil
}

func (DiscoveryScript) Down(*gorm.DB) error { return nil }
