package activitypub

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupProxyEnsureDB uses the shared test database (PostgreSQL with
// TEST_DATABASE_URL, otherwise the in-memory engine; both assign ids like
// production does).
func setupProxyEnsureDB(t *testing.T, _ int) *gorm.DB {
	t.Helper()

	conn := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountKey{}, &models.AccountExtra{}, &models.Emoji{}, &models.AccountEmoji{}, &models.AccountFederationKey{}).GormDB

	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	resetProxyCache()
	t.Cleanup(resetProxyCache)

	return conn
}

func TestEnsureProxyAccountIsIdempotent(t *testing.T) {
	conn := setupProxyEnsureDB(t, 1)
	ctx := context.Background()

	first, firstKey, err := EnsureProxyAccount(ctx)
	require.NoError(t, err)
	require.NotZero(t, first.Id)
	require.True(t, first.Bot)
	require.Nil(t, first.Domain)
	require.Contains(t, firstKey.PublicKey, "PUBLIC KEY")

	resetProxyCache()
	second, secondKey, err := EnsureProxyAccount(ctx)
	require.NoError(t, err)
	require.Equal(t, first.Id, second.Id)
	require.Equal(t, firstKey.PublicKey, secondKey.PublicKey)

	var accounts, keys int64
	require.NoError(t, conn.Model(&models.Account{}).Where("username = ? AND domain IS NULL", proxyUsername).Count(&accounts).Error)
	require.NoError(t, conn.Model(&models.AccountKey{}).Count(&keys).Error)
	require.EqualValues(t, 1, accounts)
	require.EqualValues(t, 1, keys)

	// The service uses the identity created at startup.
	got, gotKey, err := NewProxyService().GetOrCreateProxyAccount()
	require.NoError(t, err)
	require.Equal(t, first.Id, got.Id)
	require.Equal(t, firstKey.PrivateKey, gotKey.PrivateKey)
}

// Simulates several replicas starting at once: the in-process mutex is
// bypassed so only the database lock and unique key index serialise them.
// Needs a real database with row locking (PostgreSQL suite); the in-memory
// test engine has no write-write conflict detection.
func TestEnsureProxyAccountConcurrentStarts(t *testing.T) {
	if !testutil.UsePostgres() {
		t.Skip("cross-process locking is exercised on PostgreSQL (TEST_DATABASE_URL)")
	}

	conn := setupProxyEnsureDB(t, 8)
	runConcurrentEnsures(t, conn, func(ctx context.Context) (*models.Account, *models.AccountKey, error) {
		return ensureProxyAccountWithRetry(ctx, conn)
	})
}

// Concurrent callers inside one process (startup plus first signed fetch).
func TestEnsureProxyAccountConcurrentInProcess(t *testing.T) {
	conn := setupProxyEnsureDB(t, 8)
	runConcurrentEnsures(t, conn, EnsureProxyAccount)
}

func runConcurrentEnsures(t *testing.T, conn *gorm.DB, ensure func(context.Context) (*models.Account, *models.AccountKey, error)) {
	t.Helper()

	ctx := context.Background()

	const workers = 8

	var (
		wg     sync.WaitGroup
		ids    = make([]uint64, workers)
		pubs   = make([]string, workers)
		errs   = make([]error, workers)
		starts = make(chan struct{})
	)

	for i := range workers {
		wg.Go(func() {
			<-starts
			account, key, err := ensure(ctx)
			errs[i] = err
			if err == nil {
				ids[i], pubs[i] = account.Id, key.PublicKey
			}
		})
	}

	close(starts)
	wg.Wait()

	for i := range workers {
		require.NoError(t, errs[i])
		require.Equal(t, ids[0], ids[i])
		require.Equal(t, pubs[0], pubs[i])
	}

	var accounts, keys int64
	require.NoError(t, conn.Model(&models.Account{}).Where("username = ? AND domain IS NULL", proxyUsername).Count(&accounts).Error)
	require.NoError(t, conn.Model(&models.AccountKey{}).Count(&keys).Error)
	require.EqualValues(t, 1, accounts)
	require.EqualValues(t, 1, keys)
}

func TestProxyActorResolvesAfterStartupEnsure(t *testing.T) {
	setupProxyEnsureDB(t, 1)

	prevURL, prevConfigured := config.C.AppBaseURL, config.AppBaseURLConfigured()
	config.SetAppBaseURL("https://social.example", true)
	t.Cleanup(func() { config.SetAppBaseURL(prevURL, prevConfigured) })

	actor := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/users/"+proxyUsername, nil)
		c.Params = gin.Params{{Key: "username", Value: proxyUsername}}
		ActorProfile(c)

		return w
	}
	require.Equal(t, 404, actor().Code, "nothing before the ensure")

	_, key, err := EnsureProxyAccount(context.Background())
	require.NoError(t, err)

	w := actor()
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "https://social.example/users/system.proxy#main-key")
	require.Contains(t, w.Body.String(), "BEGIN PUBLIC KEY")
	_ = key
}
