package matrix

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ====================================================================
// Bridge construction
// ====================================================================

func TestNewBridgeWithConfig(t *testing.T) {
	cfg := &Config{
		Enabled:             false,
		HomeserverURL:       "https://matrix.example.com",
		ServerName:          "example.com",
		ASToken:             "as",
		HSToken:             "hs",
		BridgeUsername:      "_bridge",
		UserNamespacePrefix: "_c_",
	}
	b := NewBridgeWithConfig(cfg)
	require.NotNil(t, b)
	assert.False(t, b.IsStarted())
	assert.Equal(t, cfg, b.config)
	assert.NotNil(t, b.client)
}

// ====================================================================
// Bridge.Start (disabled)
// ====================================================================

func TestBridge_Start_Disabled(t *testing.T) {
	cfg := &Config{
		Enabled: false, // disabled — Start should be a no-op returning nil.
	}
	b := NewBridgeWithConfig(cfg)
	err := b.Start()
	require.NoError(t, err)
	assert.False(t, b.IsStarted())
}

// ====================================================================
// Bridge.Start with ping failure
// ====================================================================

func TestBridge_Start_PingFailure(t *testing.T) {
	cfg := &Config{
		Enabled:       true,
		HomeserverURL: "http://127.0.0.1:1", // non-routable
		ServerName:    "example.com",
		ASToken:       "as",
		HSToken:       "hs",
	}
	b := NewBridgeWithConfig(cfg)
	err := b.Start()
	require.Error(t, err)
	assert.False(t, b.IsStarted())
}

// ====================================================================
// Bridge.Stop (not started)
// ====================================================================

func TestBridge_Stop_NotStarted(t *testing.T) {
	b := NewBridgeWithConfig(&Config{Enabled: false})
	// Stop on a non-started bridge should be a no-op.
	b.Stop()
	assert.False(t, b.IsStarted())
}

// ====================================================================
// Bridge.Stop / Start-Stop cycle
// ====================================================================

func TestBridge_Stop_AfterManualStart(t *testing.T) {
	b := NewBridgeWithConfig(&Config{Enabled: false})
	// Simulate started without a real HS by setting the flag directly.
	b.started.Store(true)
	assert.True(t, b.IsStarted())

	b.Stop()
	assert.False(t, b.IsStarted())
	// Stopping again should be safe.
	b.Stop()
	assert.False(t, b.IsStarted())
}

// ====================================================================
// Bridge getters
// ====================================================================

func TestBridge_GetConfig(t *testing.T) {
	cfg := &Config{ServerName: "myserver"}
	b := NewBridgeWithConfig(cfg)
	assert.Equal(t, cfg, b.GetConfig())
}

func TestBridge_GetClient(t *testing.T) {
	b := NewBridgeWithConfig(&Config{HomeserverURL: "http://x", ASToken: "a"})
	c := b.GetClient()
	require.NotNil(t, c)
}

func TestBridge_GetUserMapper_Nil(t *testing.T) {
	b := NewBridgeWithConfig(&Config{Enabled: false})
	// Before Start, userMapper is nil.
	assert.Nil(t, b.GetUserMapper())
	assert.Nil(t, b.GetRoomMapper())
	assert.Nil(t, b.GetMessageBridge())
}

// ====================================================================
// Bridge.HandleTransaction (not started)
// ====================================================================

func TestBridge_HandleTransaction_NotStarted(t *testing.T) {
	b := NewBridgeWithConfig(&Config{Enabled: false})
	err := b.HandleTransaction("txn1", []Event{
		{Type: EventTypeRoomMessage, EventID: "$e1"},
	})
	require.NoError(t, err)
}

// ====================================================================
// Bridge.SendToMatrix (not started)
// ====================================================================

func TestBridge_SendToMatrix_NotStarted(t *testing.T) {
	b := NewBridgeWithConfig(&Config{Enabled: false})
	eventID, err := b.SendToMatrix(1, 2, "hello")
	require.NoError(t, err)
	assert.Equal(t, "", eventID)
}

func TestBridge_SendGroupMessageToMatrix_NotStarted(t *testing.T) {
	b := NewBridgeWithConfig(&Config{Enabled: false})
	eventID, err := b.SendGroupMessageToMatrix("!room:example.com", 1, "hello")
	require.NoError(t, err)
	assert.Equal(t, "", eventID)
}

func TestBridge_SyncGroupToMatrix_NotStarted(t *testing.T) {
	b := NewBridgeWithConfig(&Config{Enabled: false})
	roomID, err := b.SyncGroupToMatrix(1)
	require.NoError(t, err)
	assert.Equal(t, "", roomID)
}

// ====================================================================
// Bridge.handleEvent (via HandleTransaction with manual started flag)
// ====================================================================

func TestBridge_HandleTransaction_RoomCreateIgnored(t *testing.T) {
	b := NewBridgeWithConfig(&Config{Enabled: false})
	b.started.Store(true)

	// RoomCreate events are ignored (no msgBridge so room.message would
	// nil-deref, but room.create is handled before that).
	err := b.HandleTransaction("txn", []Event{
		{Type: EventTypeRoomCreate, EventID: "$c1"},
	})
	require.NoError(t, err)
}

func TestBridge_HandleTransaction_UnknownEventIgnored(t *testing.T) {
	b := NewBridgeWithConfig(&Config{Enabled: false})
	b.started.Store(true)

	err := b.HandleTransaction("txn", []Event{
		{Type: "m.unknown.event", EventID: "$u1"},
	})
	require.NoError(t, err)
}

func TestBridge_HandleTransaction_RoomMemberJoinNoProfile(t *testing.T) {
	b := NewBridgeWithConfig(&Config{Enabled: false})
	b.started.Store(true)

	// A member join event with no displayname/avatar -> userMapper is nil,
	// but the branch that calls UpdateUserProfile is skipped.
	err := b.HandleTransaction("txn", []Event{
		{
			Type:    EventTypeRoomMember,
			EventID: "$m1",
			Sender:  "@alice:example.com",
			Content: map[string]any{"membership": "join"},
		},
	})
	require.NoError(t, err)
}

func TestBridge_HandleTransaction_RoomMemberLeaveIgnored(t *testing.T) {
	b := NewBridgeWithConfig(&Config{Enabled: false})
	b.started.Store(true)

	err := b.HandleTransaction("txn", []Event{
		{
			Type:    EventTypeRoomMember,
			EventID: "$m2",
			Sender:  "@alice:example.com",
			Content: map[string]any{"membership": "leave"},
		},
	})
	require.NoError(t, err)
}

// ====================================================================
// init.go: IsEnabled / IsRunning / GetBridge
// ====================================================================

func TestIsEnabled(t *testing.T) {
	// GlobalConfig set by setupTestGlobalConfig previously.
	setupTestGlobalConfig("http://unused")
	assert.True(t, IsEnabled())

	GlobalConfig = Config{Enabled: false}
	assert.False(t, IsEnabled())
}

func TestIsRunning_NilBridge(t *testing.T) {
	// Ensure global bridge is nil (we never set it in these unit tests).
	assert.False(t, IsRunning())
}

func TestGetBridge_Nil(t *testing.T) {
	assert.Nil(t, GetBridge())
}

// ====================================================================
// init.go: Init() when disabled
// ====================================================================

func TestInit_Disabled(t *testing.T) {
	// Because Init uses sync.Once, it will only run once per process.
	// We test the disabled path by ensuring IsEnabled is false -> Init
	// returns nil without starting.
	GlobalConfig = Config{Enabled: false}
	err := Init()
	// Init uses sync.Once; if it already ran in another test it's a no-op.
	// Either way, no error and bridge is nil.
	assert.NoError(t, err)
}
