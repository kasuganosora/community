package scripts

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
)

// MigrateGroupTablesScript registers the group/contacts table migration in the migration framework.
// Up is safe: all statements use CREATE TABLE IF NOT EXISTS.
type MigrateGroupTablesScript struct{}

func (MigrateGroupTablesScript) Up(db *gorm.DB) (err error) {
	return MigrateGroupTables()
}

func (MigrateGroupTablesScript) Down(db *gorm.DB) (err error) {
	return db.Exec(`
		DROP TABLE IF EXISTS contact_requests;
		DROP TABLE IF EXISTS contacts;
		DROP TABLE IF EXISTS group_messages;
		DROP TABLE IF EXISTS group_members;
		DROP TABLE IF EXISTS groups;
	`).Error
}

// MigrateGroupTables 群组和联系人表迁移.
func MigrateGroupTables() error {
	log.Info("Running Group tables migration...")

	sql := `
-- 创建 groups 表
CREATE TABLE IF NOT EXISTS groups (
	id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
	owner_id BIGINT UNSIGNED NOT NULL COMMENT '创建者（本地用户ID）',
	name VARCHAR(255) NOT NULL DEFAULT '' COMMENT '群组名称',
	description TEXT DEFAULT '' COMMENT '群组描述',
	avatar VARCHAR(512) DEFAULT '' COMMENT '群组头像URL',
	type TINYINT NOT NULL DEFAULT 0 COMMENT '群组类型: 0=本地 1=桥接 2=Matrix来源',
	visibility VARCHAR(20) NOT NULL DEFAULT 'private' COMMENT '可见性: public/private',
	matrix_room_id VARCHAR(255) DEFAULT NULL COMMENT '关联的Matrix房间ID',
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	deleted_at DATETIME DEFAULT NULL,
	INDEX idx_owner_id (owner_id),
	INDEX idx_type (type),
	INDEX idx_visibility (visibility),
	INDEX idx_matrix_room_id (matrix_room_id),
	INDEX idx_deleted_at (deleted_at),
	FOREIGN KEY (owner_id) REFERENCES accounts(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='群组表';

-- 创建 group_members 表
CREATE TABLE IF NOT EXISTS group_members (
	id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
	group_id BIGINT UNSIGNED NOT NULL COMMENT '群组ID',
	account_id BIGINT UNSIGNED DEFAULT NULL COMMENT '本地用户ID',
	matrix_user_id VARCHAR(255) DEFAULT NULL COMMENT 'Matrix用户ID',
	display_name VARCHAR(255) DEFAULT '' COMMENT '成员显示名称',
	role TINYINT NOT NULL DEFAULT 0 COMMENT '角色: 0=成员 1=管理员 2=所有者',
	invited_by BIGINT UNSIGNED DEFAULT NULL COMMENT '邀请者（本地用户ID）',
	matrix_invited_by VARCHAR(255) DEFAULT NULL COMMENT 'Matrix邀请者ID',
	joined_at DATETIME DEFAULT NULL COMMENT '加入时间',
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	deleted_at DATETIME DEFAULT NULL,
	INDEX idx_group_id (group_id),
	INDEX idx_account_id (account_id),
	INDEX idx_matrix_user_id (matrix_user_id),
	INDEX idx_role (role),
	INDEX idx_deleted_at (deleted_at),
	FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
	FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL,
	FOREIGN KEY (invited_by) REFERENCES accounts(id) ON DELETE SET NULL,
	UNIQUE INDEX idx_group_account (group_id, account_id),
	UNIQUE INDEX idx_group_matrix (group_id, matrix_user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='群组成员表';

-- 创建 group_messages 表
CREATE TABLE IF NOT EXISTS group_messages (
	id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
	group_id BIGINT UNSIGNED NOT NULL COMMENT '群组ID',
	sender_id BIGINT UNSIGNED DEFAULT NULL COMMENT '本地发送者ID',
	matrix_sender_id VARCHAR(255) DEFAULT NULL COMMENT 'Matrix发送者ID',
	content TEXT NOT NULL COMMENT '消息内容',
	matrix_event_id VARCHAR(255) DEFAULT NULL COMMENT 'Matrix事件ID（用于去重）',
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	deleted_at DATETIME DEFAULT NULL,
	INDEX idx_group_id (group_id),
	INDEX idx_sender_id (sender_id),
	INDEX idx_matrix_sender_id (matrix_sender_id),
	INDEX idx_matrix_event_id (matrix_event_id),
	INDEX idx_created_at (created_at),
	INDEX idx_deleted_at (deleted_at),
	FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
	FOREIGN KEY (sender_id) REFERENCES accounts(id) ON DELETE SET NULL,
	UNIQUE INDEX idx_matrix_event (matrix_event_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='群组消息表';

-- 创建 contacts 表
CREATE TABLE IF NOT EXISTS contacts (
	id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
	account_id BIGINT UNSIGNED NOT NULL COMMENT '本地用户ID',
	contact_type TINYINT NOT NULL DEFAULT 0 COMMENT '联系人类型: 0=本地 1=Matrix',
	contact_account_id BIGINT UNSIGNED DEFAULT NULL COMMENT '联系人（本地用户）',
	contact_matrix_id VARCHAR(255) DEFAULT NULL COMMENT '联系人（Matrix用户）',
	display_name VARCHAR(255) DEFAULT '' COMMENT '自定义显示名称',
	status TINYINT NOT NULL DEFAULT 0 COMMENT '状态: 0=等待 1=已接受 2=已屏蔽',
	notes TEXT DEFAULT '' COMMENT '备注',
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	deleted_at DATETIME DEFAULT NULL,
	INDEX idx_account_id (account_id),
	INDEX idx_contact_type (contact_type),
	INDEX idx_contact_account_id (contact_account_id),
	INDEX idx_contact_matrix_id (contact_matrix_id),
	INDEX idx_status (status),
	INDEX idx_deleted_at (deleted_at),
	FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
	FOREIGN KEY (contact_account_id) REFERENCES accounts(id) ON DELETE CASCADE,
	UNIQUE INDEX idx_account_contact (account_id, contact_account_id, contact_matrix_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='联系人表';

-- 创建 contact_requests 表
CREATE TABLE IF NOT EXISTS contact_requests (
	id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
	from_account_id BIGINT UNSIGNED DEFAULT NULL COMMENT '发起者（本地用户）',
	from_matrix_user_id VARCHAR(255) DEFAULT NULL COMMENT '发起者（Matrix用户）',
	to_account_id BIGINT UNSIGNED DEFAULT NULL COMMENT '接收者（本地用户）',
	to_matrix_user_id VARCHAR(255) DEFAULT NULL COMMENT '接收者（Matrix用户）',
	message TEXT DEFAULT '' COMMENT '请求消息',
	status TINYINT NOT NULL DEFAULT 0 COMMENT '状态: 0=等待 1=已接受 2=已屏蔽',
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
	deleted_at DATETIME DEFAULT NULL,
	INDEX idx_from_account_id (from_account_id),
	INDEX idx_from_matrix_user_id (from_matrix_user_id),
	INDEX idx_to_account_id (to_account_id),
	INDEX idx_to_matrix_user_id (to_matrix_user_id),
	INDEX idx_status (status),
	INDEX idx_deleted_at (deleted_at),
	FOREIGN KEY (from_account_id) REFERENCES accounts(id) ON DELETE CASCADE,
	FOREIGN KEY (to_account_id) REFERENCES accounts(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='好友请求表';
`

	err := db.GetDB().Exec(sql).Error
	if err != nil {
		log.Error("Failed to migrate Group tables: %v", err)

		return err
	}

	log.Info("Group tables migration completed successfully")

	return nil
}
