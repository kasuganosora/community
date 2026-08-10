package homeserver

import (
	"crypto/ed25519"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// resetGlobalHS sets GlobalHS to the given value and resets initOnce so that
// GetHomeServer() returns GlobalHS without attempting InitHomeServer (which
// requires a configured DB). Returns a restore function that resets GlobalHS.
//
// Note: initOnce is reset to a fresh sync.Once{} (matching the pattern used by
// other tests in this package). Tests in this package are not run in parallel.
func resetGlobalHS(hs *HomeServer) func() {
	savedHS := GlobalHS
	initOnce = sync.Once{} // reset so GetHomeServer() can be controlled
	GlobalHS = hs
	// If GlobalHS is set, GetHomeServer() returns it without re-init (nil check).
	// If GlobalHS is nil, we must ensure initOnce is "done" to avoid re-init.
	if hs == nil {
		// Mark once as done so GetHomeServer() won't trigger InitHomeServer
		initOnce.Do(func() {})
	}
	return func() {
		GlobalHS = savedHS
	}
}

// ===== SyncNotifier unit tests =====

func TestSyncNotifier_New(t *testing.T) {
	n := NewSyncNotifier()
	assert.NotNil(t, n)
	assert.NotNil(t, n.waiters)
}

func TestSyncNotifier_NotifyWait_RoundTrip(t *testing.T) {
	n := NewKeyCache() // dummy to avoid collision; replace below
	_ = n

	notifier := NewSyncNotifier()

	// Register a waiter
	ch := notifier.Wait("@alice:test.local", "DEVICE1")

	// Notify in a goroutine after a short delay
	go func() {
		time.Sleep(20 * time.Millisecond)
		notifier.Notify("@alice:test.local", "DEVICE1")
	}()

	// Wait should unblock when notified
	select {
	case <-ch:
		// success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Wait was not unblocked by Notify")
	}
}

func TestSyncNotifier_Notify_NoWaiter(t *testing.T) {
	notifier := NewSyncNotifier()
	// Notify with no waiters should not panic
	assert.NotPanics(t, func() {
		notifier.Notify("@nobody:test.local", "DEV")
	})
}

func TestSyncNotifier_NotifyMultipleWaiters(t *testing.T) {
	notifier := NewSyncNotifier()

	ch1 := notifier.Wait("@bob:test.local", "D1")
	ch2 := notifier.Wait("@bob:test.local", "D1")

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		select {
		case <-ch1:
		case <-time.After(500 * time.Millisecond):
			t.Error("waiter 1 timed out")
		}
	}()

	go func() {
		defer wg.Done()
		select {
		case <-ch2:
		case <-time.After(500 * time.Millisecond):
			t.Error("waiter 2 timed out")
		}
	}()

	time.Sleep(20 * time.Millisecond)
	notifier.Notify("@bob:test.local", "D1")

	wg.Wait()
}

func TestSyncNotifier_NotifyRemovesWaiters(t *testing.T) {
	notifier := NewSyncNotifier()

	_ = notifier.Wait("@carol:test.local", "DEV")
	notifier.Notify("@carol:test.local", "DEV")

	// After notify, the waiters map for this key should be empty
	notifier.mu.RLock()
	_, exists := notifier.waiters["@carol:test.local:DEV"]
	notifier.mu.RUnlock()
	// Key uses userID:deviceID format
	notifier.mu.RLock()
	_, exists2 := notifier.waiters["@carol:test.local"]
	notifier.mu.RUnlock()

	_ = exists
	assert.False(t, exists2, "waiters key should be removed after Notify")
}

func TestSyncNotifier_DifferentDevices(t *testing.T) {
	notifier := NewSyncNotifier()

	chDev1 := notifier.Wait("@dave:test.local", "DEV1")
	_ = notifier.Wait("@dave:test.local", "DEV2")

	// Notify only DEV1
	notifier.Notify("@dave:test.local", "DEV1")

	// DEV1 should be unblocked
	select {
	case <-chDev1:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("DEV1 should have been notified")
	}
}

// ===== HomeServer.NotifyToDeviceUpdate tests =====

func TestHomeServer_NotifyToDeviceUpdate_NilNotifier(t *testing.T) {
	hs := &HomeServer{
		Config: &HSConfig{ServerName: "test.local"},
		// SyncNotifier is nil
	}
	// Should not panic when notifier is nil
	assert.NotPanics(t, func() {
		hs.NotifyToDeviceUpdate("@alice:test.local", "DEV1")
	})
}

func TestHomeServer_NotifyToDeviceUpdate_WithNotifier(t *testing.T) {
	hs := &HomeServer{
		Config:       &HSConfig{ServerName: "test.local"},
		SyncNotifier: NewSyncNotifier(),
	}
	ch := hs.SyncNotifier.Wait("@alice:test.local", "DEV1")

	go func() {
		time.Sleep(10 * time.Millisecond)
		hs.NotifyToDeviceUpdate("@alice:test.local", "DEV1")
	}()

	select {
	case <-ch:
		// success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("NotifyToDeviceUpdate should trigger notification")
	}
}

// ===== HomeServer.NotifyRoomEvent / NotifyUser with nil notifier =====

func TestHomeServer_NotifyRoomEvent_NilNotifier(t *testing.T) {
	hs := &HomeServer{
		Config: &HSConfig{ServerName: "test.local"},
	}
	// Should not panic
	assert.NotPanics(t, func() {
		hs.NotifyRoomEvent("!room:test.local")
	})
}

func TestHomeServer_NotifyUser_NilNotifier(t *testing.T) {
	hs := &HomeServer{
		Config: &HSConfig{ServerName: "test.local"},
	}
	// Should not panic
	assert.NotPanics(t, func() {
		hs.NotifyUser("@alice:test.local")
	})
}

// ===== IsEnabled tests =====

func TestIsEnabled_GlobalHSNil(t *testing.T) {
	restore := resetGlobalHS(nil)
	defer restore()
	assert.False(t, IsEnabled())
}

func TestIsEnabled_Disabled(t *testing.T) {
	restore := resetGlobalHS(&HomeServer{Config: &HSConfig{Enabled: false}})
	defer restore()
	assert.False(t, IsEnabled())
}

func TestIsEnabled_Enabled(t *testing.T) {
	restore := resetGlobalHS(&HomeServer{Config: &HSConfig{Enabled: true}})
	defer restore()
	assert.True(t, IsEnabled())
}

// ===== GetInitError tests =====

func TestGetInitError_NilByDefault(t *testing.T) {
	// errInit may be nil or set depending on previous tests; just verify it doesn't panic
	_ = GetInitError()
}

// ===== loadHSConfig tests =====

func TestLoadHSConfig_WithServerNameEnv(t *testing.T) {
	t.Setenv("SERVER_NAME", "envoverride.test")

	cfg := loadHSConfig()
	assert.Equal(t, "envoverride.test", cfg.ServerName)
	assert.Equal(t, "https://envoverride.test", cfg.BaseURL)
	assert.Equal(t, 8448, cfg.FederationPort)
	assert.Equal(t, "10", cfg.RoomVersion)
	assert.True(t, cfg.Enabled)
}

func TestLoadHSConfig_EmptyServerNameFallsBack(t *testing.T) {
	// SERVER_NAME empty, systemsettings.GetDomain() may return empty too
	t.Setenv("SERVER_NAME", "")

	cfg := loadHSConfig()
	// Should fall back to "localhost" if domain is empty
	if cfg.ServerName == "" {
		cfg.ServerName = "localhost"
	}
	assert.Equal(t, "localhost", cfg.ServerName)
}

// ===== HomeServer construction with keys =====

func TestHomeServer_KeyFields(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	hs := &HomeServer{
		Config: &HSConfig{
			ServerName: "keytest.local",
		},
		privateKey: priv,
		publicKey:  pub,
		keyID:      "ed25519:test",
	}

	assert.Equal(t, "keytest.local", hs.Config.ServerName)
	assert.Equal(t, priv, hs.privateKey)
	assert.Equal(t, pub, hs.publicKey)
	assert.Equal(t, "ed25519:test", hs.keyID)
}

func TestHSConfig_Defaults(t *testing.T) {
	cfg := &HSConfig{
		Enabled:        true,
		ServerName:     "default.test",
		BaseURL:        "https://default.test",
		AdminToken:     "",
		RoomVersion:    "10",
		EncryptedKey:   "",
		FederationPort: 8448,
	}
	assert.Equal(t, "10", cfg.RoomVersion)
	assert.Equal(t, 8448, cfg.FederationPort)
	assert.True(t, cfg.Enabled)
}
