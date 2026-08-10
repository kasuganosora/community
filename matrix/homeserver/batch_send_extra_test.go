package homeserver

import (
	"testing"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== BatchSendService.extractDomain tests =====

func TestBatchSendService_ExtractDomain_ValidUserID(t *testing.T) {
	setupMinimalTestDB(t)
	bss := NewBatchSendService(minimalHS("test.local"))

	domain := bss.extractDomain("@alice:example.com")
	assert.Equal(t, "example.com", domain)
}

func TestBatchSendService_ExtractDomain_InvalidUserID(t *testing.T) {
	setupMinimalTestDB(t)
	bss := NewBatchSendService(minimalHS("fallback.local"))

	// Invalid user ID (no @ prefix) → falls back to server name
	domain := bss.extractDomain("not-a-valid-user-id")
	assert.Equal(t, "fallback.local", domain)
}

func TestBatchSendService_ExtractDomain_LocalUser(t *testing.T) {
	setupMinimalTestDB(t)
	bss := NewBatchSendService(minimalHS("myserver.test"))

	domain := bss.extractDomain("@bob:myserver.test")
	assert.Equal(t, "myserver.test", domain)
}

// ===== BatchSendService.getLatestEvents tests =====

func TestBatchSendService_GetLatestEvents_NoRoom(t *testing.T) {
	setupMinimalTestDB(t)
	bss := NewBatchSendService(minimalHS("test.local"))

	events := bss.getLatestEvents("!nonexistent:test.local")
	assert.Empty(t, events)
}

func TestBatchSendService_GetLatestEvents_WithLastEventID(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}))
	bss := NewBatchSendService(minimalHS("test.local"))

	lastEventID := "$evt123:test.local"
	room := &MatrixRoom{
		RoomID:      "!testroom:test.local",
		RoomVersion: "10",
		LastEventID: &lastEventID,
	}
	require.NoError(t, db.GetDB().Create(room).Error)

	events := bss.getLatestEvents("!testroom:test.local")
	assert.Len(t, events, 1)
	assert.Equal(t, lastEventID, events[0])
}

func TestBatchSendService_GetLatestEvents_EmptyLastEventID(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}))
	bss := NewBatchSendService(minimalHS("test.local"))

	emptyID := ""
	room := &MatrixRoom{
		RoomID:      "!emptylast:test.local",
		RoomVersion: "10",
		LastEventID: &emptyID,
	}
	require.NoError(t, db.GetDB().Create(room).Error)

	events := bss.getLatestEvents("!emptylast:test.local")
	assert.Empty(t, events)
}

// ===== BatchSendService.getNextDepth tests =====

func TestBatchSendService_GetNextDepth_NoEvents(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}))
	bss := NewBatchSendService(minimalHS("test.local"))

	depth := bss.getNextDepth("!noroom:test.local")
	assert.Equal(t, int64(1), depth, "empty room should have depth 1")
}

func TestBatchSendService_GetNextDepth_WithEvents(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}))
	bss := NewBatchSendService(minimalHS("test.local"))

	// Insert an event with depth 5
	evt := &MatrixEvent{
		EventID:   "$depth5:test.local",
		RoomID:    "!depthroom:test.local",
		EventType: EventTypeRoomMessage,
		Sender:    "@alice:test.local",
		Depth:     5,
	}
	require.NoError(t, db.GetDB().Create(evt).Error)

	depth := bss.getNextDepth("!depthroom:test.local")
	assert.Equal(t, int64(6), depth, "next depth should be 6 after an event at depth 5")
}

// ===== BatchSendService.BatchSend: room not found =====

func TestBatchSendService_BatchSend_RoomNotFound(t *testing.T) {
	setupMinimalTestDB(t)
	bss := NewBatchSendService(minimalHS("test.local"))

	resp, err := bss.BatchSend("!nonexistent:test.local", &BatchSendRequest{
		Events: []BatchEvent{
			{Type: "m.room.message", Sender: "@alice:test.local", Content: map[string]any{"body": "hi"}},
		},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "room not found")
}

func TestBatchSendService_BatchSend_EmptyEvents(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}))
	bss := NewBatchSendService(minimalHS("test.local"))
	bss.hs.Room = NewRoomService(bss.hs)

	room := &MatrixRoom{
		RoomID:      "!emptybatch:test.local",
		RoomVersion: "10",
	}
	require.NoError(t, db.GetDB().Create(room).Error)

	resp, err := bss.BatchSend("!emptybatch:test.local", &BatchSendRequest{
		Events: []BatchEvent{},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.EventIDs)
}

// ===== NewBatchSendService =====

func TestNewBatchSendService(t *testing.T) {
	hs := minimalHS("test.local")
	bss := NewBatchSendService(hs)
	assert.NotNil(t, bss)
	assert.Equal(t, hs, bss.hs)
}

// ===== BatchSendRequest / BatchEvent struct tests =====

func TestBatchSendRequest_Fields(t *testing.T) {
	req := &BatchSendRequest{
		Events:      []BatchEvent{{Type: "m.room.message"}},
		StateEvents: []BatchEvent{{Type: "m.room.create"}},
		PrevEventID: "$prev:test.local",
	}
	assert.Len(t, req.Events, 1)
	assert.Len(t, req.StateEvents, 1)
	assert.Equal(t, "$prev:test.local", req.PrevEventID)
}

func TestBatchEvent_Fields(t *testing.T) {
	stateKey := ""
	evt := BatchEvent{
		Type:      "m.room.member",
		Sender:    "@alice:test.local",
		Content:   map[string]any{"membership": "join"},
		Timestamp: 1234567890,
		StateKey:  &stateKey,
	}
	assert.Equal(t, "m.room.member", evt.Type)
	assert.Equal(t, "@alice:test.local", evt.Sender)
	assert.Equal(t, int64(1234567890), evt.Timestamp)
	assert.NotNil(t, evt.StateKey)
}

func TestBatchSendResponse_Fields(t *testing.T) {
	resp := &BatchSendResponse{
		EventIDs: []string{"$a:1", "$b:2"},
	}
	assert.Len(t, resp.EventIDs, 2)
}

// ===== device_list helpers =====

func TestParseUserID_WithDomain(t *testing.T) {
	domain := parseUserID("@alice:example.com")
	assert.Equal(t, "example.com", domain)
}

func TestParseUserID_NoColon(t *testing.T) {
	domain := parseUserID("invaliduserid")
	assert.Equal(t, "", domain)
}

func TestParseUserID_Empty(t *testing.T) {
	domain := parseUserID("")
	assert.Equal(t, "", domain)
}

func TestIsLocalUser_Local(t *testing.T) {
	assert.True(t, isLocalUser("@alice:myserver.test", "myserver.test"))
}

func TestIsLocalUser_Remote(t *testing.T) {
	assert.False(t, isLocalUser("@alice:other.server", "myserver.test"))
}

func TestIsLocalUser_InvalidFormat(t *testing.T) {
	assert.False(t, isLocalUser("invalid", "myserver.test"))
}

func TestNewDeviceListService(t *testing.T) {
	hs := minimalHS("test.local")
	dls := NewDeviceListService(hs)
	assert.NotNil(t, dls)
	assert.Equal(t, hs, dls.hs)
}
