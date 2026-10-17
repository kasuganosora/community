package scripts

import "gorm.io/gorm"

// StatusAccountRepliesIndexScript adds idx_statuses_account_reply_id on
// statuses(account_id, in_reply_to_id, id). Profile timelines filter
// "account_id = ? AND in_reply_to_id IS NULL ORDER BY id DESC" (Posts tab,
// exclude_replies); with only single-column indexes, reply-heavy accounts
// were scanned row by row. The equality/IS NULL prefix plus id serves the
// filter and the order on PostgreSQL, MySQL and SQLite. Idempotent.
type StatusAccountRepliesIndexScript struct{}

const statusAccountRepliesIndex = "idx_statuses_account_reply_id"

type statusAccountRepliesRow struct {
	AccountID   uint64  `gorm:"column:account_id;index:idx_statuses_account_reply_id,priority:1"`
	InReplyToID *uint64 `gorm:"column:in_reply_to_id;index:idx_statuses_account_reply_id,priority:2"`
	ID          uint64  `gorm:"column:id;primaryKey;index:idx_statuses_account_reply_id,priority:3"`
}

func (statusAccountRepliesRow) TableName() string { return "statuses" }

func (StatusAccountRepliesIndexScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("statuses") || m.HasIndex(&statusAccountRepliesRow{}, statusAccountRepliesIndex) {
		return nil
	}
	return m.CreateIndex(&statusAccountRepliesRow{}, statusAccountRepliesIndex)
}

func (StatusAccountRepliesIndexScript) Down(*gorm.DB) error { return nil }
