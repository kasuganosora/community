package activitypub

import (
	"testing"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRelayServiceDB creates a test DB and injects it globally for functions
// that use db.GetDB() internally (like GenerateActivityID which calls getDomain).
func setupRelayServiceDB(t *testing.T) *testutil.TestDB {
	t.Helper()
	testDB := testutil.QuickDBSetup(t,
		&models.Relay{},
		&Delivery{},
		&models.SystemSetting{},
	)
	database.SetTestDB(testDB.GormDB)
	t.Cleanup(database.ResetTestDB)
	return testDB
}

func TestRelayServiceNewRelayService(t *testing.T) {
	setupRelayServiceDB(t)
	svc := NewRelayService()
	require.NotNil(t, svc)
	assert.NotNil(t, svc.db)
}

func TestRelayServiceGetDomain(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	// Set a domain so getDomain returns a non-empty value
	systemsettings.Set(systemsettings.KeyDomain, "testrelay.example.com")
	t.Cleanup(func() { systemsettings.Set(systemsettings.KeyDomain, "") })

	svc := &RelayService{db: testDB.GormDB}
	domain := svc.getDomain()
	assert.Equal(t, "testrelay.example.com", domain)
}

func TestRelayServiceGetInstanceActorURI(t *testing.T) {
	setupRelayServiceDB(t)
	svc := &RelayService{db: database.GetDB()}
	uri := svc.GetInstanceActorURI()
	assert.Contains(t, uri, "/actor")
	assert.Contains(t, uri, "https://")
}

func TestRelayServiceGetPublicCollectionURI(t *testing.T) {
	setupRelayServiceDB(t)
	svc := &RelayService{db: database.GetDB()}
	uri := svc.GetPublicCollectionURI()
	assert.Contains(t, uri, "/public")
}

func TestRelayServiceGenerateActivityID(t *testing.T) {
	setupRelayServiceDB(t)
	svc := &RelayService{db: database.GetDB()}
	id1 := svc.GenerateActivityID("follow")
	id2 := svc.GenerateActivityID("undo")
	assert.Contains(t, id1, "/activities/follow/")
	assert.Contains(t, id2, "/activities/undo/")
	// IDs should be unique (time-based)
	assert.NotEqual(t, id1, id2)
}

func TestRelayServiceCreateRelayError(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	svc := &RelayService{db: testDB.GormDB}

	// Create a relay
	relay, err := svc.CreateRelay("https://relay.example.com/inbox")
	require.NoError(t, err)
	assert.Equal(t, models.RelayStateIdle, relay.State)

	// Duplicate should error
	_, err = svc.CreateRelay("https://relay.example.com/inbox")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestRelayServiceEnableRelay(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	svc := &RelayService{db: testDB.GormDB}

	relay, err := svc.CreateRelay("https://relay-enable.example.com/inbox")
	require.NoError(t, err)

	// Enable it
	err = svc.EnableRelay(relay.ID)
	require.NoError(t, err)

	// Verify state changed to pending
	fetched, _ := svc.GetRelayByID(relay.ID)
	assert.Equal(t, models.RelayStatePending, fetched.State)
	assert.NotEmpty(t, fetched.FollowActivityID)

	// Verify a delivery was created
	var deliveryCount int64
	testDB.GormDB.Model(&Delivery{}).Where("target_inbox = ?", relay.InboxURL).Count(&deliveryCount)
	assert.Equal(t, int64(1), deliveryCount)
}

func TestRelayServiceEnableRelayAlreadyEnabled(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	svc := &RelayService{db: testDB.GormDB}

	relay, err := svc.CreateRelay("https://relay-already.example.com/inbox")
	require.NoError(t, err)

	// Set relay to accepted state (enabled)
	relay.State = models.RelayStateAccepted
	require.NoError(t, testDB.GormDB.Save(relay).Error)

	// Enable again should error because it's already enabled
	err = svc.EnableRelay(relay.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already enabled")
}

func TestRelayServiceEnableRelayNotFound(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	svc := &RelayService{db: testDB.GormDB}

	err := svc.EnableRelay(99999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRelayServiceDisableRelay(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	svc := &RelayService{db: testDB.GormDB}

	relay, err := svc.CreateRelay("https://relay-disable.example.com/inbox")
	require.NoError(t, err)

	// Enable first
	require.NoError(t, svc.EnableRelay(relay.ID))

	// Now disable
	err = svc.DisableRelay(relay.ID)
	require.NoError(t, err)

	// Verify state changed to idle
	fetched, _ := svc.GetRelayByID(relay.ID)
	assert.Equal(t, models.RelayStateIdle, fetched.State)
	assert.Empty(t, fetched.FollowActivityID)
}

func TestRelayServiceDisableRelayNotFound(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	svc := &RelayService{db: testDB.GormDB}

	err := svc.DisableRelay(99999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRelayServiceDeleteRelayEnabled(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	svc := &RelayService{db: testDB.GormDB}

	relay, err := svc.CreateRelay("https://relay-delete-enabled.example.com/inbox")
	require.NoError(t, err)

	// Enable first (so delete needs to disable first)
	require.NoError(t, svc.EnableRelay(relay.ID))

	// Delete should work (calls DisableRelay internally)
	err = svc.DeleteRelay(relay.ID)
	require.NoError(t, err)
}

func TestRelayServiceRejectRelayNotFound(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	svc := &RelayService{db: testDB.GormDB}

	// Non-existent follow activity ID returns nil (not an error)
	err := svc.RejectRelay("https://nonexistent.example.com/activity")
	assert.NoError(t, err)
}

func TestRelayServiceRejectRelaySuccess(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	svc := &RelayService{db: testDB.GormDB}

	relay, err := svc.CreateRelay("https://relay-reject.example.com/inbox")
	require.NoError(t, err)
	relay.FollowActivityID = "https://example.com/activities/reject/123"
	relay.State = models.RelayStatePending
	require.NoError(t, testDB.GormDB.Save(relay).Error)

	err = svc.RejectRelay("https://example.com/activities/reject/123")
	require.NoError(t, err)

	fetched, _ := svc.GetRelayByID(relay.ID)
	assert.Equal(t, models.RelayStateRejected, fetched.State)
}

func TestGetEnabledRelayInboxesGlobal(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	svc := &RelayService{db: testDB.GormDB}

	// Create and enable a relay
	relay, err := svc.CreateRelay("https://relay-global.example.com/inbox")
	require.NoError(t, err)
	relay.State = models.RelayStateAccepted
	require.NoError(t, testDB.GormDB.Save(relay).Error)

	// Test the global function
	inboxes, err := GetEnabledRelayInboxes()
	require.NoError(t, err)
	assert.Contains(t, inboxes, "https://relay-global.example.com/inbox")
}

func TestIsRequestedThroughRelay(t *testing.T) {
	testDB := setupRelayServiceDB(t)

	// Create an enabled relay
	relay := &models.Relay{
		InboxURL: "https://relay-check.example.com/inbox",
		State:    models.RelayStateAccepted,
	}
	require.NoError(t, testDB.GormDB.Create(relay).Error)

	assert.True(t, IsRequestedThroughRelay("https://relay-check.example.com/inbox"))
	assert.False(t, IsRequestedThroughRelay("https://nonexistent.example.com/inbox"))
}

func TestDistributeToRelaysNoRelays(t *testing.T) {
	setupRelayServiceDB(t)

	// No enabled relays - should return nil
	err := DistributeToRelays(map[string]any{"type": "Create"}, "https://example.com/activities/1")
	assert.NoError(t, err)
}

func TestRelayServiceGetRelayByIDNotFound(t *testing.T) {
	testDB := setupRelayServiceDB(t)
	svc := &RelayService{db: testDB.GormDB}

	_, err := svc.GetRelayByID(99999)
	require.Error(t, err)
}

func TestRelayServiceWithCustomDomain(t *testing.T) {
	setupRelayServiceDB(t)
	systemsettings.Set(systemsettings.KeyDomain, "custom.example.com")
	t.Cleanup(func() { systemsettings.Set(systemsettings.KeyDomain, "") })

	svc := &RelayService{db: database.GetDB()}
	uri := svc.GetInstanceActorURI()
	assert.Contains(t, uri, "custom.example.com")
}
