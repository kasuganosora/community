package list

import (
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// allListModels is the full set of models that GetStatusTimelineContent
// preloads (directly or transitively), plus the list-specific tables.
// AutoMigrating all of them lets us run end-to-end timeline reads against
// an in-memory SQLite database.
func allListModels() []any {
	return []any{
		&models.User{},
		&models.UserSetting{},
		&models.UserSecretData{},
		&models.Account{},
		&models.AccountExtra{},
		&models.Block{},
		&models.MuteAccount{},
		&models.List{},
		&models.ListAccount{},
		&models.ListFollowAccount{},
		&models.Status{},
		&models.StatusContent{},
		&models.StatusToAccount{},
		&models.Application{},
		&models.Attachment{},
		&models.Emoji{},
		&models.Poll{},
		&models.PollOption{},
		&models.Tag{},
		&models.StatusTag{},
		&models.ListHashtag{},
	}
}

// newIsolatedGormDB opens a fresh in-memory SQLite database with a single
// pooled connection (so ":memory:" always refers to the same private
// database for every GORM call made through this handle).
//
// The handle is NOT closed automatically: the list package's init() registers
// an event handler that spawns best-effort goroutines (`go Push(...)` /
// `go Unpush(...)`). Those goroutines call db.GetDB(); if we closed this
// handle while such a goroutine is in flight it would crash against a closed
// connection. Since each test gets its own isolated ":memory:" database,
// leaving the handle open does not cause cross-test data bleed. The
// connections are reclaimed when the test binary exits.
func newIsolatedGormDB(t *testing.T) *gorm.DB {
	t.Helper()

	gdb, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err, "failed to open in-memory sqlite")

	return migrateListDB(t, gdb)
}

func migrateListDB(t *testing.T, gdb *gorm.DB) *gorm.DB {
	t.Helper()

	if sqlDB, dbErr := gdb.DB(); dbErr == nil {
		// A single pooled connection guarantees that every statement runs
		// against the same private ":memory:" database. SQLite also
		// serialises writes, so this keeps things consistent for the
		// goroutines spawned by the event handler.
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	}

	for _, model := range allListModels() {
		stmt := &gorm.Statement{DB: gdb}
		require.NoError(t, stmt.Parse(model))
		for _, field := range stmt.Schema.Fields {
			if field.AutoIncrement {
				field.DataType = schema.Int
			}
		}
	}
	require.NoError(t, gdb.AutoMigrate(allListModels()...), "failed to auto-migrate models")

	return gdb
}

// sharedGormDB is a long-lived in-memory database used by the event-handler
// tests. Those tests fire `event.Trigger`, which the package's init() handler
// answers by spawning goroutines that call db.GetDB(). Those goroutines may
// outlive the test that launched them, so they need a stable handle that is
// never closed or swapped out from under them. A single shared, never-closed
// handle satisfies that. It is migrated lazily so the cost is paid only by
// tests that actually need it.
var (
	sharedGormDB *gorm.DB
	sharedDBMu   sync.Mutex
)

// getSharedGormDB returns the long-lived shared database, migrating it on
// first use. Tests that touch the event system use this so background
// goroutines always find a valid handle.
func getSharedGormDB(t *testing.T) *gorm.DB {
	t.Helper()

	sharedDBMu.Lock()
	defer sharedDBMu.Unlock()

	if sharedGormDB != nil {
		return sharedGormDB
	}

	if pgtest.Enabled() {
		// Outlives the first test (event handler goroutines keep using it),
		// so it must not be tied to that test's cleanup.
		gdb, err := pgtest.OpenShared("t_feed_list_shared", &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
		require.NoError(t, err)
		sharedGormDB = migrateListDB(t, gdb)
		return sharedGormDB
	}

	sharedGormDB = newIsolatedGormDB(t)
	return sharedGormDB
}

// setupDBOnly injects a fresh in-memory SQLite DB into the global database
// handle, plus a mock feed service. It is used by tests that exercise
// Push/Unpush/Rebuild synchronously (no background goroutines).
func setupDBOnly(t *testing.T) *gorm.DB {
	t.Helper()

	gdb := newIsolatedGormDB(t)
	database.SetTestDB(gdb)

	// Use the in-process mock Redis for feed operations.
	feed.SetDefaultService(feed.NewFeedService(feed.NewMockRedisClient()))

	return gdb
}

// setupFull wires up a fresh in-memory SQLite DB *and* an in-process miniredis,
// so GetStatusTimelineContent (which talks to db.GetRedis() directly) can be
// exercised end-to-end. Returns the gorm.DB handle for fixture creation and
// the miniredis handle for assertions.
func setupFull(t *testing.T) (*gorm.DB, *miniredis.Miniredis) {
	t.Helper()

	gdb := newIsolatedGormDB(t)
	database.SetTestDB(gdb)

	// Spin up miniredis and inject it both into the global redis pool
	// (for db.GetRedis() used by GetStatusTimelineContent) and into a
	// FeedService used by Push/Unpush/Rebuild.
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	savedRedises := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{client})
	t.Cleanup(func() { cRedis.SetRedisesForTest(savedRedises) })

	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(client)))

	return gdb, mr
}

// setupSharedDB injects the long-lived shared database. Use this for tests
// that trigger the package's event handler (which spawns background
// goroutines that outlive a per-test handle).
func setupSharedDB(t *testing.T) *gorm.DB {
	t.Helper()

	gdb := getSharedGormDB(t)
	database.SetTestDB(gdb)

	feed.SetDefaultService(feed.NewFeedService(feed.NewMockRedisClient()))

	return gdb
}

// createAccount is a tiny fixture helper that persists a user + account pair.
// A unique username (suffixed with a high-resolution timestamp) keeps rows
// distinct even in the shared database used by event tests; the username
// column has a UNIQUE constraint.
func createAccount(t *testing.T, gdb *gorm.DB, username string) *models.Account {
	t.Helper()

	now := time.Now()
	suffix := now.Format("150405.000000000")
	fullName := username + "_" + suffix

	user := &models.User{
		UUID:           fullName + "-uuid",
		UserName:       fullName,
		EmailHash:      "hash_" + fullName,
		CreatedAt:      &now,
		UpdatedAt:      &now,
		Status:         models.UserStatusNormal,
		IsMailVerified: true,
	}
	require.NoError(t, gdb.Create(user).Error)

	account := &models.Account{
		UserId:      user.Id,
		Username:    fullName,
		DisplayName: username + " display",
		Acct:        fullName,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	require.NoError(t, gdb.Create(account).Error)

	return account
}

// createStatus persists a public status for the given account.
func createStatus(t *testing.T, gdb *gorm.DB, account *models.Account, overrides ...func(*models.Status)) *models.Status {
	t.Helper()

	now := time.Now()
	status := &models.Status{
		AccountId:  account.Id,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}

	for _, o := range overrides {
		o(status)
	}

	require.NoError(t, gdb.Create(status).Error)

	return status
}

// createList persists a list owned by account, viewed recently so Push will
// pick it up (Push only targets lists viewed in the last 30 days).
func createList(t *testing.T, gdb *gorm.DB, owner *models.Account, title string) *models.List {
	t.Helper()

	now := time.Now()
	list := &models.List{
		Title:        title,
		AccountId:    owner.Id,
		LastViewedAt: &now,
		CreatedAt:    &now,
		UpdatedAt:    &now,
	}
	require.NoError(t, gdb.Create(list).Error)

	return list
}

// addListMember links account to list via ListAccount.
func addListMember(t *testing.T, gdb *gorm.DB, list *models.List, account *models.Account) *models.ListAccount {
	t.Helper()

	now := time.Now()
	la := &models.ListAccount{
		ListId:    list.Id,
		AccountId: account.Id,
		CreatedAt: &now,
	}
	require.NoError(t, gdb.Create(la).Error)

	return la
}
