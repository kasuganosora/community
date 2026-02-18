package scripts

import (
	"fmt"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// MigrateRelayTable 中继表迁移
func MigrateRelayTable() error {
	log.Info("Running relay table migration...")

	sql := `
	-- 创建 relays 表
	CREATE TABLE IF NOT EXISTS relays (
		id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		inbox_url VARCHAR(512) NOT NULL,
		follow_activity_id VARCHAR(255),
		state TINYINT NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		deleted_at DATETIME DEFAULT NULL,
		UNIQUE INDEX idx_relays_inbox_url (inbox_url),
		INDEX idx_relays_state (state),
		INDEX idx_relays_follow_activity_id (follow_activity_id),
		INDEX idx_relays_deleted_at (deleted_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
	`

	if err := db.GetDB().Exec(sql).Error; err != nil {
		log.Info(fmt.Sprintf("Failed to create relays table: %v", err))
		return err
	}

	log.Info("Relay table migration completed successfully")
	return nil
}
