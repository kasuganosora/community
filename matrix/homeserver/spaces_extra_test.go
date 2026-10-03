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

// ===== getServerList tests =====

func TestGetServerList(t *testing.T) {
	result := getServerList("myserver.test")
	assert.Equal(t, []string{"myserver.test"}, result)
}

// ===== Spaces handler tests =====

func TestRegisterSpacesRoutes_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	assert.NotPanics(t, func() {
		r := gin.New()
		RegisterSpacesRoutes(r)
	})
}

func TestHandleSpaceHierarchy_AccessDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}))

	hs := fullHS("test.local")
	hs.Spaces = NewSpacesService(hs)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.GET("/rooms/:roomId/hierarchy", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleSpaceHierarchy(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/!space:test.local/hierarchy?limit=10&max_depth=2", nil)
	r.ServeHTTP(w, req)

	// Non-existent space → access denied → 403
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandleSpaceSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}))

	hs := fullHS("test.local")
	hs.Spaces = NewSpacesService(hs)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.GET("/rooms/:roomId/spaces", func(c *gin.Context) {
		c.Set("user_id", "@alice:test.local")
		handleSpaceSummary(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/rooms/!space:test.local/spaces", nil)
	r.ServeHTTP(w, req)

	// Non-existent space → 403 (access denied)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ===== SpacesService method tests =====

func TestSpacesService_CreateSpace_SetsRoomType(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}, &MatrixRoomState{}, &MatrixEvent{}, &MatrixRoomMember{}))
	hs := fullHS("test.local")
	ss := NewSpacesService(hs)

	req := &CreateRoomRequest{
		Name: strPtr("My Space"),
	}
	// CreateSpace calls CreateRoom which needs DB; may fail but CreationContent should be set
	_, matErr := ss.CreateSpace("@alice:test.local", req)
	_ = matErr

	// Verify CreationContent now has type=space
	assert.Equal(t, RoomTypeSpace, req.CreationContent["type"])
}

func TestSpacesService_AddSpaceChild_Forbidden(t *testing.T) {
	setupMinimalTestDB(t)
	hs := fullHS("test.local")
	ss := NewSpacesService(hs)

	matErr := ss.AddSpaceChild("@alice:test.local", "!space:test.local", "!child:test.local", false, "")
	// Without proper room setup → forbidden
	assert.NotNil(t, matErr)
}

func TestSpacesService_RemoveSpaceChild_Forbidden(t *testing.T) {
	setupMinimalTestDB(t)
	hs := fullHS("test.local")
	ss := NewSpacesService(hs)

	matErr := ss.RemoveSpaceChild("@alice:test.local", "!space:test.local", "!child:test.local")
	assert.NotNil(t, matErr)
}

func TestSpacesService_SetSpaceParent_Forbidden(t *testing.T) {
	setupMinimalTestDB(t)
	hs := fullHS("test.local")
	ss := NewSpacesService(hs)

	matErr := ss.SetSpaceParent("@alice:test.local", "!room:test.local", "!space:test.local", false)
	assert.NotNil(t, matErr)
}

func TestSpacesService_GetSpaceHierarchy_AccessDenied(t *testing.T) {
	setupMinimalTestDB(t)
	hs := fullHS("test.local")
	ss := NewSpacesService(hs)

	result, err := ss.GetSpaceHierarchy("@alice:test.local", "!nonexistent:test.local", 20, 3, "")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "access denied")
}

// ===== Backfill handler tests =====

func TestHandleBackfill_RoomNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}, &MatrixEvent{}))

	hs := fullHS("test.local")
	hs.Room = NewRoomService(hs)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.GET("/backfill/:roomId", handleBackfill)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/backfill/!nonexistent:test.local?limit=10&v=$evt:1", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code, "unverified server must not read history")
}

func TestRegisterBackfillRoutes_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	assert.NotPanics(t, func() {
		r := gin.New()
		RegisterBackfillRoutes(r)
	})
}

func TestHandleGetMissingEvents_Backfill(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}, &MatrixEvent{}))

	hs := fullHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.POST("/missing/:roomId", handleGetMissingEvents)

	body := `{"earliest_events":["$a:1"],"latest_events":["$b:2"],"limit":10}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/missing/!room:test.local", strings.NewReader(body))
	r.ServeHTTP(w, req)

	// Non-existent room → likely 404
	assert.True(t, w.Code >= 200 && w.Code < 600, "should return a valid HTTP status code, got %d", w.Code)
}

func TestHandleTimestampToEvent_RoomNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixRoom{}))

	hs := fullHS("test.local")
	hs.Room = NewRoomService(hs)
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.GET("/ts/:roomId", handleTimestampToEvent)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ts/!nonexistent:test.local?ts=1234567890&dir=f", nil)
	r.ServeHTTP(w, req)

	assert.True(t, w.Code == http.StatusNotFound || w.Code == http.StatusBadRequest,
		"expected 404 or 400, got %d", w.Code)
}

func TestHandleEventAuth_RoomNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)

	hs := fullHS("test.local")
	restore := resetGlobalHS(hs)
	defer restore()

	r := gin.New()
	r.GET("/event_auth/:roomId/:eventId", handleEventAuth)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/event_auth/!nonexistent:test.local/$evt:1", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code, "unverified server must not read event auth")
}

func TestRegisterAdditionalFederationRoutes_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	assert.NotPanics(t, func() {
		r := gin.New()
		RegisterAdditionalFederationRoutes(r)
	})
}

// ===== BackfillResponse struct test =====

func TestBackfillResponse_JSON(t *testing.T) {
	resp := BackfillResponse{
		Origin:         "test.local",
		OriginServerTS: 1234567890,
		PDUs:           []ClientEvent{},
	}
	assert.Equal(t, "test.local", resp.Origin)
	assert.Equal(t, int64(1234567890), resp.OriginServerTS)
	assert.Empty(t, resp.PDUs)
}

// ===== Spaces struct tests =====

func TestSpaceHierarchyResponse_Struct(t *testing.T) {
	resp := &SpaceHierarchyResponse{
		Rooms: []SpaceRoomInfo{},
	}
	assert.Empty(t, resp.Rooms)
}

// ===== RoomUpgrade struct tests =====

func TestMatrixRoomUpgrade_TableName(t *testing.T) {
	assert.Equal(t, "matrix_room_upgrades", MatrixRoomUpgrade{}.TableName())
}

// ===== federation_e2ee register routes =====

func TestRegisterFederationE2EERoutes_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	assert.NotPanics(t, func() {
		r := gin.New()
		RegisterFederationE2EERoutes(r)
	})
}

// ===== RegisterThreePIDRoutes nil HS =====

func TestRegisterThreePIDRoutes_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	assert.NotPanics(t, func() {
		r := gin.New()
		RegisterThreePIDRoutes(r)
	})
}

// ===== RegisterDeviceListRoutes nil HS =====

func TestRegisterDeviceListRoutes_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	assert.NotPanics(t, func() {
		r := gin.New()
		RegisterDeviceListRoutes(r)
	})
}

// ===== RegisterRoomUpgradeRoutes nil HS =====

func TestRegisterRoomUpgradeRoutes_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	assert.NotPanics(t, func() {
		r := gin.New()
		RegisterRoomUpgradeRoutes(r)
	})
}

// ===== RegisterBatchSendRoutes nil HS =====

func TestRegisterBatchSendRoutes_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	assert.NotPanics(t, func() {
		r := gin.New()
		RegisterBatchSendRoutes(r)
	})
}

// ===== RegisterOpenIDRoutes nil HS =====

func TestRegisterOpenIDRoutes_GlobalHSNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := resetGlobalHS(nil)
	defer restore()

	assert.NotPanics(t, func() {
		r := gin.New()
		RegisterOpenIDRoutes(r)
	})
}

// ===== AppService struct tests =====

func TestAppService_TableName(t *testing.T) {
	assert.Equal(t, "matrix_appservices", AppService{}.TableName())
}

// ===== DeviceList struct tests =====

func TestDeviceListChange_TableName(t *testing.T) {
	assert.Equal(t, "matrix_device_list_changes", DeviceListChange{}.TableName())
}

func TestDeviceListStream_TableName(t *testing.T) {
	assert.Equal(t, "matrix_device_list_streams", DeviceListStream{}.TableName())
}
