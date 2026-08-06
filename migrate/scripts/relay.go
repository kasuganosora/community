package scripts

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
)

// MigrateRelayTableScript registers the relay table migration in the migration framework.
// Up is safe: uses CREATE TABLE IF NOT EXISTS.
type MigrateRelayTableScript struct{}

func (MigrateRelayTableScript) Up(db *gorm.DB) (err error) {
	// 注意：必须使用传入的 db（可能是事务），而非全局 db.GetDB()，
	// 否则 migrate.Up() 中的事务包裹将无法回滚。
	log.Info("Running relay table migration...")

	// XML (sqlexec) 模式不支持 MySQL 特有的 DDL 语法（ENGINE=InnoDB 等），
	// 跳过此 MySQL 特定脚本。
	if database.IsXmlMode() {
		log.Debug("Skip relay table migration in XML mode")
		return nil
	}

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

	err = db.Exec(sql).Error
	if err != nil {
		log.Error("Failed to create relays table: %v", err)

		return err
	}

	log.Info("Relay table migration completed successfully")

	return nil
}

func (MigrateRelayTableScript) Down(db *gorm.DB) (err error) {
	// XML (sqlexec) 模式下 Up 被跳过，Down 也应跳过。
	if database.IsXmlMode() {
		return nil
	}

	return db.Exec("DROP TABLE IF EXISTS relays").Error
}

// MigrateRelayTable 中继表迁移（使用全局 DB 句柄，供非迁移框架场景调用）.
func MigrateRelayTable() error {
	log.Info("Running relay table migration...")

	// XML (sqlexec) 模式不支持 MySQL 特有的 DDL 语法（ENGINE=InnoDB 等）。
	if database.IsXmlMode() {
		log.Debug("Skip relay table migration in XML mode")
		return nil
	}

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

	err := db.GetDB().Exec(sql).Error
	if err != nil {
		log.Error("Failed to create relays table: %v", err)

		return err
	}

	log.Info("Relay table migration completed successfully")

	return nil
}
