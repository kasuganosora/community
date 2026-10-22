package scripts

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

// SlowQueryIndexesScript adds the indexes from the 2026-10-22 slow-query
// review. Every build is online and idempotent; nothing is dropped or
// rewritten.
//
//   - idx_accounts_lower_username_domain ON accounts (lower(username),
//     lower(domain)): remote WebFinger/acct lookups filter
//     "LOWER(username) = ? AND LOWER(domain) = ?" and full-scanned accounts.
//     PostgreSQL and SQLite only. MySQL is skipped: its columns use a
//     case-insensitive collation and a functional index (8.0.13+) adds a
//     hidden virtual column, which is not worth the risk before cutover.
//   - idx_tags_name_key_pattern ON tags (name_key text_pattern_ops): prefix
//     search "name_key LIKE 'x%'" cannot use the plain unique index under a
//     non-C collation. PostgreSQL only (MySQL/SQLite prefix LIKE already
//     uses uidx_tags_name_key or cannot use any index).
//   - idx_statuses_live_created_id ON statuses (created_at, id) WHERE
//     deleted_at IS NULL: trend/candidate windows order by "created_at DESC,
//     id DESC LIMIT n"; this lets them walk the index backwards and stop
//     early instead of scanning and sorting half the table. MySQL is skipped:
//     no partial indexes, and InnoDB's idx_statuses_created_at already
//     carries the primary key, i.e. is effectively (created_at, id).
//   - idx_status_tags_tag_status ON status_tags (tag_id, status_id): tag ->
//     statuses joins (exact-topic candidates, keyed trends) can be answered
//     from the index alone; the existing unique key is (status_id, tag_id).
//   - idx_matrix_room_members_avatar_url: Matrix media GC checks
//     "avatar_url = ?" for every candidate (matrix/homeserver/media_gc.go),
//     a full scan of matrix_room_members each time.
//   - idx_feed_events_state_updated: the feed_events retention sweep filters
//     "state IN (...) AND updated_at < ?" (feed/worker/worker.go), which no
//     index covered.
//
// PostgreSQL: CREATE INDEX CONCURRENTLY outside a transaction; an INVALID
// leftover from an interrupted build is dropped concurrently and rebuilt.
// MySQL: ALGORITHM=INPLACE, LOCK=NONE.
type SlowQueryIndexesScript struct{}

type onlineIndex struct {
	name, table string
	pg          string // column list / expression (and WHERE) for PostgreSQL
	mysql       string // column list for MySQL; empty = skip
	sqlite      string // column list (and WHERE) for SQLite; empty = skip
}

var slowQueryIndexes = []onlineIndex{
	{name: "idx_accounts_lower_username_domain", table: "accounts",
		pg: "(lower(username), lower(domain))", sqlite: "(lower(username), lower(domain))"},
	{name: "idx_tags_name_key_pattern", table: "tags",
		pg: "(name_key text_pattern_ops)"},
	{name: "idx_statuses_live_created_id", table: "statuses",
		pg: "(created_at, id) WHERE deleted_at IS NULL",
		sqlite: "(created_at, id) WHERE deleted_at IS NULL"},
	{name: "idx_status_tags_tag_status", table: "status_tags",
		pg: "(tag_id, status_id)", mysql: "(tag_id, status_id)", sqlite: "(tag_id, status_id)"},
	{name: "idx_matrix_room_members_avatar_url", table: "matrix_room_members",
		pg: "(avatar_url)", mysql: "(avatar_url)", sqlite: "(avatar_url)"},
	{name: "idx_feed_events_state_updated", table: "feed_events",
		pg: "(state, updated_at)", mysql: "(state, updated_at)", sqlite: "(state, updated_at)"},
}

func (SlowQueryIndexesScript) NonTransactional() bool { return true }

func (SlowQueryIndexesScript) Up(db *gorm.DB) error {
	for _, ix := range slowQueryIndexes {
		if err := createOnlineIndex(db, ix); err != nil {
			return fmt.Errorf("%s: %w", ix.name, err)
		}
	}
	return nil
}

func (SlowQueryIndexesScript) Down(*gorm.DB) error { return nil }

func createOnlineIndex(db *gorm.DB, ix onlineIndex) error {
	m := db.Migrator()
	if !m.HasTable(ix.table) {
		return nil
	}
	switch {
	case dialect.IsPostgres(db):
		if _, inTx := db.Statement.ConnPool.(*sql.Tx); inTx {
			return errors.New("CREATE INDEX CONCURRENTLY cannot run inside a transaction")
		}
		var invalid int64
		if err := db.Raw(`SELECT COUNT(*) FROM pg_index i
			JOIN pg_class c ON c.oid = i.indexrelid
			WHERE c.relname = ? AND c.relnamespace = to_regnamespace(current_schema())::oid AND NOT i.indisvalid`,
			ix.name).Scan(&invalid).Error; err != nil {
			return err
		}
		if invalid > 0 {
			if err := db.Exec("DROP INDEX CONCURRENTLY IF EXISTS " + ix.name).Error; err != nil {
				return err
			}
		}
		return db.Exec("CREATE INDEX CONCURRENTLY IF NOT EXISTS " + ix.name + " ON " + ix.table + " " + ix.pg).Error
	case dialect.IsMySQL(db):
		if ix.mysql == "" || m.HasIndex(ix.table, ix.name) {
			return nil
		}
		return db.Exec("ALTER TABLE " + ix.table + " ADD INDEX " + ix.name + " " + ix.mysql + ", ALGORITHM=INPLACE, LOCK=NONE").Error
	default:
		if ix.sqlite == "" {
			return nil
		}
		return db.Exec("CREATE INDEX IF NOT EXISTS " + ix.name + " ON " + ix.table + " " + ix.sqlite).Error
	}
}
