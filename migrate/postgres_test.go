package migrate

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasuganosora/community/migrate/scripts"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestUp_PostgresFreshDatabase runs every registered migration on an empty
// PostgreSQL schema, then again to prove re-runs are no-ops. It only runs
// when TEST_DATABASE_URL points at PostgreSQL (see tests/testutil/postgres.go).
func TestUp_PostgresFreshDatabase(t *testing.T) {
	if !testutil.UsePostgres() {
		t.Skip("set TEST_DATABASE_URL to a PostgreSQL database to run")
	}

	conn := testutil.OpenPostgresTestDB(t, nil)

	require.NoError(t, Up(conn))
	require.Equal(t, int64(len(scripts.GetScriptsName())), countMigrations(t, conn))

	require.NoError(t, Up(conn))
	require.Equal(t, int64(len(scripts.GetScriptsName())), countMigrations(t, conn))

	lastBatch, err := getLastBatchNum(conn)
	require.NoError(t, err)
	require.Equal(t, 1, lastBatch)

	// Re-running the AutoMigrate based scripts on the finished schema must
	// not alter any column: the translated MySQL column types have to compare
	// equal to what PostgreSQL reports, or every start would ALTER tables.
	// (CreateInitTables re-creates the dropped legacy join table; that CREATE
	// is expected and not recorded.)
	rec := &ddlRecorder{}
	recording := conn.Session(&gorm.Session{Logger: rec})
	for _, script := range []scripts.MigrationScript{
		scripts.CreateInitTables{},
		scripts.CreateActivityPubTablesScript{},
		scripts.CreateMissingModelTablesScript{},
		scripts.OAuthGrantHardeningScript{},
		scripts.StatusReactionsScript{},
		scripts.FollowedTagsScript{},
		scripts.AddDeliveryAuthorizationScript{},
	} {
		require.NoError(t, script.Up(recording))
	}
	require.Empty(t, rec.ddl, "AutoMigrate should be a no-op on a migrated PostgreSQL schema")

	// Identity columns compare case-insensitively, as under MySQL's *_ci
	// collation: one user per username regardless of case.
	require.NoError(t, conn.Exec(`INSERT INTO users (uuid, username, email_hash) VALUES ('u1', 'Alice', 'h1')`).Error)
	require.Error(t, conn.Exec(`INSERT INTO users (uuid, username, email_hash) VALUES ('u2', 'alice', 'h2')`).Error)
	var n int64
	require.NoError(t, conn.Table("users").Where("username = ?", "ALICE").Count(&n).Error)
	require.EqualValues(t, 1, n)
}

type ddlRecorder struct {
	mu  sync.Mutex
	ddl []string // ALTER statements seen
}

func (r *ddlRecorder) LogMode(logger.LogLevel) logger.Interface { return r }
func (r *ddlRecorder) Info(context.Context, string, ...any)     {}
func (r *ddlRecorder) Warn(context.Context, string, ...any)     {}
func (r *ddlRecorder) Error(context.Context, string, ...any)    {}
func (r *ddlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if strings.HasPrefix(upper, "ALTER ") {
		r.mu.Lock()
		r.ddl = append(r.ddl, sql)
		r.mu.Unlock()
	}
}
