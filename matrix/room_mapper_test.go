package matrix

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ====================================================================
// RoomMapper construction
// ====================================================================

func TestNewRoomMapper(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "a"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	require.NotNil(t, rm)
	assert.Equal(t, c, rm.client)
	assert.Equal(t, um, rm.userMapper)
	assert.NotNil(t, rm.cache)
}

func TestRoomMapper_ClearCache(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	// ClearCache on empty cache should not panic.
	rm.ClearCache()

	// Seed cache then clear.
	room := &models.MatrixRoom{ConversationId: 1, MatrixRoomID: "!r:example.com"}
	rm.cacheRoom(room)
	rm.ClearCache()

	rm.cache.RLock()
	_, ok := rm.cache.convToRoom[1]
	rm.cache.RUnlock()
	assert.False(t, ok, "cache should be empty after ClearCache")
}

// ====================================================================
// cacheRoom
// ====================================================================

func TestRoomMapper_CacheRoom(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	room := &models.MatrixRoom{ConversationId: 42, MatrixRoomID: "!room:example.com"}
	rm.cacheRoom(room)

	rm.cache.RLock()
	gotConv, okConv := rm.cache.convToRoom[42]
	gotRoom, okRoom := rm.cache.roomToConv["!room:example.com"]
	rm.cache.RUnlock()

	assert.True(t, okConv)
	assert.Equal(t, room, gotConv)
	assert.True(t, okRoom)
	assert.Equal(t, room, gotRoom)
}

// ====================================================================
// GetOrCreateRoomByConversation — cache hit
// ====================================================================

func TestRoomMapper_GetOrCreateRoomByConversation_CacheHit(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	room := &models.MatrixRoom{ConversationId: 7, MatrixRoomID: "!cached:example.com"}
	rm.cacheRoom(room)

	got, err := rm.GetOrCreateRoomByConversation(7, nil)
	require.NoError(t, err)
	assert.Equal(t, room, got)
}

// ====================================================================
// GetOrCreateRoomByConversation — DB hit
// ====================================================================

func TestRoomMapper_GetOrCreateRoomByConversation_DBHit(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	existing := &models.MatrixRoom{
		ConversationId:      10,
		MatrixRoomID:        "!dbroom:example.com",
		MatrixRoomLocalpart: "dbroom",
		MatrixServer:        "example.com",
		IsDirect:            true,
	}
	require.NoError(t, db.Create(existing).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	got, err := rm.GetOrCreateRoomByConversation(10, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "!dbroom:example.com", got.MatrixRoomID)
	assert.Equal(t, uint64(10), got.ConversationId)
}

// ====================================================================
// GetOrCreateConversationByRoom — cache hit
// ====================================================================

func TestRoomMapper_GetOrCreateConversationByRoom_CacheHit(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	room := &models.MatrixRoom{ConversationId: 99, MatrixRoomID: "!convroom:example.com"}
	rm.cacheRoom(room)

	got, err := rm.GetOrCreateConversationByRoom("!convroom:example.com", nil)
	require.NoError(t, err)
	assert.Equal(t, room, got)
}

// ====================================================================
// GetOrCreateConversationByRoom — DB hit
// ====================================================================

func TestRoomMapper_GetOrCreateConversationByRoom_DBHit(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	existing := &models.MatrixRoom{
		ConversationId:      20,
		MatrixRoomID:        "!convdb:example.com",
		MatrixRoomLocalpart: "convdb",
		MatrixServer:        "example.com",
		IsDirect:            true,
	}
	require.NoError(t, db.Create(existing).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	got, err := rm.GetOrCreateConversationByRoom("!convdb:example.com", nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, uint64(20), got.ConversationId)
}

// ====================================================================
// GetConversationID (cache + DB lookups)
// ====================================================================

func TestRoomMapper_GetConversationID_CacheHit(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	room := &models.MatrixRoom{ConversationId: 55, MatrixRoomID: "!gid:example.com"}
	rm.cacheRoom(room)

	convID, err := rm.GetConversationID("!gid:example.com")
	require.NoError(t, err)
	assert.Equal(t, uint64(55), convID)
}

func TestRoomMapper_GetConversationID_DBHit(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	existing := &models.MatrixRoom{
		ConversationId:      77,
		MatrixRoomID:        "!convlook:example.com",
		MatrixRoomLocalpart: "convlook",
		MatrixServer:        "example.com",
	}
	require.NoError(t, db.Create(existing).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	convID, err := rm.GetConversationID("!convlook:example.com")
	require.NoError(t, err)
	assert.Equal(t, uint64(77), convID)
}

func TestRoomMapper_GetConversationID_NotFound(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	_, err := rm.GetConversationID("!nonexistent:example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ====================================================================
// GetMatrixRoomID (cache + DB lookups)
// ====================================================================

func TestRoomMapper_GetMatrixRoomID_CacheHit(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	room := &models.MatrixRoom{ConversationId: 33, MatrixRoomID: "!mid:example.com"}
	rm.cacheRoom(room)

	roomID, err := rm.GetMatrixRoomID(33)
	require.NoError(t, err)
	assert.Equal(t, "!mid:example.com", roomID)
}

func TestRoomMapper_GetMatrixRoomID_DBHit(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	existing := &models.MatrixRoom{
		ConversationId:      44,
		MatrixRoomID:        "!roomlook:example.com",
		MatrixRoomLocalpart: "roomlook",
		MatrixServer:        "example.com",
	}
	require.NoError(t, db.Create(existing).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	roomID, err := rm.GetMatrixRoomID(44)
	require.NoError(t, err)
	assert.Equal(t, "!roomlook:example.com", roomID)
}

func TestRoomMapper_GetMatrixRoomID_NotFound(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	_, err := rm.GetMatrixRoomID(99999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ====================================================================
// UpdateLastSync
// ====================================================================

func TestRoomMapper_UpdateLastSync(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	existing := &models.MatrixRoom{
		ConversationId:      88,
		MatrixRoomID:        "!syncroom:example.com",
		MatrixRoomLocalpart: "syncroom",
		MatrixServer:        "example.com",
	}
	require.NoError(t, db.Create(existing).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	err := rm.UpdateLastSync("!syncroom:example.com", "$event123")
	require.NoError(t, err)

	// Verify DB updated.
	var updated models.MatrixRoom
	require.NoError(t, db.Where("matrix_room_id = ?", "!syncroom:example.com").First(&updated).Error)
	assert.Equal(t, "$event123", updated.LastEventID)
	require.NotNil(t, updated.LastSyncAt)
}

func TestRoomMapper_UpdateLastSync_CacheUpdate(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	existing := &models.MatrixRoom{
		ConversationId:      89,
		MatrixRoomID:        "!syncroom2:example.com",
		MatrixRoomLocalpart: "syncroom2",
		MatrixServer:        "example.com",
	}
	require.NoError(t, db.Create(existing).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	// Seed cache.
	rm.cacheRoom(existing)

	err := rm.UpdateLastSync("!syncroom2:example.com", "$evt456")
	require.NoError(t, err)

	// Verify cache updated.
	rm.cache.RLock()
	cached := rm.cache.roomToConv["!syncroom2:example.com"]
	rm.cache.RUnlock()
	assert.Equal(t, "$evt456", cached.LastEventID)
	require.NotNil(t, cached.LastSyncAt)
}

// ====================================================================
// IsDirectRoom
// ====================================================================

func TestRoomMapper_IsDirectRoom_True(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	existing := &models.MatrixRoom{
		ConversationId:      66,
		MatrixRoomID:        "!direct:example.com",
		MatrixRoomLocalpart: "direct",
		MatrixServer:        "example.com",
		IsDirect:            true,
	}
	require.NoError(t, db.Create(existing).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	isDirect, err := rm.IsDirectRoom("!direct:example.com")
	require.NoError(t, err)
	assert.True(t, isDirect)
}

func TestRoomMapper_IsDirectRoom_NotFound(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	_, err := rm.IsDirectRoom("!nope:example.com")
	require.Error(t, err)
}

// ====================================================================
// GetRoomInfo (mock HS)
// ====================================================================

func TestRoomMapper_GetRoomInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chunk": []map[string]any{
				{
					"type":      "m.room.member",
					"sender":    "@alice:example.com",
					"state_key": "@alice:example.com",
					"content":   map[string]any{"membership": "join"},
				},
				{
					"type":      "m.room.member",
					"sender":    "@bob:example.com",
					"state_key": "@bob:example.com",
					"content":   map[string]any{"membership": "leave"},
				},
				{
					"type":    "m.room.message",
					"sender":  "@alice:example.com",
					"content": map[string]any{"body": "hi"},
				},
			},
		})
	}))
	defer srv.Close()

	c := NewASClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	info, err := rm.GetRoomInfo("!inforoom:example.com")
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "!inforoom:example.com", info.RoomID)
	assert.Len(t, info.Members, 2)
	assert.Equal(t, "join", info.Members["@alice:example.com"])
	assert.Equal(t, "leave", info.Members["@bob:example.com"])
}

func TestRoomMapper_GetRoomInfo_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewASClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	info, err := rm.GetRoomInfo("!errroom:example.com")
	require.Error(t, err)
	assert.Nil(t, info)
}

// ====================================================================
// GetOrCreateRoomByConversation — create new room (full path)
// ====================================================================

func TestRoomMapper_GetOrCreateRoomByConversation_CreateNew(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	// Create a local conversation for the room mapper to find.
	conv := &models.Conversation{AccountsIds: []uint64{1, 2}}
	require.NoError(t, db.Create(conv).Error)

	// Mock HS: accept registration + room creation.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"room_id": "!newdirect:example.com"})
	}))
	defer srv.Close()

	c := NewASClientWithConfig(&Config{HomeserverURL: srv.URL, ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	got, err := rm.GetOrCreateRoomByConversation(conv.Id, []string{"@bob:example.com"})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "!newdirect:example.com", got.MatrixRoomID)
	assert.Equal(t, conv.Id, got.ConversationId)
	assert.True(t, got.IsDirect)
}

func TestRoomMapper_GetOrCreateRoomByConversation_NoParticipants(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	_, err := rm.GetOrCreateRoomByConversation(12345, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no participants")
}

// ====================================================================
// GetOrCreateConversationByRoom — create new conversation (full path)
// ====================================================================

func TestRoomMapper_GetOrCreateConversationByRoom_CreateNew(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	// Create a local account that the user mapper will find.
	account := &models.Account{Username: "dave"}
	require.NoError(t, db.Create(account).Error)

	// Create matrix user mapping so GetLocalAccountID finds it.
	matrixUser := &models.MatrixUser{
		LocalAccountId:  account.Id,
		MatrixUserID:    "@dave:example.com",
		MatrixLocalpart: "dave",
		MatrixServer:    "example.com",
		Direction:       "remote",
	}
	require.NoError(t, db.Create(matrixUser).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	got, err := rm.GetOrCreateConversationByRoom("!newconv:example.com", []string{"@dave:example.com"})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "!newconv:example.com", got.MatrixRoomID)
	assert.True(t, got.IsDirect)
}

func TestRoomMapper_GetOrCreateConversationByRoom_NoParticipants(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "as"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)

	// Only bridge user as participant -> skipped -> no valid participants.
	_, err := rm.GetOrCreateConversationByRoom("!empty:example.com", []string{"@_bridge:example.com"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid participants")
}
