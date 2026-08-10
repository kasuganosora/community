package db_test

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// setup helpers
// ---------------------------------------------------------------------------

// newInMemoryGormDB opens a GORM DB backed by an in-memory SQLite database.
// This is used to inject a real (but ephemeral) *gorm.DB handle into the
// database package without touching any external service.
func newInMemoryGormDB(t *testing.T) *gorm.DB {
	t.Helper()

	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite via gorm: %v", err)
	}

	// Ensure the underlying *sql.DB is closed when the test ends to avoid leaks.
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	return gdb
}

// ---------------------------------------------------------------------------
// GetDB
// ---------------------------------------------------------------------------

// TestGetDB_NoTestDB returns nil by default in test mode until SetTestDB is
// called. The db package's init() runs database.InitGorm() which is a no-op
// in test mode (isTestMode is auto-detected), so DB stays nil.
func TestGetDB_ReturnsNilBeforeInjection(t *testing.T) {
	// Start from a clean slate.
	database.ResetTestDB()
	t.Cleanup(database.ResetTestDB)

	// In test mode DB has not been initialized, so GetDB returns nil.
	assert.Nil(t, db.GetDB(), "GetDB should return nil before SetTestDB")
}

func TestGetDB_ReturnsInjectedHandle(t *testing.T) {
	database.ResetTestDB()
	t.Cleanup(database.ResetTestDB)

	mock := newInMemoryGormDB(t)
	database.SetTestDB(mock)

	got := db.GetDB()
	assert.NotNil(t, got, "GetDB should return the injected *gorm.DB")
	// The returned handle should be the same pointer we injected.
	assert.Same(t, mock, got)
}

func TestGetDB_DelegatesToDatabasePackage(t *testing.T) {
	// Verify the db facade GetDB() delegates to database.GetDB().
	database.ResetTestDB()
	t.Cleanup(database.ResetTestDB)

	mock := newInMemoryGormDB(t)
	database.SetTestDB(mock)

	// Both should return the same handle.
	assert.Same(t, database.GetDB(), db.GetDB())
}

// ---------------------------------------------------------------------------
// GetRedis
// ---------------------------------------------------------------------------

func TestGetRedis_DelegatesToRedisPackage(t *testing.T) {
	// Save & restore the global redises slice to avoid affecting other tests.
	saved := cRedis.GetRedisesForTest()
	t.Cleanup(func() { cRedis.SetRedisesForTest(saved) })

	// Inject a real (but unused) client so GetRedis(0) does not panic.
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() { _ = client.Close() })

	cRedis.SetRedisesForTest([]*redis.Client{client})

	// No index -> first client.
	got := db.GetRedis()
	assert.NotNil(t, got, "GetRedis() should return the configured client")
	assert.Same(t, client, got)

	// Explicit index 0.
	got0 := db.GetRedis(0)
	assert.Same(t, client, got0)
}

func TestGetRedis_PanicsWhenEmpty(t *testing.T) {
	saved := cRedis.GetRedisesForTest()
	t.Cleanup(func() { cRedis.SetRedisesForTest(saved) })

	cRedis.SetRedisesForTest([]*redis.Client{})

	assert.Panics(t, func() {
		_ = db.GetRedis()
	}, "GetRedis should panic when no redis is configured")
}

func TestGetRedis_PanicsOutOfRange(t *testing.T) {
	saved := cRedis.GetRedisesForTest()
	t.Cleanup(func() { cRedis.SetRedisesForTest(saved) })

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() { _ = client.Close() })

	cRedis.SetRedisesForTest([]*redis.Client{client})

	assert.Panics(t, func() {
		_ = db.GetRedis(5)
	}, "GetRedis should panic for an out-of-range index")
}
