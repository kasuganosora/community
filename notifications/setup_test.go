package notifications

import (
	"os"
	"testing"
	"time"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	sqlexecapi "github.com/kasuganosora/sqlexec/pkg/api"
	sqlexecgorm "github.com/kasuganosora/sqlexec/pkg/api/gorm"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestMain enables test mode for the database package (so InitGorm is a no-op)
// before running any tests. Without this, importing the db package would try
// to open a real database connection on package init.
func TestMain(m *testing.M) {
	database.SetTestMode()
	os.Exit(m.Run())
}

// notificationsStateMu serializes tests that mutate the package-global
// database.DB handle. None of these tests use t.Parallel, so a simple mutex
// keeps the global mutation test-local and prevents races between tests.
var notificationsStateMu stateMutex

// stateMutex is a tiny channel-based mutex used to serialize tests that
// mutate package-global database state.
type stateMutex struct{ ch chan struct{} }

func (m *stateMutex) Lock()   { <-m.ch }
func (m *stateMutex) Unlock() { m.ch <- struct{}{} }

func init() {
	notificationsStateMu.ch = make(chan struct{}, 1)
	notificationsStateMu.ch <- struct{}{}
}

// allModels lists every model the notifications package touches, so that
// auto-migration produces a consistent schema for every test.
func allModels() []any {
	return []any{
		&models.User{},
		&models.Account{},
		&models.AccountExtra{},
		&models.AccountEmoji{},
		&models.Status{},
		&models.StatusContent{},
		&models.StatusTag{},
		&models.StatusToAccount{},
		&models.StatusEmoji{},
		&models.StatusMediaAttachment{},
		&models.FavoriteStatus{},
		&models.Follow{},
		&models.Block{},
		&models.DomainBlock{},
		&models.Notification{},
		&models.Report{},
		&models.Poll{},
		&models.PollOption{},
		&models.PollOptionVote{},
		&models.List{},
		&models.ListAccount{},
		&models.ListFollowAccount{},
		&models.Tag{},
		&models.Emoji{},
	}
}

// setupTestDB creates an in-memory SQLExec database, injects it via
// database.SetTestDB so that db.GetDB() returns it, and migrates all the
// models the notifications package touches. The previous DB handle is
// restored on cleanup.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	testDB, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err, "failed to create isolated test DB")

	// Routing tests poll while a notification goroutine writes. SQLExec's
	// result cache can retain an empty SELECT if that read finishes after
	// the concurrent INSERT invalidates the cache. Use uncached reads so
	// these tests observe database state, like the production MySQL driver.
	if testDB.DB != nil {
		session := testDB.DB.SessionWithOptions(&sqlexecapi.SessionOptions{CacheEnabled: false})
		t.Cleanup(func() { _ = session.Close() })
		testDB.GormDB, err = gorm.Open(sqlexecgorm.NewDialector(session), &gorm.Config{SkipDefaultTransaction: true, NowFunc: testutil.SQLExecNow})
		require.NoError(t, err)
	}

	require.NoError(t, testDB.AutoMigrate(allModels()...), "failed to auto-migrate models")

	// Inject the DB and snapshot the previous handle for restoration.
	prevDB := database.GetDB()
	database.SetTestDB(testDB.GormDB)

	t.Cleanup(func() {
		if prevDB != nil {
			database.SetTestDB(prevDB)
		} else {
			database.ResetTestDB()
		}
	})

	return testDB.GormDB
}

// createLocalAccount inserts a local account (Domain == nil) and returns it.
func createLocalAccount(t *testing.T, gdb *gorm.DB, username string) *models.Account {
	t.Helper()
	now := time.Now()
	account := &models.Account{
		Username:    username,
		DisplayName: username,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	require.NoError(t, gdb.Create(account).Error, "failed to create local account")
	return account
}

// createRemoteAccount inserts a remote account (Domain != nil) and returns it.
func createRemoteAccount(t *testing.T, gdb *gorm.DB, username, domain string) *models.Account {
	t.Helper()
	now := time.Now()
	account := &models.Account{
		Username:    username,
		Domain:      &domain,
		DisplayName: username,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	require.NoError(t, gdb.Create(account).Error, "failed to create remote account")
	return account
}

// createStatus inserts a status owned by accountId and returns it.
// It uses Omit to skip association hooks so the status event handlers are
// not cascaded (we call notification functions directly for determinism).
//
// Note: creating a Status fires the "status/after_create" event, which the
// notifications package handles in background goroutines (via safeGo). To keep
// tests deterministic we wait briefly after creation so those goroutines
// settle before the test adds more data; otherwise a goroutine could observe
// rows that did not exist when the event fired.
func createStatus(t *testing.T, gdb *gorm.DB, accountId uint64) *models.Status {
	t.Helper()
	now := time.Now()
	status := &models.Status{
		AccountId:  accountId,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}
	require.NoError(t, gdb.Create(status).Error, "failed to create status")
	settleEvents()
	return status
}

// settleEvents waits briefly for notification goroutines spawned by event
// handlers (status after_create, etc.) to finish. Mirrors the approach used
// in feed/tag tests.
func settleEvents() {
	time.Sleep(80 * time.Millisecond)
}

// countNotifications returns the total number of notifications for accountId.
func countNotifications(t *testing.T, gdb *gorm.DB, accountId uint64) int64 {
	t.Helper()
	var n int64
	require.NoError(t, gdb.Model(&models.Notification{}).Where("account_id = ?", accountId).Count(&n).Error)
	return n
}

// latestNotificationFor returns the most recent notification of the given type
// for accountId, or nil if none exists.
func latestNotificationFor(t *testing.T, gdb *gorm.DB, accountId uint64, notifType int) *models.Notification {
	t.Helper()
	var n models.Notification
	err := gdb.Where("account_id = ? AND type = ?", accountId, notifType).Last(&n).Error
	if err != nil {
		return nil
	}
	return &n
}

// allNotifications returns all notifications of the given type for accountId.
func allNotifications(t *testing.T, gdb *gorm.DB, accountId uint64, notifType int) []models.Notification {
	t.Helper()
	var ns []models.Notification
	require.NoError(t, gdb.Where("account_id = ? AND type = ?", accountId, notifType).Find(&ns).Error)
	return ns
}
