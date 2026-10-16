package scripts

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Regression: the first version passed the column name to CreateIndex and
// failed with "failed to create index with name closed_notified_at".
func TestPollCloseNotifiedScriptOnPreviousSchema(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.Exec(`CREATE TABLE polls (id integer primary key, expires_at datetime)`).Error)
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	require.NoError(t, conn.Exec(`INSERT INTO polls (id, expires_at) VALUES (1, ?), (2, ?), (3, NULL)`, past, future).Error)
	for range 2 { // idempotent
		require.NoError(t, PollCloseNotifiedScript{}.Up(conn))
	}
	require.True(t, conn.Migrator().HasColumn(&pollCloseNotifiedRow{}, "closed_notified_at"))
	require.True(t, conn.Migrator().HasIndex(&pollCloseNotifiedRow{}, pollCloseNotifiedIndex))
	var notified []uint64
	require.NoError(t, conn.Raw(`SELECT id FROM polls WHERE closed_notified_at IS NOT NULL ORDER BY id`).Scan(&notified).Error)
	require.Equal(t, []uint64{1}, notified, "only polls that had already ended are marked")

	// A partial earlier run (column added, index missing) is completed.
	require.NoError(t, conn.Exec(`DROP INDEX idx_polls_closed_notified_at`).Error)
	require.NoError(t, PollCloseNotifiedScript{}.Up(conn))
	require.True(t, conn.Migrator().HasIndex(&pollCloseNotifiedRow{}, pollCloseNotifiedIndex))
}
