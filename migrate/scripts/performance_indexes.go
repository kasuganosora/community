package scripts

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// CreatePerformanceIndexes 创建性能优化索引
func CreatePerformanceIndexes() error {
	log.Info("Creating performance indexes...")

	sql := `
	-- 账户表索引
	CREATE INDEX IF NOT EXISTS idx_accounts_created_at ON accounts(created_at);
	CREATE INDEX IF NOT EXISTS idx_accounts_last_status_at ON accounts(last_status_at);
	CREATE INDEX IF NOT EXISTS idx_accounts_is_ban ON accounts(is_ban);
	CREATE INDEX IF NOT EXISTS idx_accounts_locked ON accounts(locked);

	-- 嘟文表索引
	CREATE INDEX IF NOT EXISTS idx_statuses_account_id ON statuses(account_id);
	CREATE INDEX IF NOT EXISTS idx_statuses_created_at ON statuses(created_at);
	CREATE INDEX IF NOT EXISTS idx_statuses_visibility ON statuses(visibility);
	CREATE INDEX IF NOT EXISTS idx_statuses_in_reply_to_id ON statuses(in_reply_to_id);
	CREATE INDEX IF NOT EXISTS idx_statuses_reblog_id ON statuses(reblog_id);
	CREATE INDEX IF NOT EXISTS idx_statuses_scheduled_at ON statuses(scheduled_at);

	-- 嘟文内容表索引
	CREATE INDEX IF NOT EXISTS idx_status_contents_status_id ON status_contents(status_id);

	-- 媒体附件表索引
	CREATE INDEX IF NOT EXISTS idx_media_attachments_account_id ON media_attachments(account_id);
	CREATE INDEX IF NOT EXISTS idx_media_attachments_status_id ON media_attachments(status_id);
	CREATE INDEX IF NOT EXISTS idx_media_attachments_created_at ON media_attachments(created_at);

	-- 关注表索引
	CREATE INDEX IF NOT EXISTS idx_follows_account_id ON follows(account_id);
	CREATE INDEX IF NOT EXISTS idx_follows_follow_id ON follows(follow_id);
	CREATE INDEX IF NOT EXISTS idx_follows_created_at ON follows(created_at);

	-- 通知表索引
	CREATE INDEX IF NOT EXISTS idx_notifications_account_id ON notifications(account_id);
	CREATE INDEX IF NOT EXISTS idx_notifications_target_account_id ON notifications(target_account_id);
	CREATE INDEX IF NOT EXISTS idx_notifications_created_at ON notifications(created_at);
	CREATE INDEX IF NOT EXISTS idx_notifications_type ON notifications(type);

	-- 会话表索引
	CREATE INDEX IF NOT EXISTS idx_conversations_account_id ON conversations(account_id);
	CREATE INDEX IF NOT EXISTS idx_conversations_last_status_at ON conversations(last_status_at);

	-- 报告表索引
	CREATE INDEX IF NOT EXISTS idx_reports_account_id ON reports(account_id);
	CREATE INDEX IF NOT EXISTS idx_reports_target_account_id ON reports(target_account_id);
	CREATE INDEX IF NOT EXISTS idx_reports_action_taken ON reports(action_taken);
	CREATE INDEX IF NOT EXISTS idx_reports_created_at ON reports(created_at);

	-- 标签表索引
	CREATE INDEX IF NOT EXISTS idx_tags_name ON tags(name);
	CREATE INDEX IF NOT EXISTS idx_tags_status_count ON tags(status_count);

	-- 屏蔽/静音表索引
	CREATE INDEX IF NOT EXISTS idx_blocks_account_id ON blocks(account_id);
	CREATE INDEX IF NOT EXISTS idx_blocks_target_id ON blocks(target_id);
	CREATE INDEX IF NOT EXISTS idx_mutes_account_id ON mutes(account_id);
	CREATE INDEX IF NOT EXISTS idx_mutes_target_id ON mutes(target_id);
	CREATE INDEX IF NOT EXISTS idx_domain_blocks_account_id ON domain_blocks(account_id);

	-- 收藏/书签表索引
	CREATE INDEX IF NOT EXISTS idx_favourites_account_id ON favourites(account_id);
	CREATE INDEX IF NOT EXISTS idx_favourites_status_id ON favourites(status_id);
	CREATE INDEX IF NOT EXISTS idx_bookmarks_account_id ON bookmarks(account_id);
	CREATE INDEX IF NOT EXISTS idx_bookmarks_status_id ON bookmarks(status_id);

	-- 关注请求表索引
	CREATE INDEX IF NOT EXISTS idx_follow_requests_account_id ON follow_requests(account_id);
	CREATE INDEX IF NOT EXISTS idx_follow_requests_target_id ON follow_requests(target_id);

	-- 过滤器表索引
	CREATE INDEX IF NOT EXISTS idx_filters_account_id ON filters(account_id);
	CREATE INDEX IF NOT EXISTS idx_filters_expires_at ON filters(expires_at);

	-- 列表表索引
	CREATE INDEX IF NOT EXISTS idx_lists_account_id ON lists(account_id);
	CREATE INDEX IF NOT EXISTS idx_list_accounts_list_id ON list_accounts(list_id);
	CREATE INDEX IF NOT EXISTS idx_list_accounts_account_id ON list_accounts(account_id);

	-- 标记表索引
	CREATE INDEX IF NOT EXISTS idx_markers_account_id ON markers(account_id);
	CREATE INDEX IF NOT EXISTS idx_markers_timeline ON markers(timeline);

	-- 认证令牌表索引
	CREATE INDEX IF NOT EXISTS idx_auth_tokens_user_id ON auth_tokens(user_id);
	CREATE INDEX IF NOT EXISTS idx_auth_tokens_token ON auth_tokens(token);
	CREATE INDEX IF NOT EXISTS idx_auth_tokens_expires_at ON auth_tokens(expires_at);

	-- OAuth 应用表索引
	CREATE INDEX IF NOT EXISTS idx_oauth_applications_uid ON oauth_applications(uid);
	CREATE INDEX IF NOT EXISTS idx_oauth_applications_owner_id ON oauth_applications(owner_id);

	-- 表情表索引
	CREATE INDEX IF NOT EXISTS idx_emojis_shortcode ON emojis(shortcode);
	CREATE INDEX IF NOT EXISTS idx_emojis_domain ON emojis(domain);
	CREATE INDEX IF NOT EXISTS idx_emojis_visible_in_picker ON emojis(visible_in_picker);

	-- 公告表索引
	CREATE INDEX IF NOT EXISTS idx_announcements_published ON announcements(published);
	CREATE INDEX IF NOT EXISTS idx_announcements_starts_at ON announcements(starts_at);
	CREATE INDEX IF NOT EXISTS idx_announcements_ends_at ON announcements(ends_at);

	-- 推送订阅表索引
	CREATE INDEX IF NOT EXISTS idx_push_subscriptions_account_id ON push_subscriptions(account_id);
	`

	if err := db.GetDB().Exec(sql).Error; err != nil {
		log.Error("Failed to create performance indexes: %v", err)
		return err
	}

	log.Info("Performance indexes created successfully")
	return nil
}

// OptimizeDatabase 优化数据库
func OptimizeDatabase() error {
	log.Info("Optimizing database...")

	// 分析表以更新统计信息
	tables := []string{
		"accounts",
		"statuses",
		"status_contents",
		"media_attachments",
		"follows",
		"notifications",
		"favourites",
		"bookmarks",
		"blocks",
		"mutes",
		"tags",
		"reports",
	}

	for _, table := range tables {
		// 分析表
		if err := db.GetDB().Exec("ANALYZE TABLE " + table).Error; err != nil {
			log.Warn("Failed to analyze table %s: %v", table, err)
		}
	}

	log.Info("Database optimized successfully")
	return nil
}

// RunPerformanceMigrations 运行性能优化迁移
func RunPerformanceMigrations() error {
	if err := CreatePerformanceIndexes(); err != nil {
		return err
	}
	if err := OptimizeDatabase(); err != nil {
		return err
	}
	return nil
}
