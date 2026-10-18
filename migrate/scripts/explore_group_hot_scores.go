package scripts

import (
	"time"

	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

// ExploreGroupHotScoresScript adds matrix_group_hot_scores, the precomputed
// ranking read by the Explore "Groups" tab (GET /api/v1/explore/groups), with
// idx_group_hot_order (score_key, last_post_ts, created_ts) for the ordered
// keyset listing. It also makes sure the background score job only does
// indexed range scans: matrix_events(room_id, origin_server_ts, event_id) and
// matrix_room_members(room_id, membership). Existing indexes are kept.
//
// Index builds on big tables do not block writes: PostgreSQL uses
// CREATE INDEX CONCURRENTLY outside a transaction (hence NonTransactional),
// MySQL uses online DDL, SQLite a plain CREATE INDEX. Idempotent. Down drops
// only the new table and the members index (the events index predates it).
type ExploreGroupHotScoresScript struct{}

type exploreGroupHotScoreRow struct {
	RoomID      string    `gorm:"primaryKey;type:varchar(255)"`
	ScoreKey    int64     `gorm:"not null;default:0;index:idx_group_hot_order,priority:1"`
	LastPostTS  int64     `gorm:"not null;default:0;index:idx_group_hot_order,priority:2"`
	CreatedTS   int64     `gorm:"not null;default:0;index:idx_group_hot_order,priority:3"`
	Server      string    `gorm:"type:varchar(255);not null;default:'';index"`
	IsLocal     bool      `gorm:"not null;default:false"`
	MemberCount int64     `gorm:"not null;default:0"`
	ComputedAt  time.Time `gorm:"not null;index"`
}

func (exploreGroupHotScoreRow) TableName() string { return "matrix_group_hot_scores" }

const exploreMembersIndex = "idx_matrix_room_members_room_membership"

func (ExploreGroupHotScoresScript) NonTransactional() bool { return true }

func exploreCreateIndex(db *gorm.DB, table, name, cols string) error {
	if !db.Migrator().HasTable(table) || db.Migrator().HasIndex(table, name) {
		return nil
	}
	switch {
	case dialect.IsPostgres(db):
		// A failed concurrent build leaves an INVALID index; rebuild it.
		var invalid int64
		db.Raw("SELECT COUNT(*) FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE c.relname = ? AND NOT i.indisvalid", name).Scan(&invalid)
		if invalid > 0 {
			if err := db.Exec("DROP INDEX CONCURRENTLY IF EXISTS " + name).Error; err != nil {
				return err
			}
		}
		return db.Exec("CREATE INDEX CONCURRENTLY IF NOT EXISTS " + name + " ON " + table + " (" + cols + ")").Error
	case dialect.IsMySQL(db):
		return db.Exec("ALTER TABLE " + table + " ADD INDEX " + name + " (" + cols + "), ALGORITHM=INPLACE, LOCK=NONE").Error
	default:
		return db.Exec("CREATE INDEX IF NOT EXISTS " + name + " ON " + table + " (" + cols + ")").Error
	}
}

func (ExploreGroupHotScoresScript) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&exploreGroupHotScoreRow{}); err != nil {
		return err
	}
	if err := exploreCreateIndex(db, "matrix_events", "idx_matrix_events_room_ts", "room_id, origin_server_ts, event_id"); err != nil {
		return err
	}
	return exploreCreateIndex(db, "matrix_room_members", exploreMembersIndex, "room_id, membership")
}

func (ExploreGroupHotScoresScript) Down(db *gorm.DB) error {
	if db.Migrator().HasIndex("matrix_room_members", exploreMembersIndex) {
		if err := db.Migrator().DropIndex("matrix_room_members", exploreMembersIndex); err != nil {
			return err
		}
	}
	return db.Migrator().DropTable("matrix_group_hot_scores")
}
