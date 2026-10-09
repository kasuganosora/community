package scripts

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// MigrateMatrixTablesScript registers the Matrix table migration in the migration framework.
// Up is safe: all statements use CREATE TABLE IF NOT EXISTS.
type MigrateMatrixTablesScript struct{}

func (MigrateMatrixTablesScript) Up(db *gorm.DB) (err error) {
	// 注意：必须使用传入的 db（可能是事务），而非全局 db.GetDB()，
	// 否则 migrate.Up() 中的事务包裹将无法回滚。
	log.Info("Running Matrix tables migration...")

	// XML (sqlexec) 模式不支持 MySQL 特有的 DDL 语法（ENGINE=InnoDB、COMMENT 等），
	// 跳过此 MySQL 特定脚本。
	if database.IsXmlMode() {
		log.Debug("Skip Matrix tables migration in XML mode")
		return nil
	}

	err = execTableDDL(db, matrixTablesSQL, matrixTablesPostgresSQL, &models.MatrixUser{}, &models.MatrixRoom{}, &models.MatrixMessage{})
	if err != nil {
		log.Error("Failed to run Matrix tables migration: %v", err)

		return err
	}

	log.Info("Matrix tables migration completed successfully")

	return nil
}

func (MigrateMatrixTablesScript) Down(db *gorm.DB) (err error) {
	// 注意：必须使用传入的 db（可能是事务），而非全局 db.GetDB()。
	// XML (sqlexec) 模式下 Up 被跳过，Down 也应跳过。
	if database.IsXmlMode() {
		return nil
	}

	return db.Exec(`
		DROP TABLE IF EXISTS matrix_messages;
		DROP TABLE IF EXISTS matrix_rooms;
		DROP TABLE IF EXISTS matrix_users;
	`).Error
}

// MigrateMatrixTables Matrix 表迁移（使用全局 DB 句柄，供非迁移框架场景调用）.
func MigrateMatrixTables() error {
	log.Info("Running Matrix tables migration...")

	// XML (sqlexec) 模式不支持 MySQL 特有的 DDL 语法（ENGINE=InnoDB、COMMENT 等）。
	if database.IsXmlMode() {
		log.Debug("Skip Matrix tables migration in XML mode")
		return nil
	}

	conn := db.GetDB()
	err := execTableDDL(conn, matrixTablesSQL, matrixTablesPostgresSQL, &models.MatrixUser{}, &models.MatrixRoom{}, &models.MatrixMessage{})
	if err != nil {
		log.Error("Failed to run Matrix tables migration: %v", err)

		return err
	}

	log.Info("Matrix tables migration completed successfully")

	return nil
}

// DropMatrixTables 删除 Matrix 表（用于回滚，使用全局 DB 句柄）.
func DropMatrixTables() error {
	log.Info("Dropping Matrix tables...")

	err := db.GetDB().Exec(`
		DROP TABLE IF EXISTS matrix_messages;
		DROP TABLE IF EXISTS matrix_rooms;
		DROP TABLE IF EXISTS matrix_users;
	`).Error
	if err != nil {
		log.Error("Failed to drop Matrix tables: %v", err)

		return err
	}

	log.Info("Matrix tables dropped successfully")

	return nil
}

// matrixTablesSQL is the shared SQL for Matrix mapping table migration.
// All statements use CREATE TABLE IF NOT EXISTS for idempotency.
const matrixTablesSQL = `
	-- 创建 matrix_users 表
	CREATE TABLE IF NOT EXISTS matrix_users (
		id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		local_account_id BIGINT UNSIGNED NOT NULL COMMENT '本地账号ID',
		matrix_user_id VARCHAR(255) NOT NULL COMMENT 'Matrix用户ID完整格式',
		matrix_localpart VARCHAR(128) NOT NULL COMMENT 'Matrix用户名部分',
		matrix_server VARCHAR(128) NOT NULL COMMENT 'Matrix服务器域名',
		direction VARCHAR(16) NOT NULL DEFAULT 'local' COMMENT '映射方向: local/remote',
		display_name VARCHAR(128) DEFAULT '' COMMENT 'Matrix显示名称',
		avatar_url VARCHAR(512) DEFAULT '' COMMENT 'Matrix头像URL',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		deleted_at DATETIME DEFAULT NULL,
		INDEX idx_local_account_id (local_account_id),
		UNIQUE INDEX idx_matrix_user_id (matrix_user_id),
		INDEX idx_matrix_localpart (matrix_localpart),
		INDEX idx_matrix_server (matrix_server),
		INDEX idx_direction (direction),
		INDEX idx_deleted_at (deleted_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Matrix用户映射表';

	-- 创建 matrix_rooms 表
	CREATE TABLE IF NOT EXISTS matrix_rooms (
		id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		conversation_id BIGINT UNSIGNED NOT NULL COMMENT '本地会话ID',
		matrix_room_id VARCHAR(255) NOT NULL COMMENT 'Matrix房间ID完整格式',
		matrix_room_localpart VARCHAR(128) DEFAULT '' COMMENT 'Matrix房间名部分',
		matrix_server VARCHAR(128) NOT NULL COMMENT 'Matrix服务器域名',
		is_direct BOOLEAN NOT NULL DEFAULT TRUE COMMENT '是否为私信房间',
		room_name VARCHAR(255) DEFAULT '' COMMENT '房间名称',
		last_event_id VARCHAR(255) DEFAULT '' COMMENT '最后同步的事件ID',
		last_sync_at DATETIME DEFAULT NULL COMMENT '最后同步时间',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		deleted_at DATETIME DEFAULT NULL,
		UNIQUE INDEX idx_conversation_id (conversation_id),
		UNIQUE INDEX idx_matrix_room_id (matrix_room_id),
		INDEX idx_matrix_room_localpart (matrix_room_localpart),
		INDEX idx_matrix_server (matrix_server),
		INDEX idx_is_direct (is_direct),
		INDEX idx_deleted_at (deleted_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Matrix房间映射表';

	-- 创建 matrix_messages 表
	CREATE TABLE IF NOT EXISTS matrix_messages (
		id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		status_id BIGINT UNSIGNED NOT NULL COMMENT '本地Status ID',
		matrix_event_id VARCHAR(255) NOT NULL COMMENT 'Matrix事件ID',
		matrix_room_id VARCHAR(255) NOT NULL COMMENT 'Matrix房间ID',
		matrix_sender_id VARCHAR(255) NOT NULL COMMENT 'Matrix发送者ID',
		direction VARCHAR(16) NOT NULL COMMENT '同步方向: to_matrix/from_matrix',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE INDEX idx_status_id (status_id),
		UNIQUE INDEX idx_matrix_event_id (matrix_event_id),
		INDEX idx_matrix_room_id (matrix_room_id),
		INDEX idx_matrix_sender_id (matrix_sender_id),
		INDEX idx_direction (direction),
		INDEX idx_created_at (created_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Matrix消息映射表';
	`
