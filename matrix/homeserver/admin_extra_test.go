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

// ===== AdminService tests =====

func TestNewAdminService(t *testing.T) {
	hs := minimalHS("test.local")
	as := NewAdminService(hs)
	assert.NotNil(t, as)
	assert.Equal(t, hs, as.hs)
}

func TestAdminService_GetServerStats_Empty(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}, &MatrixRoom{}, &MatrixEvent{}, &MatrixPresence{}))
	hs := minimalHS("test.local")
	as := NewAdminService(hs)

	stats, err := as.GetServerStats()
	require.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Equal(t, int64(0), stats.TotalUsers)
	assert.Equal(t, int64(0), stats.TotalRooms)
	assert.Equal(t, int64(0), stats.TotalEvents)
}

func TestAdminService_ListUsers_Empty(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	hs := minimalHS("test.local")
	as := NewAdminService(hs)

	users, total, err := as.ListUsers(0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, users)
}

func TestAdminService_GetUserInfo_NotFound(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	hs := minimalHS("test.local")
	as := NewAdminService(hs)

	info, err := as.GetUserInfo("@nonexistent:test.local")
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "user not found")
}

func TestAdminService_DeactivateUser(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}, &MatrixRoomMember{}, &MatrixAccountData{}, &MatrixFilter{}))
	hs := minimalHS("test.local")
	hs.Account = NewAccountService(hs)
	as := NewAdminService(hs)

	err := as.DeactivateUser("@nobody:test.local", false)
	// Should not error on non-existent user
	assert.NoError(t, err)
}

func TestAdminService_ResetUserPassword(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixUserSession{}))
	hs := minimalHS("test.local")
	hs.Account = NewAccountService(hs)
	as := NewAdminService(hs)

	// updatePassword will likely error for non-existent user, but won't panic
	_ = as.ResetUserPassword("@nobody:test.local", "newpassword")
}

func TestAdminService_MakeUserAdmin(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixAccountData{}))
	hs := minimalHS("test.local")
	as := NewAdminService(hs)

	err := as.MakeUserAdmin("@nobody:test.local", true)
	assert.NoError(t, err)
}

func TestAdminService_ListRooms_Empty(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}))
	hs := minimalHS("test.local")
	as := NewAdminService(hs)

	rooms, total, err := as.ListRooms(0, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, rooms)
}

func TestAdminService_DeleteRoom_NonExistent(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}))
	hs := minimalHS("test.local")
	as := NewAdminService(hs)

	err := as.DeleteRoom("!nonexistent:test.local")
	assert.NoError(t, err)
}

// ===== isUserAdmin tests =====

func TestIsUserAdmin_InvalidUserID(t *testing.T) {
	setupMinimalTestDB(t)
	assert.False(t, isUserAdmin("invalid"))
}

func TestIsUserAdmin_NotFound(t *testing.T) {
	setupMinimalTestDB(t)
	assert.False(t, isUserAdmin("@nobody:test.local"))
}

// ===== Admin handler tests =====

func newAdminTestHS(t *testing.T) *HomeServer {
	t.Helper()
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(
		&MatrixUserSession{}, &MatrixRoom{}, &MatrixEvent{}, &MatrixPresence{}, &MatrixAccountData{},
	))
	hs := fullHS("test.local")
	hs.Admin = NewAdminService(hs)
	return hs
}

func TestHandleGetServerStats_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newAdminTestHS(t)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.GET("/stats", handleGetServerStats)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var stats ServerStats
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &stats))
}

func TestHandleGetUserStats_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newAdminTestHS(t)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.GET("/userstats", handleGetUserStats)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/userstats", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]int64
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "total")
}

func TestHandleGetRoomStats_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newAdminTestHS(t)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.GET("/roomstats", handleGetRoomStats)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/roomstats", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleListUsers_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newAdminTestHS(t)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.GET("/users", handleListUsers)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users?offset=0&limit=10", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleListRooms_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newAdminTestHS(t)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.GET("/rooms", handleListRooms)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms?offset=0&limit=10", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleGetUserDetails_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newAdminTestHS(t)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.GET("/users/:userId", handleGetUserDetails)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/@nobody:test.local", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleResetUserPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newAdminTestHS(t)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.POST("/users/:userId/reset_password", handleResetUserPassword)

	body := `{"new_password":"newpass"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users/@nobody:test.local/reset_password", strings.NewReader(body))
	r.ServeHTTP(w, req)

	// ResetUserPassword calls updatePassword which errors for non-existent user → 500
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleMakeUserAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newAdminTestHS(t)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.POST("/users/:userId/admin", handleMakeUserAdmin)

	body := `{"admin":true}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users/@nobody:test.local/admin", strings.NewReader(body))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleDeleteRoom(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newAdminTestHS(t)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.DELETE("/rooms/:roomId", handleDeleteRoom)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/rooms/!nonexistent:test.local", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleWhoisUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hs := newAdminTestHS(t)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.POST("/whois/:userId", handleWhoisUser)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/whois/@nobody:test.local", nil)
	r.ServeHTTP(w, req)

	// Non-existent user → GetUserInfo errors → 404
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRegisterAdminRoutes_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	assert.NotPanics(t, func() {
		r := gin.New()
		RegisterAdminRoutes(r)
	})
}

// ===== ServerStats struct test =====

func TestServerStats_JSON(t *testing.T) {
	stats := ServerStats{
		TotalUsers:         10,
		TotalRooms:         5,
		TotalEvents:        100,
		DailyActiveUsers:   3,
		MonthlyActiveUsers: 7,
		OnlineUsers:        2,
	}
	data, err := json.Marshal(stats)
	require.NoError(t, err)

	var parsed map[string]int64
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, int64(10), parsed["total_users"])
	assert.Equal(t, int64(5), parsed["total_rooms"])
}

// ===== UserInfo struct test =====

func TestUserInfo_JSON(t *testing.T) {
	info := UserInfo{
		UserID:      "@alice:test.local",
		DisplayName: "Alice",
		IsAdmin:     true,
	}
	data, err := json.Marshal(info)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, "@alice:test.local", parsed["user_id"])
	assert.Equal(t, "Alice", parsed["display_name"])
	assert.Equal(t, true, parsed["is_admin"])
}

// ===== Search service tests =====

func TestSearchService_ConvertAvatarToMXC_Empty(t *testing.T) {
	hs := minimalHS("test.local")
	ss := NewSearchService(hs)
	result := ss.convertAvatarToMXC("")
	assert.Equal(t, "", result)
}

// ===== Spaces service tests =====

func TestNewSpacesService(t *testing.T) {
	hs := minimalHS("test.local")
	ss := NewSpacesService(hs)
	assert.NotNil(t, ss)
}

// ===== AppService service tests =====

func TestNewAppServiceService(t *testing.T) {
	hs := minimalHS("test.local")
	ass := NewAppServiceService(hs)
	assert.NotNil(t, ass)
}

func TestAppService_IsValidAppServiceID(t *testing.T) {
	assert.False(t, isValidAppServiceID(""))
	assert.False(t, isValidAppServiceID("   "))
	assert.True(t, isValidAppServiceID("validid"))
	assert.True(t, isValidAppServiceID("my-app_123"))
}

func TestAppService_GenerateSecureToken(t *testing.T) {
	token1 := generateSecureToken()
	assert.NotEmpty(t, token1)
	token2 := generateSecureToken()
	assert.NotEmpty(t, token2)
	assert.NotEqual(t, token1, token2, "tokens should be unique")
}

// ===== ThreePID service tests =====

func TestNewThreePIDService(t *testing.T) {
	hs := minimalHS("test.local")
	ts := NewThreePIDService(hs)
	assert.NotNil(t, ts)
}

func TestThreePID_GenerateValidationToken(t *testing.T) {
	token := generateValidationToken()
	assert.NotEmpty(t, token)
}

// ===== RoomUpgrade service tests =====

func TestNewRoomUpgradeService(t *testing.T) {
	hs := minimalHS("test.local")
	rus := NewRoomUpgradeService(hs)
	assert.NotNil(t, rus)
}

func TestRoomUpgrade_IsSupportedRoomVersion(t *testing.T) {
	assert.True(t, isSupportedRoomVersion("10"))
	assert.True(t, isSupportedRoomVersion("11"))
	assert.True(t, isSupportedRoomVersion("1"))
	assert.False(t, isSupportedRoomVersion("0"))
	assert.False(t, isSupportedRoomVersion(""))
}
