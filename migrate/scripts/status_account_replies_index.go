package scripts

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

// NonTransactional is implemented by scripts whose DDL must not run inside
// the migration transaction (PostgreSQL CREATE/DROP INDEX CONCURRENTLY).
// migrate.Up then calls Up on the plain connection; such scripts must be
// idempotent.
type NonTransactional interface {
	NonTransactional() bool
}

// StatusAccountRepliesIndexScript adds idx_statuses_account_reply_id on
// statuses(account_id, in_reply_to_id, id). Profile timelines filter
// "account_id = ? AND in_reply_to_id IS NULL ORDER BY id DESC" (Posts tab,
// exclude_replies); with only single-column indexes, reply-heavy accounts
// were scanned row by row. The equality/IS NULL prefix plus id serves the
// filter and the order on PostgreSQL, MySQL and SQLite. Idempotent.
//
// statuses is large and hot, so the build must not block writes:
//   - PostgreSQL: CREATE INDEX CONCURRENTLY IF NOT EXISTS, outside any
//     transaction. A previous interrupted concurrent build leaves an INVALID
//     index that IF NOT EXISTS would accept; it is dropped (concurrently)
//     and rebuilt first.
//   - MySQL: ALGORITHM=INPLACE, LOCK=NONE (online DDL).
//   - SQLite: plain CREATE INDEX.
type StatusAccountRepliesIndexScript struct{}

const statusAccountRepliesIndex = "idx_statuses_account_reply_id"

type statusAccountRepliesRow struct {
	AccountID   uint64  `gorm:"column:account_id;index:idx_statuses_account_reply_id,priority:1"`
	InReplyToID *uint64 `gorm:"column:in_reply_to_id;index:idx_statuses_account_reply_id,priority:2"`
	ID          uint64  `gorm:"column:id;primaryKey;index:idx_statuses_account_reply_id,priority:3"`
}

func (statusAccountRepliesRow) TableName() string { return "statuses" }

func (StatusAccountRepliesIndexScript) NonTransactional() bool { return true }

func (StatusAccountRepliesIndexScript) Up(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasTable("statuses") {
		return nil
	}
	switch {
	case dialect.IsPostgres(db):
		return createStatusAccountRepliesIndexPG(db)
	case dialect.IsMySQL(db):
		if m.HasIndex(&statusAccountRepliesRow{}, statusAccountRepliesIndex) {
			return nil
		}
		return db.Exec("ALTER TABLE statuses ADD INDEX " + statusAccountRepliesIndex +
			" (account_id, in_reply_to_id, id), ALGORITHM=INPLACE, LOCK=NONE").Error
	default:
		if m.HasIndex(&statusAccountRepliesRow{}, statusAccountRepliesIndex) {
			return nil
		}
		return m.CreateIndex(&statusAccountRepliesRow{}, statusAccountRepliesIndex)
	}
}

func createStatusAccountRepliesIndexPG(db *gorm.DB) error {
	if _, inTx := db.Statement.ConnPool.(*sql.Tx); inTx {
		return errors.New("CREATE INDEX CONCURRENTLY cannot run inside a transaction")
	}
	// Leftover of an interrupted concurrent build (visible to the current
	// search_path only, like the table itself).
	var invalid int64
	if err := db.Raw(`SELECT COUNT(*) FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = ? AND c.relnamespace = to_regnamespace(current_schema())::oid AND NOT i.indisvalid`,
		statusAccountRepliesIndex).Scan(&invalid).Error; err != nil {
		return fmt.Errorf("checking %s validity: %w", statusAccountRepliesIndex, err)
	}
	if invalid > 0 {
		if err := db.Exec("DROP INDEX CONCURRENTLY IF EXISTS " + statusAccountRepliesIndex).Error; err != nil {
			return fmt.Errorf("dropping invalid %s: %w", statusAccountRepliesIndex, err)
		}
	}
	return db.Exec("CREATE INDEX CONCURRENTLY IF NOT EXISTS " + statusAccountRepliesIndex +
		" ON statuses (account_id, in_reply_to_id, id)").Error
}

func (StatusAccountRepliesIndexScript) Down(*gorm.DB) error { return nil }
