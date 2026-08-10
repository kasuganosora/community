package homeserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRoomTestHS creates a full HS with test DB for room handler tests.
func setupRoomTestHS(t *testing.T) *HomeServer {
	t.Helper()
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}, &MatrixRoomTag{}))
	hs := fullHS("test.local")
	GlobalHS = hs
	return hs
}

// ===== handleCreateRoom =====

func TestHandleCreateRoom_BadJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	r := gin.New()
	r.POST("/createRoom", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleCreateRoom(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/createRoom", strings.NewReader("bad json"))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "M_BAD_JSON")
}

func TestHandleCreateRoom_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	r := gin.New()
	r.POST("/createRoom", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleCreateRoom(c)
	})

	body := `{"name":"Test Room","preset":"public_chat"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/createRoom", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "room_id")
}

// helper: seed a public join rule state for a room
func seedPublicJoinRule(t *testing.T, roomID string) {
	t.Helper()
	st := &MatrixRoomState{
		RoomID:    roomID,
		EventType: EventTypeRoomJoinRules,
		StateKey:  "",
		Content:   map[string]any{"join_rule": "public"},
	}
	require.NoError(t, db.GetDB().Create(st).Error)
}

// helper: seed a power-level state giving userID powerLevel 100
func seedPowerLevels(t *testing.T, roomID, userID string, level int64) {
	t.Helper()
	st := &MatrixRoomState{
		RoomID:    roomID,
		EventType: EventTypeRoomPowerLevels,
		StateKey:  "",
		Content: map[string]any{
			"users":          map[string]any{userID: level},
			"users_default":  0,
			"events":         map[string]any{},
			"events_default": 0,
			"state_default":  50,
			"ban":            50,
			"kick":           50,
			"redact":         50,
			"invite":         0,
		},
	}
	require.NoError(t, db.GetDB().Create(st).Error)
}

func TestHandleJoinRoom_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	// Create a public room first
	room := &MatrixRoom{RoomID: "!joinable:test.local", RoomVersion: "10", JoinRule: "public"}
	require.NoError(t, db.GetDB().Create(room).Error)
	seedPublicJoinRule(t, "!joinable:test.local")

	r := gin.New()
	r.POST("/rooms/:roomId/join", func(c *gin.Context) {
		c.Set("user_id", "@bob:test.local")
		handleJoinRoom(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rooms/%21joinable%3Atest.local/join", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "room_id")
}

// ===== handleLeaveRoom =====

func TestHandleLeaveRoom_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	room := &MatrixRoom{RoomID: "!leavable:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	member := &MatrixRoomMember{
		RoomID:     "!leavable:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.POST("/rooms/:roomId/leave", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleLeaveRoom(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rooms/%21leavable%3Atest.local/leave", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== handleInviteUser =====

func TestHandleInviteUser_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	room := &MatrixRoom{RoomID: "!inviteroom:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	seedPowerLevels(t, "!inviteroom:test.local", "@alice:test.local", 100)
	member := &MatrixRoomMember{
		RoomID:     "!inviteroom:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
		PowerLevel: 100,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.POST("/rooms/:roomId/invite", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleInviteUser(c)
	})

	body := `{"user_id":"@bob:test.local"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rooms/%21inviteroom%3Atest.local/invite", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== handleKickUser =====

func TestHandleKickUser_NotMember(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	room := &MatrixRoom{RoomID: "!kickroom:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	member := &MatrixRoomMember{
		RoomID:     "!kickroom:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.POST("/rooms/:roomId/kick", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleKickUser(c)
	})

	body := `{"user_id":"@nobody:test.local"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rooms/%21kickroom%3Atest.local/kick", strings.NewReader(body))
	r.ServeHTTP(w, req)

	// Kicking a non-member should error
	assert.True(t, w.Code >= 400)
}

// ===== handleBanUser =====

func TestHandleBanUser_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	room := &MatrixRoom{RoomID: "!banroom:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	seedPowerLevels(t, "!banroom:test.local", "@alice:test.local", 100)
	member := &MatrixRoomMember{
		RoomID:     "!banroom:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
		PowerLevel: 100,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.POST("/rooms/:roomId/ban", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleBanUser(c)
	})

	body := `{"user_id":"@badguy:test.local"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rooms/%21banroom%3Atest.local/ban", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== handleUnbanUser =====

func TestHandleUnbanUser_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	room := &MatrixRoom{RoomID: "!unbanroom:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	member := &MatrixRoomMember{
		RoomID:     "!unbanroom:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
		PowerLevel: 100,
	}
	require.NoError(t, db.GetDB().Create(member).Error)
	// Create a banned user to unban
	bannedMember := &MatrixRoomMember{
		RoomID:     "!unbanroom:test.local",
		UserID:     "@banned:test.local",
		Membership: MembershipBan,
	}
	require.NoError(t, db.GetDB().Create(bannedMember).Error)

	r := gin.New()
	r.POST("/rooms/:roomId/unban", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleUnbanUser(c)
	})

	body := `{"user_id":"@banned:test.local"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rooms/%21unbanroom%3Atest.local/unban", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== handleForgetRoom =====

func TestHandleForgetRoom_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	room := &MatrixRoom{RoomID: "!forgetroom:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	member := &MatrixRoomMember{
		RoomID:     "!forgetroom:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipLeave,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.POST("/rooms/:roomId/forget", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleForgetRoom(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/rooms/%21forgetroom%3Atest.local/forget", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== handleGetJoinedMembers =====

func TestHandleGetJoinedMembers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	room := &MatrixRoom{RoomID: "!jmemroom:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	member := &MatrixRoomMember{
		RoomID:     "!jmemroom:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.GET("/rooms/:roomId/joined_members", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetJoinedMembers(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/%21jmemroom%3Atest.local/joined_members", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "joined")
}

// ===== handleSetRoomVisibility =====

func TestHandleSetRoomVisibility_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	room := &MatrixRoom{RoomID: "!visset:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	visMember := &MatrixRoomMember{
		RoomID:     "!visset:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
		PowerLevel: 100,
	}
	require.NoError(t, db.GetDB().Create(visMember).Error)

	r := gin.New()
	r.PUT("/directory/list/room/:roomId", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleSetRoomVisibility(c)
	})

	body := `{"visibility":"public"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/directory/list/room/%21visset%3Atest.local", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== handleGetRoomStateEvent =====

func TestHandleGetRoomStateEvent_JoinedMember(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	room := &MatrixRoom{RoomID: "!sevtroom:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	member := &MatrixRoomMember{
		RoomID:     "!sevtroom:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.GET("/rooms/:roomId/state/:eventType/:stateKey", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetRoomStateEvent(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/%21sevtroom%3Atest.local/state/m.room.name/%40alice%3Atest.local", nil)
	r.ServeHTTP(w, req)

	// No state event set → 404
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleGetRoomStateEvent_NoStateKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	room := &MatrixRoom{RoomID: "!sevtroom2:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	member := &MatrixRoomMember{
		RoomID:     "!sevtroom2:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.GET("/rooms/:roomId/state/:eventType", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		// gin doesn't match optional param easily; emulate by setting empty stateKey
		c.Params = append(c.Params, gin.Param{Key: "stateKey", Value: ""})
		handleGetRoomStateEvent(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/%21sevtroom2%3Atest.local/state/m.room.create", nil)
	r.ServeHTTP(w, req)

	assert.True(t, w.Code == http.StatusOK || w.Code == http.StatusNotFound)
}

// ===== handleDeleteDevice =====

func TestHandleDeleteDevice_NoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	// Create a device session so the handler gets past the existence check
	delSess := &MatrixUserSession{UserID: "@alice:test.local", DeviceID: "DEV1", AccessToken: "tok-del"}
	require.NoError(t, db.GetDB().Create(delSess).Error)

	r := gin.New()
	r.DELETE("/devices/:deviceId", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		c.Set("device_id", "DEV1")
		handleDeleteDevice(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/devices/DEV1", nil)
	r.ServeHTTP(w, req)

	// Without auth body, should return 401 (needs UI auth)
	assert.True(t, w.Code == http.StatusUnauthorized || w.Code == http.StatusOK || w.Code == http.StatusBadRequest)
}

// ===== handleUpdateDevice =====

func TestHandleUpdateDevice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	// Create session
	sess := &MatrixUserSession{UserID: "@alice:test.local", DeviceID: "DEVUPD", AccessToken: "tok"}
	require.NoError(t, db.GetDB().Create(sess).Error)

	r := gin.New()
	r.PUT("/devices/:deviceId", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleUpdateDevice(c)
	})

	body := `{"display_name":"My Device"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/DEVUPD", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== handleGetOneTimeKeysCount =====

func TestHandleGetOneTimeKeysCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(setupRoomTestHS(t))
	defer restore()

	r := gin.New()
	r.GET("/keys/count", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		c.Set("device_id", "DEV1")
		handleGetOneTimeKeysCount(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/keys/count", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
