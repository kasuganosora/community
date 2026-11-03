package scripts

import (
	"strings"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// ReplyParentBackfillScript links replies to parents this server already
// has, using a stored inReplyTo address or one local status URL in the body.
// It does not fetch remote objects. Safe to run more than once.
type ReplyParentBackfillScript struct{}

func (ReplyParentBackfillScript) Up(db *gorm.DB) error {
	// Resolve the base URL on this transaction. systemsettings.GetBaseURL
	// loads settings on a second connection, which deadlocks when the pool
	// has one connection and this migration already holds it.
	_, err := models.RepairUnlinkedReplyParents(db, migrationBaseURL(db))
	return err
}

func migrationBaseURL(conn *gorm.DB) string {
	if config.AppBaseURLConfigured() && config.AppBaseHost() != "" {
		return strings.TrimRight(config.C.AppBaseURL, "/")
	}
	if conn != nil {
		var row models.SystemSetting
		if err := conn.Where("key = ?", systemsettings.KeyDomain).Limit(1).Find(&row).Error; err == nil {
			if domain, ok := row.Val.(string); ok {
				domain = strings.ToLower(strings.TrimSpace(domain))
				if domain != "" {
					return "https://" + domain
				}
			}
		}
	}
	if host := config.AppBaseHost(); host != "" {
		return strings.TrimRight(config.C.AppBaseURL, "/")
	}
	return "http://localhost"
}

func (ReplyParentBackfillScript) Down(*gorm.DB) error { return nil }
