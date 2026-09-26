package scripts

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
)

// MigrateMediaTablesScript registers media table migration in the migration framework.
// Up is safe: all statements use IF NOT EXISTS (CREATE INDEX IF NOT EXISTS, ADD COLUMN IF NOT EXISTS).
// Note: Down is a no-op because indexes and columns are additive changes;
// dropping them could break existing functionality.
type MigrateMediaTablesScript struct{}

func (MigrateMediaTablesScript) Up(db *gorm.DB) (err error) {
	// 注意：必须使用传入的 db（可能是事务），而非全局 db.GetDB()，
	// 否则 migrate.Up() 中的事务包裹将无法回滚。
	log.Info("Running media tables migration...")

	// XML (sqlexec) 模式不支持 ALTER TABLE（添加列）以及 MySQL 特有的
	// FULLTEXT INDEX 语法，跳过此 MySQL 特定脚本。
	if database.IsXmlMode() {
		log.Debug("Skip media tables migration in XML mode")
		return nil
	}

	err = applyPortableDDL(db, mediaTablesSQL)
	if err != nil {
		log.Error("Failed to run media tables migration: %v", err)

		return err
	}

	log.Info("Media tables migration completed successfully")

	return nil
}

func (MigrateMediaTablesScript) Down(db *gorm.DB) (err error) {
	// No-op: media indexes and added columns are additive/non-destructive changes.
	// Dropping them is fragile and could break application functionality.
	return nil
}

// MigrateMediaTables 媒体表迁移（使用全局 DB 句柄，供非迁移框架场景调用）.
func MigrateMediaTables() error {
	log.Info("Running media tables migration...")

	// XML (sqlexec) 模式不支持 ALTER TABLE 以及 MySQL 特有的 FULLTEXT INDEX 语法。
	if database.IsXmlMode() {
		log.Debug("Skip media tables migration in XML mode")
		return nil
	}

	err := applyPortableDDL(db.GetDB(), mediaTablesSQL)
	if err != nil {
		log.Error("Failed to run media tables migration: %v", err)

		return err
	}

	log.Info("Media tables migration completed successfully")

	return nil
}

// mediaTablesSQL is the shared SQL for media/index migration.
// All statements use IF NOT EXISTS for idempotency.
const mediaTablesSQL = `
	-- 为 media_attachments 表添加索引
	CREATE INDEX IF NOT EXISTS idx_media_attachments_account_id ON media_attachments(account_id);
	CREATE INDEX IF NOT EXISTS idx_media_attachments_status_id ON media_attachments(status_id);
	CREATE INDEX IF NOT EXISTS idx_media_attachments_created_at ON media_attachments(created_at);
	CREATE INDEX IF NOT EXISTS idx_media_attachments_type ON media_attachments(type);

	-- 添加 blurhash 列（如果不存在）
	ALTER TABLE media_attachments ADD COLUMN IF NOT EXISTS blurhash VARCHAR(50);

	-- 添加 focus_x 和 focus_y 列（如果不存在）
	ALTER TABLE media_attachments ADD COLUMN IF NOT EXISTS focus_x DOUBLE DEFAULT 0;
	ALTER TABLE media_attachments ADD COLUMN IF NOT EXISTS focus_y DOUBLE DEFAULT 0;

	-- 为 accounts 表添加全文索引（用于搜索）
	ALTER TABLE accounts ADD FULLTEXT INDEX IF NOT EXISTS idx_accounts_search (username, display_name, bio);

	-- 为 status_contents 表添加全文索引（用于搜索）
	ALTER TABLE status_contents ADD FULLTEXT INDEX IF NOT EXISTS idx_status_content_search (content, spoiler_text);

	-- 为 tags 表添加索引
	CREATE INDEX IF NOT EXISTS idx_tags_name ON tags(name);
	CREATE INDEX IF NOT EXISTS idx_tags_status_count ON tags(status_count);

	-- 为 follows 表优化索引
	CREATE INDEX IF NOT EXISTS idx_follows_account_follow ON follows(account_id, follow_id);
	CREATE INDEX IF NOT EXISTS idx_follows_follow_account ON follows(follow_id, account_id);

	-- 为 favourites 表添加索引
	CREATE INDEX IF NOT EXISTS idx_favourites_account_id ON favourites(account_id);
	CREATE INDEX IF NOT EXISTS idx_favourites_status_id ON favourites(status_id);
	CREATE INDEX IF NOT EXISTS idx_favourites_created_at ON favourites(created_at);

	-- 为 bookmarks 表添加索引
	CREATE INDEX IF NOT EXISTS idx_bookmarks_account_id ON bookmarks(account_id);
	CREATE INDEX IF NOT EXISTS idx_bookmarks_status_id ON bookmarks(status_id);
	CREATE INDEX IF NOT EXISTS idx_bookmarks_created_at ON bookmarks(created_at);

	-- 为 blocks 表添加索引
	CREATE INDEX IF NOT EXISTS idx_blocks_account_id ON blocks(account_id);
	CREATE INDEX IF NOT EXISTS idx_blocks_target_id ON blocks(target_id);

	-- 为 mutes 表添加索引
	CREATE INDEX IF NOT EXISTS idx_mutes_account_id ON mutes(account_id);
	CREATE INDEX IF NOT EXISTS idx_mutes_target_id ON mutes(target_id);

	-- 为 domain_blocks 表添加索引
	CREATE INDEX IF NOT EXISTS idx_domain_blocks_account_id ON domain_blocks(account_id);
	CREATE INDEX IF NOT EXISTS idx_domain_blocks_domain ON domain_blocks(domain);

	-- 为 featured_tags 表添加索引
	CREATE INDEX IF NOT EXISTS idx_featured_tags_account_id ON featured_tags(account_id);
	CREATE INDEX IF NOT EXISTS idx_featured_tags_tag_id ON featured_tags(tag_id);

	-- 为 announcements 表添加索引
	CREATE INDEX IF NOT EXISTS idx_announcements_published ON announcements(published);
	CREATE INDEX IF NOT EXISTS idx_announcements_starts_at ON announcements(starts_at);
	CREATE INDEX IF NOT EXISTS idx_announcements_ends_at ON announcements(ends_at);

	-- 为 push_subscriptions 表添加索引
	CREATE INDEX IF NOT EXISTS idx_push_subscriptions_account_id ON push_subscriptions(account_id);
	`

// CreateSearchIndexes 创建搜索相关索引.
func CreateSearchIndexes() error {
	log.Info("Creating search indexes...")

	// XML (sqlexec) 模式不支持 CREATE INDEX ... DESC 等 MySQL/PG 特定语法。
	if database.IsXmlMode() {
		log.Debug("Skip CreateSearchIndexes in XML mode")
		return nil
	}

	// 这些索引主要是为 Meilisearch 准备的数据查询优化
	sql := `
	-- 账户搜索优化索引
	CREATE INDEX IF NOT EXISTS idx_accounts_username ON accounts(username);
	CREATE INDEX IF NOT EXISTS idx_accounts_domain ON accounts(domain);
	CREATE INDEX IF NOT EXISTS idx_accounts_visibility ON accounts(visibility);

	-- 嘟文搜索优化索引
	CREATE INDEX IF NOT EXISTS idx_statuses_account_visibility ON statuses(account_id, visibility);
	CREATE INDEX IF NOT EXISTS idx_statuses_created_at_desc ON statuses(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_statuses_scheduled_at ON statuses(scheduled_at);
	`

	err := applyPortableDDL(db.GetDB(), sql)
	if err != nil {
		log.Error("Failed to create search indexes: %v", err)

		return err
	}

	log.Info("Search indexes created successfully")

	return nil
}

// OptimizeTables 优化数据库表.
// 注意：OPTIMIZE TABLE 是 MySQL 特定命令，在 SQLite/XML 模式下跳过.
func OptimizeTables() error {
	if database.IsXmlMode() {
		log.Debug("Skip OptimizeTables in XML mode")

		return nil
	}

	log.Info("Optimizing database tables...")

	tables := []string{
		"accounts",
		"statuses",
		"status_contents",
		"media_attachments",
		"follows",
		"favourites",
		"bookmarks",
		"blocks",
		"mutes",
		"tags",
		"notifications",
	}

	// 表名来自硬编码常量列表（非用户输入），不存在 SQL 注入风险。
	for _, table := range tables {
		sql := "OPTIMIZE TABLE `" + table + "`"
		err := db.GetDB().Exec(sql).Error
		if err != nil {
			log.Warn("Failed to optimize table %s: %v", table, err)
		}
	}

	log.Info("Database tables optimized successfully")

	return nil
}

// RunAllMigrations 运行所有迁移.
func RunAllMigrations() error {
	err := MigrateMediaTables()
	if err != nil {
		return err
	}

	err = CreateSearchIndexes()

	if err != nil {
		return err
	}

	return nil
}
