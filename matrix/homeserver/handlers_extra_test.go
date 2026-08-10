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

// ===== getUserLeaveTS tests (DB-backed) =====

func TestGetUserLeaveTS_NoMember(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomMember{}, &MatrixEvent{}))
	ts := getUserLeaveTS("!noroom:test.local", "@nobody:test.local")
	assert.Equal(t, int64(0), ts)
}

func TestGetUserLeaveTS_StillJoined(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomMember{}, &MatrixEvent{}))
	member := &MatrixRoomMember{
		RoomID:     "!joined:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	ts := getUserLeaveTS("!joined:test.local", "@alice:test.local")
	assert.Equal(t, int64(0), ts, "joined member should return 0")
}

func TestGetUserLeaveTS_LeftButNoEventID(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomMember{}, &MatrixEvent{}))
	member := &MatrixRoomMember{
		RoomID:     "!leftnoevt:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipLeave,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	ts := getUserLeaveTS("!leftnoevt:test.local", "@alice:test.local")
	assert.Equal(t, int64(0), ts, "left member without event_id should return 0")
}

func TestGetUserLeaveTS_LeftWithEvent(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomMember{}, &MatrixEvent{}))

	evt := &MatrixEvent{
		EventID:        "$leaveevt:test.local",
		RoomID:         "!left:test.local",
		EventType:      EventTypeRoomMember,
		Sender:         "@alice:test.local",
		OriginServerTS: 1234567890,
	}
	require.NoError(t, db.GetDB().Create(evt).Error)

	member := &MatrixRoomMember{
		RoomID:     "!left:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipLeave,
		EventID:    "$leaveevt:test.local",
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	ts := getUserLeaveTS("!left:test.local", "@alice:test.local")
	assert.Equal(t, int64(1234567890), ts)
}

func TestGetUserLeaveTS_BannedWithEvent(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomMember{}, &MatrixEvent{}))

	evt := &MatrixEvent{
		EventID:        "$banevt:test.local",
		RoomID:         "!banned:test.local",
		EventType:      EventTypeRoomMember,
		Sender:         "@mod:test.local",
		OriginServerTS: 9999999,
	}
	require.NoError(t, db.GetDB().Create(evt).Error)

	member := &MatrixRoomMember{
		RoomID:     "!banned:test.local",
		UserID:     "@badguy:test.local",
		Membership: MembershipBan,
		EventID:    "$banevt:test.local",
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	ts := getUserLeaveTS("!banned:test.local", "@badguy:test.local")
	assert.Equal(t, int64(9999999), ts)
}

// ===== getStateAtTime / getFullStateAtTime tests (DB-backed, empty room) =====

func TestGetStateAtTime_NotFound(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}))
	result, matErr := getStateAtTime("!noroom:test.local", "m.room.name", "", 1000)
	assert.Nil(t, result)
	require.NotNil(t, matErr)
	assert.Equal(t, "M_NOT_FOUND", matErr.ErrCode)
}

func TestGetStateAtTime_Found(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}))

	evt := &MatrixEvent{
		EventID:        "$stateevt:test.local",
		RoomID:         "!stateroom:test.local",
		EventType:      EventType("m.room.name"),
		Sender:         "@alice:test.local",
		StateKey:       strPtr(""),
		OriginServerTS: 500,
		Content:        map[string]any{"name": "My Room"},
	}
	require.NoError(t, db.GetDB().Create(evt).Error)

	result, matErr := getStateAtTime("!stateroom:test.local", "m.room.name", "", 1000)
	require.Nil(t, matErr)
	require.NotNil(t, result)
	assert.Equal(t, "$stateevt:test.local", result.EventID)
	assert.Equal(t, "My Room", result.Content["name"])
}

func TestGetStateAtTime_TooNew(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixEvent{}))

	// Event with ts=2000; query with ts=1000 → not found (event is too new).
	evt := &MatrixEvent{
		EventID:        "$future:test.local",
		RoomID:         "!futureroom:test.local",
		EventType:      EventType("m.room.name"),
		Sender:         "@alice:test.local",
		StateKey:       strPtr(""),
		OriginServerTS: 2000,
	}
	require.NoError(t, db.GetDB().Create(evt).Error)

	result, matErr := getStateAtTime("!futureroom:test.local", "m.room.name", "", 1000)
	assert.Nil(t, result)
	require.NotNil(t, matErr)
}

func TestGetFullStateAtTime_EmptyRoom(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomState{}, &MatrixEvent{}))
	result := getFullStateAtTime("!empty:test.local", 1000)
	assert.Empty(t, result)
}

func TestGetFullStateAtTime_WithState(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomState{}, &MatrixEvent{}))

	// Insert a room state entry and the corresponding event.
	stateEntry := &MatrixRoomState{
		RoomID:    "!fullstate:test.local",
		EventType: "m.room.name",
		StateKey:  "",
	}
	require.NoError(t, db.GetDB().Create(stateEntry).Error)

	evt := &MatrixEvent{
		EventID:        "$fullstateevt:test.local",
		RoomID:         "!fullstate:test.local",
		EventType:      EventType("m.room.name"),
		Sender:         "@alice:test.local",
		StateKey:       strPtr(""),
		OriginServerTS: 500,
		Content:        map[string]any{"name": "Full State Room"},
	}
	require.NoError(t, db.GetDB().Create(evt).Error)

	result := getFullStateAtTime("!fullstate:test.local", 1000)
	require.Len(t, result, 1)
	assert.Equal(t, "Full State Room", result[0].Content["name"])
}

// ===== matrixAuthMiddleware tests =====

func TestMatrixAuthMiddleware_NoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(matrixAuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		t.Error("next handler should not be called without token")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "M_MISSING_TOKEN")
}

func TestMatrixAuthMiddleware_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.Use(matrixAuthMiddleware())
	r.GET("/test", func(c *gin.Context) {
		t.Error("next handler should not be called with invalid token")
	})

	// Provide a Bearer token that doesn't match any session.
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-token-xyz")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Invalid token → 401.
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ===== handleLogin bad JSON =====

func TestHandleLogin_BadJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/login", handleLogin)

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "M_BAD_JSON")
}

// ===== handleRefresh bad JSON =====

func TestHandleRefresh_BadJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/refresh", handleRefresh)

	req := httptest.NewRequest(http.MethodPost, "/refresh", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "M_BAD_JSON")
}

// ===== handleRegister invalid UTF-8 =====

func TestHandleRegister_InvalidUTF8(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/register", handleRegister)

	// Invalid UTF-8 byte sequence.
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader("\xff\xfe\xfd"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "M_NOT_JSON")
	assert.Contains(t, w.Body.String(), "invalid UTF-8")
}

func TestHandleRegister_BadJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/register", handleRegister)

	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader("not json at all"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ===== handleGetPushers without auth =====

func TestHandleGetPushers_EmptyUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/pushers", handleGetPushers)

	req := httptest.NewRequest(http.MethodGet, "/pushers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Without user_id in context, GetPushers returns empty list → 200.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "pushers")
}

// ===== strPtr helper =====

func strPtr(s string) *string {
	return &s
}
