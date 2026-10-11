package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFeedClaimIndexesIdempotentOnSQLiteAndPostgres(t *testing.T) {
	dbs := map[string]*gorm.DB{}
	lite, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	dbs["sqlite"] = lite
	if pgtest.Enabled() {
		dbs["postgres"] = pgtest.Open(t, &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	}
	for name, conn := range dbs {
		t.Run(name, func(t *testing.T) {
			sql, _ := conn.DB()
			sql.SetMaxOpenConns(1)
			// A production database as migration 36 left it: redundant single-column indexes.
			require.NoError(t, conn.AutoMigrate(&models.FeedEvent{}, &models.FeedBatch{}))
			m := conn.Migrator()
			for table, names := range map[string][]string{
				"feed_events":  {"idx_feed_events_state", "idx_feed_events_lease_expires_at", "idx_feed_events_next_attempt_at"},
				"feed_batches": {"idx_feed_batches_state", "idx_feed_batches_lease_expires_at", "idx_feed_batches_next_attempt_at", "idx_feed_batches_event_id"},
			} {
				for _, idx := range names {
					if !m.HasIndex(table, idx) {
						require.NoError(t, conn.Exec("CREATE INDEX "+idx+" ON "+table+" ("+"state"+")").Error)
					}
				}
			}
			for i := 0; i < 3; i++ { // idempotent: re-running is a no-op
				require.NoError(t, FeedClaimIndexesScript{}.Up(conn))
			}
			for table, names := range map[string][]string{
				"feed_events":  {"idx_feed_events_state", "idx_feed_events_lease_expires_at", "idx_feed_events_next_attempt_at"},
				"feed_batches": {"idx_feed_batches_state", "idx_feed_batches_lease_expires_at", "idx_feed_batches_next_attempt_at", "idx_feed_batches_event_id"},
			} {
				for _, idx := range names {
					require.False(t, m.HasIndex(table, idx), idx)
				}
				require.True(t, m.HasIndex(table, "idx_"+table+"_feed_claim"), "composite state index is kept")
				if dialect.IsPostgres(conn) || dialect.IsSQLite(conn) {
					require.True(t, m.HasIndex(table, "idx_"+table+"_claim_order"))
				}
			}
			// Fresh installs (new model tags) must not recreate the dropped indexes.
			require.NoError(t, conn.AutoMigrate(&models.FeedEvent{}, &models.FeedBatch{}))
			require.False(t, m.HasIndex("feed_events", "idx_feed_events_state"))
		})
	}
}
