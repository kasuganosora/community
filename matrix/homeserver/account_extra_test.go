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

// ===== generateSID / generateResetToken tests =====

func TestGenerateSID(t *testing.T) {
	sid1 := generateSID()
	sid2 := generateSID()
	assert.NotEmpty(t, sid1)
	assert.NotEqual(t, sid1, sid2, "generateSID should produce unique values")
}

func TestGenerateResetToken(t *testing.T) {
	token1 := generateResetToken()
	token2 := generateResetToken()
	assert.NotEmpty(t, token1)
	assert.NotEqual(t, token1, token2, "generateResetToken should produce unique values")
	// Reset token is two UUIDs concatenated → 72 chars.
	assert.Len(t, token1, 72)
}

// ===== AccountService.GetAccountData tests =====

func TestAccountService_GetAccountData_NotFound(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixAccountData{}))
	hs := minimalHS("test.local")
	as := NewAccountService(hs)

	result, err := as.GetAccountData("@nobody:test.local", "m.foo", nil)
	require.Error(t, err)
	assert.Nil(t, result)
}

func TestAccountService_GetAccountData_Found(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixAccountData{}))
	hs := minimalHS("test.local")
	as := NewAccountService(hs)

	rec := &MatrixAccountData{
		UserID:   "@alice:test.local",
		DataType: "m.custom",
		Content:  map[string]any{"key": "value"},
	}
	require.NoError(t, db.GetDB().Create(rec).Error)

	result, err := as.GetAccountData("@alice:test.local", "m.custom", nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "m.custom", result.Type)
	assert.Equal(t, "value", result.Content["key"])
}

func TestAccountService_GetAccountData_RoomScoped(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixAccountData{}))
	hs := minimalHS("test.local")
	as := NewAccountService(hs)

	roomID := "!room:test.local"
	rec := &MatrixAccountData{
		UserID:   "@alice:test.local",
		DataType: "m.room.data",
		Content:  map[string]any{"x": float64(1)},
		RoomID:   &roomID,
	}
	require.NoError(t, db.GetDB().Create(rec).Error)

	result, err := as.GetAccountData("@alice:test.local", "m.room.data", &roomID)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, float64(1), result.Content["x"])
}

// ===== AccountService.SetAccountData tests =====

func TestAccountService_SetAccountData_Create(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixAccountData{}))
	hs := minimalHS("test.local")
	as := NewAccountService(hs)

	err := as.SetAccountData("@alice:test.local", "m.new", nil, map[string]any{"a": "b"})
	require.NoError(t, err)

	// Verify it was stored.
	result, err := as.GetAccountData("@alice:test.local", "m.new", nil)
	require.NoError(t, err)
	assert.Equal(t, "b", result.Content["a"])
}

func TestAccountService_SetAccountData_Update(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixAccountData{}))
	hs := minimalHS("test.local")
	as := NewAccountService(hs)

	// Create
	require.NoError(t, as.SetAccountData("@alice:test.local", "m.upd", nil, map[string]any{"v": "1"}))
	// Update
	require.NoError(t, as.SetAccountData("@alice:test.local", "m.upd", nil, map[string]any{"v": "2"}))

	result, err := as.GetAccountData("@alice:test.local", "m.upd", nil)
	require.NoError(t, err)
	assert.Equal(t, "2", result.Content["v"])
}

// ===== AccountService.Whoami tests =====

func TestAccountService_Whoami_InvalidUserID(t *testing.T) {
	setupMinimalTestDB(t)
	hs := minimalHS("test.local")
	as := NewAccountService(hs)

	result, err := as.Whoami("invalid")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid user ID")
}

func TestAccountService_Whoami_UserNotFound(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixAccountData{}))
	// Need to migrate the Account model from the models package, but Whoami
	// queries models.Account. Without it, the query fails → "user not found".
	hs := minimalHS("test.local")
	as := NewAccountService(hs)

	result, err := as.Whoami("@ghost:test.local")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "user not found")
}

// ===== handleWhoami handler test =====

func TestHandleWhoami_NoUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/account/whoami", handleWhoami)

	req := httptest.NewRequest(http.MethodGet, "/account/whoami", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Empty user_id → Whoami fails → 404.
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "M_NOT_FOUND")
}

func TestHandleWhoami_InvalidUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/account/whoami", func(c *gin.Context) {
		c.Set("user_id", "invalid")
		handleWhoami(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/account/whoami", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ===== handleGetAccountData tests =====

func TestHandleGetAccountData_ForbiddenOtherUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/user/:userId/account_data/:type", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetAccountData(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/user/%40bob%3Atest.local/account_data/m.foo", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "M_FORBIDDEN")
}

func TestHandleGetAccountData_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixAccountData{}))
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/user/:userId/account_data/:type", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetAccountData(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/user/%40alice%3Atest.local/account_data/m.nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "M_NOT_FOUND")
}

func TestHandleGetAccountData_Found(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixAccountData{}))
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	rec := &MatrixAccountData{
		UserID:   "@alice:test.local",
		DataType: "m.found",
		Content:  map[string]any{"hello": "world"},
	}
	require.NoError(t, db.GetDB().Create(rec).Error)

	r := gin.New()
	r.GET("/user/:userId/account_data/:type", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetAccountData(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/user/%40alice%3Atest.local/account_data/m.found", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "world")
}

// ===== handleSetAccountData tests =====

func TestHandleSetAccountData_ForbiddenOtherUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.PUT("/user/:userId/account_data/:type", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleSetAccountData(c)
	})

	req := httptest.NewRequest(http.MethodPut, "/user/%40bob%3Atest.local/account_data/m.foo", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandleSetAccountData_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixAccountData{}))
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.PUT("/user/:userId/account_data/:type", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleSetAccountData(c)
	})

	body := `{"key":"val"}`
	req := httptest.NewRequest(http.MethodPut, "/user/%40alice%3Atest.local/account_data/m.settest", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleSetAccountData_BadJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixAccountData{}))
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.PUT("/user/:userId/account_data/:type", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleSetAccountData(c)
	})

	req := httptest.NewRequest(http.MethodPut, "/user/%40alice%3Atest.local/account_data/m.badjson", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "M_BAD_JSON")
}

// ===== handleChangePassword tests =====

func TestHandleChangePassword_BadJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/account/password", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleChangePassword(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/account/password", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "M_BAD_JSON")
}

func TestHandleChangePassword_NoAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.POST("/account/password", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleChangePassword(c)
	})

	// Valid JSON but no auth → starts UIA flow (401 with session).
	body := `{"new_password":"newpass123"}`
	req := httptest.NewRequest(http.MethodPost, "/account/password", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "session")
	assert.Contains(t, w.Body.String(), "m.login.password")
}

// ===== handleGetRoomAccountData tests =====

func TestHandleGetRoomAccountData_ForbiddenOtherUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.GET("/user/:userId/rooms/:roomId/account_data/:type", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleGetRoomAccountData(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/user/%40bob%3Atest.local/rooms/%21room%3Atest.local/account_data/m.foo", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ===== handleSetRoomAccountData tests =====

func TestHandleSetRoomAccountData_ForbiddenOtherUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	restore := resetGlobalHS(fullHS("test.local"))
	defer restore()

	r := gin.New()
	r.PUT("/user/:userId/rooms/:roomId/account_data/:type", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleSetRoomAccountData(c)
	})

	req := httptest.NewRequest(http.MethodPut, "/user/%40bob%3Atest.local/rooms/%21room%3Atest.local/account_data/m.foo", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
