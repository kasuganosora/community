package testutil

import (
	"testing"

	cRedis "github.com/kasuganosora/community/db/redis"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewTestSetup_RestoresGlobalRedis verifies that NewTestSetup mutates the
// global redis client pool (db/redis.redises) only for the duration of the test
// and restores the previous value afterwards. Without this, a test that injects
// a (later closed) mock client would leave GetRedis() pointing at a dead client
// for every subsequent test in the process.
func TestNewTestSetup_RestoresGlobalRedis(t *testing.T) {
	// Snapshot whatever the global pool holds before the test. In the test
	// environment this is typically empty (no .env redis configured) but we
	// must restore exactly that, not assume emptiness.
	before := cRedis.GetRedisesForTest()

	// Run NewTestSetup in a sub-test so its t.Cleanup runs and completes
	// before we assert on the global state below.
	t.Run("setup", func(t *testing.T) {
		setup := NewTestSetup(t)
		require.NotNil(t, setup)
		require.NotNil(t, setup.Redis)
		require.NotNil(t, setup.Redis.Client)

		// While the setup is active, the global pool must point at our client.
		during := cRedis.GetRedisesForTest()
		require.Len(t, during, 1, "global redis pool should hold exactly the injected client during setup")
		assert.Same(t, setup.Redis.Client, during[0], "global pool should point at the injected client during setup")
	})

	// After the sub-test (and its cleanup) ran, the global pool must be
	// restored to its pre-test value.
	after := cRedis.GetRedisesForTest()
	assert.Equal(t, before, after, "global redis pool must be restored after NewTestSetup cleanup")
}

// TestNewTestSetup_DoesNotLeaveDeadClient verifies that after a NewTestSetup
// test completes, the global redis pool does not reference the (now closed)
// client that was injected. This is the concrete symptom of the bug: subsequent
// callers of db.GetRedis() would get a closed client.
func TestNewTestSetup_DoesNotLeaveDeadClient(t *testing.T) {
	var injectedClient *goredis.Client

	t.Run("setup", func(t *testing.T) {
		setup := NewTestSetup(t)
		require.NotNil(t, setup)
		require.NotNil(t, setup.Redis.Client)
		injectedClient = setup.Redis.Client
	})

	// After cleanup, the global pool must no longer reference the closed client.
	for i, c := range cRedis.GetRedisesForTest() {
		assert.NotSame(t, injectedClient, c, "global pool[%d] must not reference the closed injected client", i)
	}
}

// TestTestDB_SharedBorrowDoesNotCloseUnderlying verifies that the TestDB
// returned by SetupSharedTestDB is a borrowed view: calling Close() on it must
// NOT tear down the shared singleton database that other tests rely on.
func TestTestDB_SharedBorrowDoesNotCloseUnderlying(t *testing.T) {
	shared, err := GetSharedTestDB()
	require.NoError(t, err)
	require.NotNil(t, shared)
	require.NotNil(t, shared.DB, "shared singleton must have a live *api.DB")

	borrowed, err := SetupSharedTestDB(t)
	require.NoError(t, err)
	require.NotNil(t, borrowed)

	// Closing the borrowed view must not close the shared singleton's DB.
	borrowed.Close()
	assert.NotNil(t, shared.DB, "shared singleton DB must still be alive after borrowed view is closed")

	// The borrowed view's Close must be idempotent (safe to call again).
	borrowed.Close()
}

// TestTestDB_CloseIsIdempotent verifies that Close can be called multiple times
// without panicking or double-closing resources.
func TestTestDB_CloseIsIdempotent(t *testing.T) {
	testDB, err := NewTestDB()
	require.NoError(t, err)
	require.NotNil(t, testDB)

	require.NotPanics(t, func() {
		testDB.Close()
		testDB.Close()
		testDB.Close()
	})
}
