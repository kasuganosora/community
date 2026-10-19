package activitypub

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// openLocalKeyTestDB returns a multi-connection database so callers really
// run concurrently: a WAL SQLite file (immediate write transactions, so
// writers queue instead of failing to upgrade) or a PostgreSQL schema when
// TEST_DATABASE_URL is set.
func openLocalKeyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "keys.db") + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	conn, err := pgtest.GormOpen(t, sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.AccountKey{}, &models.AccountFederationKey{}))
	return conn
}

func TestLocalAccountKeyConcurrentFirstUseCreatesOneKey(t *testing.T) {
	conn := openLocalKeyTestDB(t)
	account := &models.Account{Id: 7101, Username: "burst_actor"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(account).Error)

	const callers = 12
	keys := make([]*models.AccountKey, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			keys[i], errs[i] = localAccountKeyWith(conn, account)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i], "caller %d", i)
		require.NotZero(t, keys[i].ID)
		require.Equal(t, keys[0].ID, keys[i].ID, "caller %d got another key row", i)
		require.Equal(t, keys[0].PublicKey, keys[i].PublicKey, "caller %d got another public key", i)
		require.Equal(t, keys[0].PrivateKey, keys[i].PrivateKey, "caller %d got another private key", i)
	}
	var count int64
	require.NoError(t, conn.Model(&models.AccountKey{}).Where("account_id = ?", account.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, keys[0].ValidateKeyPair())
}

// An existing key must be served by plain reads: signing and actor documents
// may not queue behind (or hold) the account row lock.
func TestLocalAccountKeyExistingKeyIgnoresAccountRowLock(t *testing.T) {
	conn := openLocalKeyTestDB(t)
	account := &models.Account{Id: 7102, Username: "busy_actor"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(account).Error)
	first, err := localAccountKeyWith(conn, account)
	require.NoError(t, err)

	// Another session holds the account row lock (and, on SQLite, the write
	// lock) for the whole check, as a long account update would.
	holder := conn.Begin()
	require.NoError(t, holder.Error)
	t.Cleanup(func() { holder.Rollback() })
	var locked models.Account
	require.NoError(t, holder.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", account.Id).Take(&locked).Error)
	require.NoError(t, holder.Model(&models.Account{}).Where("id = ?", account.Id).Update("note", "being edited").Error)

	const callers = 8
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make([]error, callers)
	keys := make([]*models.AccountKey, callers)
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			keys[i], errs[i] = localAccountKeyWith(conn.WithContext(ctx), account)
		}(i)
	}
	wg.Wait()
	for i := range callers {
		require.NoError(t, errs[i], "caller %d blocked on the account lock", i)
		require.Equal(t, first.ID, keys[i].ID)
		require.Equal(t, first.PublicKey, keys[i].PublicKey)
	}
}
