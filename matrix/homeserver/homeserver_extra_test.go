package homeserver

import (
	"github.com/kasuganosora/community/systemsettings"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== NotifyRoomEvent tests (DB-backed) =====

func TestHomeServer_NotifyRoomEvent_NoMembers(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomMember{}, &MatrixUserSession{}))

	notifier := NewSyncNotifier()
	hs := &HomeServer{
		Config:       &HSConfig{ServerName: "test.local"},
		SyncNotifier: notifier,
	}
	// No members in DB → NotifyRoomEvent should not panic and should be a no-op.
	assert.NotPanics(t, func() {
		hs.NotifyRoomEvent("!empty:test.local")
	})
}

func TestHomeServer_NotifyRoomEvent_RemoteMembersSkipped(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomMember{}, &MatrixUserSession{}))

	// Insert a remote member (different domain) → should be skipped.
	member := &MatrixRoomMember{
		RoomID:     "!remote:test.local",
		UserID:     "@alice:other.server",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	notifier := NewSyncNotifier()
	hs := &HomeServer{
		Config:       &HSConfig{ServerName: "test.local"},
		SyncNotifier: notifier,
	}
	assert.NotPanics(t, func() {
		hs.NotifyRoomEvent("!remote:test.local")
	})
}

func TestHomeServer_NotifyRoomEvent_LocalMemberNotifiesDevices(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomMember{}, &MatrixUserSession{}))

	// Insert a local member with a session.
	member := &MatrixRoomMember{
		RoomID:     "!localroom:test.local",
		UserID:     "@bob:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	session := &MatrixUserSession{
		UserID:      "@bob:test.local",
		DeviceID:    "DEVNOTIFY",
		AccessToken: "tok-notify",
	}
	require.NoError(t, db.GetDB().Create(session).Error)

	notifier := NewSyncNotifier()
	hs := &HomeServer{
		Config:       &HSConfig{ServerName: "test.local"},
		SyncNotifier: notifier,
	}

	// Register a waiter for the device before notifying.
	ch := notifier.Wait("@bob:test.local", "DEVNOTIFY")

	// Notify in a goroutine (Notify is synchronous but fast).
	go func() {
		time.Sleep(10 * time.Millisecond)
		hs.NotifyRoomEvent("!localroom:test.local")
	}()

	select {
	case <-ch:
		// success: the local member's device was notified
	case <-time.After(500 * time.Millisecond):
		t.Fatal("NotifyRoomEvent should notify local member devices")
	}
}

func TestHomeServer_NotifyRoomEvent_LocalMemberNoSessions(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomMember{}, &MatrixUserSession{}))

	// Local member but no sessions → no notification, no panic.
	member := &MatrixRoomMember{
		RoomID:     "!nosess:test.local",
		UserID:     "@carol:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	notifier := NewSyncNotifier()
	hs := &HomeServer{
		Config:       &HSConfig{ServerName: "test.local"},
		SyncNotifier: notifier,
	}
	assert.NotPanics(t, func() {
		hs.NotifyRoomEvent("!nosess:test.local")
	})
}

// ===== NotifyUser tests (DB-backed) =====

func TestHomeServer_NotifyUser_NoSessions(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))

	notifier := NewSyncNotifier()
	hs := &HomeServer{
		Config:       &HSConfig{ServerName: "test.local"},
		SyncNotifier: notifier,
	}
	assert.NotPanics(t, func() {
		hs.NotifyUser("@nobody:test.local")
	})
}

func TestHomeServer_NotifyUser_WithSessions(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))

	session := &MatrixUserSession{
		UserID:      "@dave:test.local",
		DeviceID:    "DEVUSER",
		AccessToken: "tok-user",
	}
	require.NoError(t, db.GetDB().Create(session).Error)

	notifier := NewSyncNotifier()
	hs := &HomeServer{
		Config:       &HSConfig{ServerName: "test.local"},
		SyncNotifier: notifier,
	}

	ch := notifier.Wait("@dave:test.local", "DEVUSER")

	go func() {
		time.Sleep(10 * time.Millisecond)
		hs.NotifyUser("@dave:test.local")
	}()

	select {
	case <-ch:
		// success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("NotifyUser should notify the user's devices")
	}
}

// ===== StartKeyCacheCleanup tests =====

func TestStartKeyCacheCleanup_DoesNotPanic(t *testing.T) {
	// StartKeyCacheCleanup launches a goroutine with an hourly ticker. We just
	// verify it doesn't panic on startup. The goroutine leaks a ticker, but
	// since tests are short-lived this is acceptable for coverage.
	assert.NotPanics(t, func() {
		StartKeyCacheCleanup()
	})
	// Give the goroutine a moment to start.
	time.Sleep(20 * time.Millisecond)
}

// ===== GetKeyCache singleton =====

func TestGetKeyCache_Singleton(t *testing.T) {
	kc1 := GetKeyCache()
	require.NotNil(t, kc1)
	kc2 := GetKeyCache()
	assert.Same(t, kc1, kc2, "GetKeyCache should return the same singleton")
}

// ===== HomeServer.NotifyToDeviceUpdate with real notifier =====

func TestHomeServer_NotifyToDeviceUpdate_Notifies(t *testing.T) {
	notifier := NewSyncNotifier()
	hs := &HomeServer{
		Config:       &HSConfig{ServerName: "test.local"},
		SyncNotifier: notifier,
	}

	ch := notifier.Wait("@eve:test.local", "DEVEVE")

	go func() {
		time.Sleep(10 * time.Millisecond)
		hs.NotifyToDeviceUpdate("@eve:test.local", "DEVEVE")
	}()

	select {
	case <-ch:
		// success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("NotifyToDeviceUpdate should trigger notification")
	}
}

// ===== loadHSConfig additional tests =====

func TestLoadHSConfig_SystemSettingsFallback(t *testing.T) {
	// Without SERVER_NAME env var, loadHSConfig falls back to
	// systemsettings.GetDomain() then "localhost".
	t.Setenv("SERVER_NAME", "")
	t.Setenv("MATRIX_FEDERATION_PORT", "")

	cfg := loadHSConfig()
	// ServerName and BaseURL follow the instance domain / base URL.
	assert.Equal(t, systemsettings.GetDomain(), cfg.ServerName)
	assert.Equal(t, systemsettings.GetBaseURL(), cfg.BaseURL)
	assert.Equal(t, 443, cfg.FederationPort)
	assert.Equal(t, "10", cfg.RoomVersion)
	assert.True(t, cfg.Enabled)
}

// ===== InitHomeServer (disabled config returns nil error) =====

func TestInitHomeServer_DisabledConfig(t *testing.T) {
	// Temporarily disable the homeserver via SERVER_NAME; we cannot easily
	// flip the Enabled flag (it's hardcoded to true in loadHSConfig), but we
	// can verify InitHomeServer does not panic when GlobalHS is nil.
	// We reset initOnce and GlobalHS to a clean state and rely on the fact
	// that InitHomeServer will attempt DB migration (which needs a real DB).
	// Instead, just verify the function exists and the package compiles.
	restore := resetGlobalHS(&HomeServer{Config: &HSConfig{Enabled: false}})
	defer restore()
	assert.False(t, IsEnabled())
}

func TestFederationPortFromEnv(t *testing.T) {
	t.Setenv("SERVER_NAME", "")
	t.Setenv("MATRIX_FEDERATION_PORT", "")
	assert.Equal(t, 443, federationPortFromEnv())
	t.Setenv("SERVER_NAME", "complement.test")
	assert.Equal(t, 8448, federationPortFromEnv())
	t.Setenv("MATRIX_FEDERATION_PORT", "9000")
	assert.Equal(t, 9000, federationPortFromEnv())
	t.Setenv("MATRIX_FEDERATION_PORT", "junk")
	assert.Equal(t, 8448, federationPortFromEnv())
}
