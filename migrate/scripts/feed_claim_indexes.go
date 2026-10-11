package scripts

import (
	"fmt"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// FeedClaimIndexesScript (migration 37) fixes the feed worker claim query plan.
//
// The claim orders by COALESCE(next_attempt_at, created_at), id, which no
// index of migration 36 could serve: PostgreSQL scanned every pending and
// processing row through the state index and sorted them for each poll. The
// UPDATE-heavy queue tables also carried three single-column indexes
// (state, lease_expires_at, next_attempt_at) that are covered by the
// feed_claim composite, so every lease change maintained five indexes.
//
// Up is idempotent: it drops the redundant indexes only if present and creates
// the claim-order index only if missing. PostgreSQL and SQLite get a partial
// expression index limited to unfinished rows; MySQL/MariaDB support neither
// partial indexes (nor expression indexes on MariaDB) and keep feed_claim.
type FeedClaimIndexesScript struct{}

var feedClaimTables = []struct {
	table string
	model any
	// redundant indexes created by migration 36's AutoMigrate.
	redundant []string
}{
	{"feed_events", &models.FeedEvent{}, []string{"idx_feed_events_state", "idx_feed_events_lease_expires_at", "idx_feed_events_next_attempt_at"}},
	{"feed_batches", &models.FeedBatch{}, []string{"idx_feed_batches_state", "idx_feed_batches_lease_expires_at", "idx_feed_batches_next_attempt_at", "idx_feed_batches_event_id"}},
}

func (FeedClaimIndexesScript) Up(conn *gorm.DB) error { return feedClaimIndexesUp(conn) }

func feedClaimIndexesUp(conn *gorm.DB) error {
	if database.IsXmlMode() {
		return nil
	}
	m := conn.Migrator()
	for _, t := range feedClaimTables {
		if !m.HasTable(t.table) {
			continue
		}
		for _, name := range t.redundant {
			if m.HasIndex(t.table, name) {
				if err := m.DropIndex(t.table, name); err != nil {
					return err
				}
			}
		}
		if dialect.IsPostgres(conn) || dialect.IsSQLite(conn) {
			name := "idx_" + t.table + "_claim_order"
			stmt := fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s ((COALESCE(next_attempt_at, created_at)), id) WHERE state IN ('pending','processing')", name, t.table)
			if err := conn.Exec(stmt).Error; err != nil {
				return err
			}
		}
		if dialect.IsPostgres(conn) {
			// Queue tables churn constantly; vacuum/analyze them early.
			stmt := fmt.Sprintf("ALTER TABLE %s SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.02)", t.table)
			if err := conn.Exec(stmt).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (FeedClaimIndexesScript) Down(*gorm.DB) error {
	// Indexes are non-destructive; re-running Up is idempotent.
	return nil
}
