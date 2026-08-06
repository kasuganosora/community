package scripts

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
)

// MigrateMatrixHSScript registers the Matrix Homeserver table migration in the migration framework.
// Up is safe: all SQL statements use CREATE TABLE IF NOT EXISTS (from GetMatrixHSSQL).
// Note: The original MigrateMatrixHS() function was a no-op (empty AutoMigrate).
// This script properly executes the SQL definitions from GetMatrixHSSQL().
type MigrateMatrixHSScript struct{}

func (MigrateMatrixHSScript) Up(db *gorm.DB) (err error) {
	// XML (sqlexec) 模式不支持 MySQL 特有的 DDL 语法（ENGINE=InnoDB、JSON 类型等），
	// 跳过此 MySQL 特定脚本。
	if database.IsXmlMode() {
		log.Debug("Skip Matrix Homeserver tables migration in XML mode")
		return nil
	}

	for _, sql := range GetMatrixHSSQL() {
		err = db.Exec(sql).Error
		if err != nil {
			return err
		}
	}

	return nil
}

func (MigrateMatrixHSScript) Down(db *gorm.DB) (err error) {
	// 表名是硬编码常量（非用户输入），不存在 SQL 注入风险。
	// 使用反引号引用标识符以避免与保留字冲突。
	// XML (sqlexec) 模式下 Up 被跳过，Down 也应跳过。
	if database.IsXmlMode() {
		return nil
	}

	tables := []string{
		"`matrix_cross_signing_keys`", "`matrix_one_time_keys`", "`matrix_device_keys`",
		"`matrix_room_aliases`", "`matrix_presence`", "`matrix_filters`",
		"`matrix_typing`", "`matrix_account_data`", "`matrix_receipts`",
		"`matrix_to_device_messages`", "`matrix_user_sessions`", "`matrix_room_members`",
		"`matrix_room_state`", "`matrix_rooms_hs`", "`matrix_events`",
	}
	for _, table := range tables {
		err = db.Exec("DROP TABLE IF EXISTS " + table).Error
		if err != nil {
			return err
		}
	}

	return nil
}

// MigrateMatrixHS Matrix Homeserver 数据库迁移.
func MigrateMatrixHS() error {
	// 使用 GORM 的 AutoMigrate
	return db.GetDB().AutoMigrate(
	// Matrix Homeserver 核心表
	// 注意：这些表在 homeserver 包中定义，需要通过反射导入
	// 这里手动创建表结构
	)
}

// GetMatrixHSSQL 获取 Matrix Homeserver 建表 SQL.
func GetMatrixHSSQL() []string {
	return []string{
		// matrix_events - 事件表
		`CREATE TABLE IF NOT EXISTS matrix_events (
			event_id VARCHAR(255) PRIMARY KEY,
			room_id VARCHAR(255) NOT NULL,
			event_type VARCHAR(64) NOT NULL,
			sender VARCHAR(255) NOT NULL,
			state_key VARCHAR(255),
			content JSON,
			prev_events JSON,
			auth_events JSON,
			depth BIGINT NOT NULL DEFAULT 0,
			origin_server_ts BIGINT NOT NULL,
			redacts VARCHAR(255),
			sender_domain VARCHAR(128),
			signatures JSON,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_room_id (room_id),
			INDEX idx_event_type (event_type),
			INDEX idx_sender (sender),
			INDEX idx_origin_server_ts (origin_server_ts),
			INDEX idx_room_depth (room_id, depth)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_rooms_hs - 房间表（Homeserver 使用）
		`CREATE TABLE IF NOT EXISTS matrix_rooms_hs (
			room_id VARCHAR(255) PRIMARY KEY,
			room_version VARCHAR(16) DEFAULT '10',
			room_type VARCHAR(32),
			creator VARCHAR(255),
			name VARCHAR(255),
			topic VARCHAR(512),
			avatar_url VARCHAR(512),
			join_rule VARCHAR(32) DEFAULT 'invite',
			history_visibility VARCHAR(32) DEFAULT 'shared',
			guest_access VARCHAR(32) DEFAULT 'forbidden',
			is_direct BOOLEAN DEFAULT FALSE,
			is_encrypted BOOLEAN DEFAULT FALSE,
			current_state_id VARCHAR(255),
			last_event_id VARCHAR(255),
			last_event_ts BIGINT,
			conversation_id BIGINT UNSIGNED,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			deleted_at DATETIME,
			INDEX idx_creator (creator),
			INDEX idx_conversation_id (conversation_id),
			INDEX idx_deleted_at (deleted_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_room_state - 房间状态表
		`CREATE TABLE IF NOT EXISTS matrix_room_state (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			room_id VARCHAR(255) NOT NULL,
			event_type VARCHAR(64) NOT NULL,
			state_key VARCHAR(255) NOT NULL,
			content JSON,
			event_id VARCHAR(255),
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_room_id (room_id),
			UNIQUE INDEX idx_room_state (room_id, event_type, state_key)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_room_members - 房间成员表
		`CREATE TABLE IF NOT EXISTS matrix_room_members (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			room_id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			membership VARCHAR(16) NOT NULL,
			display_name VARCHAR(128),
			avatar_url VARCHAR(512),
			is_direct BOOLEAN,
			event_id VARCHAR(255),
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_room_id (room_id),
			INDEX idx_user_id (user_id),
			INDEX idx_membership (membership),
			UNIQUE INDEX idx_room_user (room_id, user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_user_sessions - 用户会话表
		`CREATE TABLE IF NOT EXISTS matrix_user_sessions (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL,
			access_token VARCHAR(255) NOT NULL,
			refresh_token VARCHAR(255),
			device_id VARCHAR(255) NOT NULL,
			display_name VARCHAR(128),
			last_active_ts BIGINT,
			local_account_id BIGINT UNSIGNED,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			expires_at DATETIME,
			UNIQUE INDEX idx_access_token (access_token),
			INDEX idx_user_id (user_id),
			INDEX idx_device_id (device_id),
			INDEX idx_local_account_id (local_account_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_to_device_messages - To-Device 消息表
		`CREATE TABLE IF NOT EXISTS matrix_to_device_messages (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			sender VARCHAR(255) NOT NULL,
			sender_device VARCHAR(255),
			target_user VARCHAR(255) NOT NULL,
			target_device VARCHAR(255),
			event_type VARCHAR(64) NOT NULL,
			content JSON,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_sender (sender),
			INDEX idx_target (target_user, target_device)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_receipts - 回执表
		`CREATE TABLE IF NOT EXISTS matrix_receipts (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			room_id VARCHAR(255) NOT NULL,
			event_id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			receipt_type VARCHAR(32) NOT NULL,
			ts BIGINT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_room_id (room_id),
			INDEX idx_event_id (event_id),
			INDEX idx_user_id (user_id),
			UNIQUE INDEX idx_receipt (room_id, user_id, receipt_type)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_account_data - 账户数据表
		`CREATE TABLE IF NOT EXISTS matrix_account_data (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL,
			room_id VARCHAR(255),
			data_type VARCHAR(64) NOT NULL,
			content JSON,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_user_id (user_id),
			INDEX idx_room_id (room_id),
			UNIQUE INDEX idx_data (user_id, room_id, data_type)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_typing - 输入状态表
		`CREATE TABLE IF NOT EXISTS matrix_typing (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			room_id VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			timeout BIGINT NOT NULL,
			expires_at DATETIME NOT NULL,
			INDEX idx_room_id (room_id),
			INDEX idx_user_id (user_id),
			INDEX idx_expires_at (expires_at),
			UNIQUE INDEX idx_typing (room_id, user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_filters - 过滤器表
		`CREATE TABLE IF NOT EXISTS matrix_filters (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL,
			filter_id VARCHAR(64) NOT NULL,
			filter TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_user_id (user_id),
			UNIQUE INDEX idx_filter (user_id, filter_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_presence - 在线状态表
		`CREATE TABLE IF NOT EXISTS matrix_presence (
			user_id VARCHAR(255) PRIMARY KEY,
			presence VARCHAR(32) DEFAULT 'offline',
			status_msg VARCHAR(255),
			last_active_ts BIGINT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_device_keys - 设备密钥表（E2EE）
		`CREATE TABLE IF NOT EXISTS matrix_device_keys (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL,
			device_id VARCHAR(255) NOT NULL,
			algorithms JSON,
			device_keys JSON,
			signatures JSON,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE INDEX idx_device (user_id, device_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_one_time_keys - 一次性密钥表（E2EE）
		`CREATE TABLE IF NOT EXISTS matrix_one_time_keys (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			user_id VARCHAR(255) NOT NULL,
			device_id VARCHAR(255) NOT NULL,
			key_id VARCHAR(255) NOT NULL,
			algorithm VARCHAR(64) NOT NULL,
			key_data TEXT NOT NULL,
			used BOOLEAN DEFAULT FALSE,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_device (user_id, device_id),
			UNIQUE INDEX idx_key (user_id, device_id, key_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_cross_signing_keys - 跨设备签名密钥表（E2EE）
		`CREATE TABLE IF NOT EXISTS matrix_cross_signing_keys (
			user_id VARCHAR(255) PRIMARY KEY,
			master_key JSON,
			self_signing_key JSON,
			user_signing_key JSON,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,

		// matrix_room_aliases - 房间别名表
		`CREATE TABLE IF NOT EXISTS matrix_room_aliases (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			room_alias VARCHAR(255) NOT NULL,
			room_id VARCHAR(255) NOT NULL,
			creator VARCHAR(255),
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE INDEX idx_alias (room_alias),
			INDEX idx_room_id (room_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	}
}
