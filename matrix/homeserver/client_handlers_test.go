package homeserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== handleGetJoinedRooms tests =====

func TestHandleGetJoinedRooms_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/joined_rooms", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetJoinedRooms(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/joined_rooms", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "joined_rooms")
}

// ===== handleGetDevices tests =====

func TestHandleGetDevices_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/devices", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetDevices(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "devices")
}

// ===== handleCreateFilter tests =====

func TestHandleCreateFilter_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/user/:userId/filter", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleCreateFilter(c)
	})

	body := `{"room":{"timeline":{"limit":10}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/%40alice%3Atest.local/filter", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "filter_id")
}

func TestHandleCreateFilter_DifferentUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/user/:userId/filter", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleCreateFilter(c)
	})

	body := `{}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/%40bob%3Atest.local/filter", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "M_FORBIDDEN")
}

func TestHandleCreateFilter_NotObject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/user/:userId/filter", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleCreateFilter(c)
	})

	body := `[1,2,3]`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/%40alice%3Atest.local/filter", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "M_BAD_JSON")
}

func TestHandleCreateFilter_InvalidSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/user/:userId/filter", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleCreateFilter(c)
	})

	body := `{"presence":"notobject"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/%40alice%3Atest.local/filter", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "must be an object")
}

// ===== handleGetFilter tests =====

func TestHandleGetFilter_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/user/:userId/filter/:filterId", handleGetFilter)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/user/%40alice%3Atest.local/filter/nonexistent", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "M_NOT_FOUND")
}

func TestHandleGetFilter_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	// Create a filter first
	filter := &MatrixFilter{
		UserID:   "@alice:test.local",
		FilterID: "f123",
		Filter:   `{"room":{"timeline":{"limit":10}}}`,
	}
	require.NoError(t, db.GetDB().Create(filter).Error)

	r := gin.New()
	r.GET("/user/:userId/filter/:filterId", handleGetFilter)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/user/%40alice%3Atest.local/filter/f123", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	room, ok := resp["room"].(map[string]any)
	require.True(t, ok)
	timeline := room["timeline"].(map[string]any)
	assert.Equal(t, float64(10), timeline["limit"])
}

// ===== handleGetRoomTags tests =====

func TestHandleGetRoomTags_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/user/:userId/rooms/:roomId/tags", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetRoomTags(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/user/%40alice%3Atest.local/rooms/%21room%3Atest.local/tags", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "tags")
}

// ===== handleGetProfile tests =====

func TestHandleGetProfile_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/profile/:userId", handleGetProfile)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/profile/%40nonexistent%3Atest.local", nil)
	r.ServeHTTP(w, req)

	// User not found → 404
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ===== handleGetDisplayName tests =====

func TestHandleGetDisplayName_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/profile/:userId/displayname", handleGetDisplayName)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/profile/%40alice%3Atest.local/displayname", nil)
	r.ServeHTTP(w, req)

	// User not in accounts table → 404
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "M_NOT_FOUND")
}

// ===== handleGetAvatarURL tests =====

func TestHandleGetAvatarURL_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/profile/:userId/avatar_url", handleGetAvatarURL)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/profile/%40alice%3Atest.local/avatar_url", nil)
	r.ServeHTTP(w, req)

	// User not in accounts table → 404
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ===== handleGetPushRules tests =====

func TestHandleGetPushRules(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/pushrules", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetPushRules(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pushrules", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== getUserID tests =====

func TestGetUserID_Set(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("user_id", "@alice:test.local")
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	assert.Equal(t, "@alice:test.local", getUserID(c))
}

func TestGetUserID_NotSet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	assert.Equal(t, "", getUserID(c))
}

// ===== handleGetRoomVisibility tests =====

func TestHandleGetRoomVisibility_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/directory/list/room/:roomId", handleGetRoomVisibility)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/directory/list/room/%21room%3Atest.local", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleGetRoomVisibility_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	room := &MatrixRoom{RoomID: "!visroom:test.local", RoomVersion: "10", Visibility: "public"}
	require.NoError(t, db.GetDB().Create(room).Error)

	r := gin.New()
	r.GET("/directory/list/room/:roomId", handleGetRoomVisibility)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/directory/list/room/%21visroom%3Atest.local", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "visibility")
}

// ===== handleGetRoomSummary tests =====

func TestHandleGetRoomSummary_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(minimalHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/rooms/:roomId/summary", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetRoomSummary(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/%21nonexist%3Atest.local/summary", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ===== handleGetPushRules with full HS =====

func TestHandleGetPushRules_FullHS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/pushrules", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetPushRules(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pushrules", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleGetPushRulesGlobal_FullHS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/pushrules/global", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetPushRulesGlobal(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pushrules/global", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleGetPushRulesScope_FullHS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/pushrules/global/:kind", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetPushRulesScope(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pushrules/global/override", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleGetPushers_FullHS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/pushers", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetPushers(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/pushers", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "pushers")
}

func TestHandleSetPusher_FullHS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/pushers/set", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		c.Set("device_id", "DEV1")
		handleSetPusher(c)
	})

	body := `{"pushkey":"https://example.com/push","kind":"http","app_id":"myapp","app_display_name":"App","device_display_name":"Dev","lang":"en"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pushers/set", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleSetPusher_BadJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/pushers/set", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleSetPusher(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pushers/set", strings.NewReader("bad json"))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ===== handleGetDevices with full HS =====

func TestHandleGetDevices_FullHS_WithSessions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	// Create a session
	sess := &MatrixUserSession{
		UserID:   "@alice:test.local",
		DeviceID: "DEV1",
	}
	require.NoError(t, db.GetDB().Create(sess).Error)

	r := gin.New()
	r.GET("/devices", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetDevices(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "DEV1")
}

// ===== handleGetDevice =====

func TestHandleGetDevice_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/devices/:deviceId", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetDevice(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/devices/UNKNOWN", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleGetDevice_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	sess := &MatrixUserSession{
		UserID:   "@alice:test.local",
		DeviceID: "DEVFOUND",
	}
	require.NoError(t, db.GetDB().Create(sess).Error)

	r := gin.New()
	r.GET("/devices/:deviceId", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetDevice(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/devices/DEVFOUND", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== handleGetJoinedRooms with rooms =====

func TestHandleGetJoinedRooms_WithRoom(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	// Create room and member
	room := &MatrixRoom{RoomID: "!joined:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	member := &MatrixRoomMember{
		RoomID:     "!joined:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.GET("/joined_rooms", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetJoinedRooms(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/joined_rooms", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "!joined:test.local")
}

// ===== handleGetRoomTags with tags =====

func TestHandleGetRoomTags_WithTag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoomTag{}))
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	tag := &MatrixRoomTag{
		UserID:  "@alice:test.local",
		RoomID:  "!tagged:test.local",
		Tag:     "favourite",
		Content: `{"order":0.1}`,
	}
	require.NoError(t, db.GetDB().Create(tag).Error)

	r := gin.New()
	r.GET("/user/:userId/rooms/:roomId/tags", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetRoomTags(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/user/%40alice%3Atest.local/rooms/%21tagged%3Atest.local/tags", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "favourite")
}

// ===== handleRegisterAvailability =====

func TestHandleRegisterAvailability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/register/available", handleRegisterAvailability)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/register/available", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "registration_available")
}

// ===== handleUsernameAvailable =====

func TestHandleUsernameAvailable_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/username_available", handleUsernameAvailable)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/username_available?username=", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "M_INVALID_USERNAME")
}

func TestHandleUsernameAvailable_Invalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/username_available", handleUsernameAvailable)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/username_available?username=invalid%20name", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleUsernameAvailable_Available(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/username_available", handleUsernameAvailable)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/username_available?username=newuser123", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "available")
}

// ===== handleAccount =====

func TestHandleAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/account", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleAccount(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "@alice:test.local")
}

// ===== handleGetRoomState (joined member) =====

func TestHandleGetRoomState_JoinedMember(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	// Create room and joined member
	room := &MatrixRoom{RoomID: "!stateroom:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	member := &MatrixRoomMember{
		RoomID:     "!stateroom:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.GET("/rooms/:roomId/state", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetRoomState(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/%21stateroom%3Atest.local/state", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleGetRoomState_NotMember(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/rooms/:roomId/state", func(c *gin.Context) {
		c.Set("user_id", "@stranger:test.local")
		handleGetRoomState(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/%21someroom%3Atest.local/state", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "M_FORBIDDEN")
}

// ===== handleGetMessages =====

func TestHandleGetMessages_JoinedMember(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	room := &MatrixRoom{RoomID: "!msgroom:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	member := &MatrixRoomMember{
		RoomID:     "!msgroom:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.GET("/rooms/:roomId/messages", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetMessages(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/%21msgroom%3Atest.local/messages?dir=b&limit=10", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ===== handleGetRoomMembers =====

func TestHandleGetRoomMembers_JoinedMember(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	room := &MatrixRoom{RoomID: "!memroom:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)
	member := &MatrixRoomMember{
		RoomID:     "!memroom:test.local",
		UserID:     "@alice:test.local",
		Membership: MembershipJoin,
	}
	require.NoError(t, db.GetDB().Create(member).Error)

	r := gin.New()
	r.GET("/rooms/:roomId/members", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetRoomMembers(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/%21memroom%3Atest.local/members", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "chunk")
}

// ===== handleGetRoom (room exists) =====

func TestHandleGetRoom_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	room := &MatrixRoom{RoomID: "!getroom:test.local", RoomVersion: "10"}
	require.NoError(t, db.GetDB().Create(room).Error)

	r := gin.New()
	r.GET("/rooms/:roomId", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetRoom(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/%21getroom%3Atest.local", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "room_id")
}

// ===== handleGetEvent (not found) =====

func TestHandleGetEvent_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/rooms/:roomId/event/:eventId", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetEvent(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/%21room%3Atest.local/event/%24nonexistent%3Atest.local", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
