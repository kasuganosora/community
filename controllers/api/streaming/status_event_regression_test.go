package streaming

import (
	"encoding/json"
	"testing"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func mustEventJSON(t *testing.T, typ string, s *models.Status) string {
	t.Helper()

	b, err := json.Marshal(map[string]any{"type": typ, "payload": s})
	require.NoError(t, err)

	return string(b)
}

// A reblog pushed to a stream carries only the reblog_id; the reblogged
// status (and its account) must be loaded before serialization, otherwise
// toStatusBase dereferences a nil Account.
func TestSerializerStatusEvent_ReblogWithoutPreloadedAccount(t *testing.T) {
	setupTestDB(t)
	// Seed serializer fixtures without emitting live status events: those listeners
	// launch asynchronous feed work that can outlive this test's database.
	// Serialization still reloads these rows through the normal database handle.
	gdb := db.GetDB().Session(&gorm.Session{SkipHooks: true})
	setupRedisForTest(t)
	sqlDB, dbErr := gdb.DB()
	require.NoError(t, dbErr)
	sqlDB.SetMaxOpenConns(1) // in-memory SQLite: one connection == one database
	require.NoError(t, gdb.AutoMigrate(&models.AccountExtra{}))

	author := &models.Account{Id: 1, Username: "author"}
	booster := &models.Account{Id: 2, Username: "booster"}
	require.NoError(t, gdb.Create([]*models.Account{author, booster}).Error)

	orig := &models.Status{Id: 10, AccountId: 1, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, gdb.Create(orig).Error)

	reblogID := uint64(10)
	reblog := &models.Status{Id: 11, AccountId: 2, ReblogId: &reblogID, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, gdb.Create(reblog).Error)

	// What the publisher sends: no Account / Reblog objects attached.
	payload := mustEventJSON(t, "update", &models.Status{Id: 11, AccountId: 2, ReblogId: &reblogID, Visibility: models.StatusVisibilityPublic})

	var (
		eventType, out string
		err            error
	)

	require.NotPanics(t, func() {
		eventType, out, err = serializerStatusEvent(nil, payload)
	})
	require.NoError(t, err)
	assert.Equal(t, "update", eventType)
	assert.Contains(t, out, `"username":"booster"`)
	assert.Contains(t, out, `"username":"author"`)
}

// Optional relations that point at rows that no longer exist must not panic.
func TestSerializerStatusEvent_ReblogTargetMissing(t *testing.T) {
	setupTestDB(t)
	// Seed serializer fixtures without emitting live status events: those listeners
	// launch asynchronous feed work that can outlive this test's database.
	// Serialization still reloads these rows through the normal database handle.
	gdb := db.GetDB().Session(&gorm.Session{SkipHooks: true})
	setupRedisForTest(t)
	sqlDB, dbErr := gdb.DB()
	require.NoError(t, dbErr)
	sqlDB.SetMaxOpenConns(1) // in-memory SQLite: one connection == one database
	require.NoError(t, gdb.AutoMigrate(&models.AccountExtra{}))

	require.NoError(t, gdb.Create(&models.Account{Id: 2, Username: "booster"}).Error)

	gone := uint64(999)
	payload := mustEventJSON(t, "update", &models.Status{Id: 11, AccountId: 2, ReblogId: &gone, Visibility: models.StatusVisibilityPublic})

	require.NotPanics(t, func() {
		_, _, err := serializerStatusEvent(nil, payload)
		require.ErrorIs(t, err, errEventNotVisible)
	})
}

// If the author genuinely does not exist the event is skipped with an error
// (logged by the SSE loop) instead of panicking.
func TestSerializerStatusEvent_AccountMissingSkipped(t *testing.T) {
	setupTestDB(t)

	payload := mustEventJSON(t, "update", &models.Status{Id: 12, AccountId: 404, Visibility: models.StatusVisibilityPublic})

	require.NotPanics(t, func() {
		_, _, err := serializerStatusEvent(nil, payload)
		require.ErrorIs(t, err, errEventNotVisible)
	})
}

func TestSerializerStatusEvent_NullPayload(t *testing.T) {
	setupTestDB(t)

	require.NotPanics(t, func() {
		_, _, err := serializerStatusEvent(nil, `{"type":"update","payload":null}`)
		require.Error(t, err)
	})
}
