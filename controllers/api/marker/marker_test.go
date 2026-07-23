package marker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callHandler recovers from panic("abort") used by controllers.AbortIf/AbortIfError.
// Non-abort panics are re-raised.
func callHandler(fn func(*gin.Context), c *gin.Context) {
	defer func() {
		if r := recover(); r != nil && r != "abort" {
			panic(r)
		}
	}()

	fn(c)
}

var testDB *testutil.TestDB

func TestMain(m *testing.M) {
	database.SetTestMode()
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func setupTestDB(t *testing.T) {
	t.Helper()

	var err error

	testDB, err = testutil.NewIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}

	database.SetTestDB(testDB.GormDB)

	if err := testDB.AutoMigrate(
		&models.Account{},
		&models.AccountExtra{},
		&models.Marker{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}
}

// createTestAccount creates a test account in the DB.
func createTestAccount(t *testing.T, username string) *models.Account { //nolint:unparam // param reserved for future use
	t.Helper()

	now := time.Now()

	account := &models.Account{
		Username:    username,
		DisplayName: "Test " + username,
		Locked:      false,
		Bot:         false,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	err := testDB.GormDB.Create(account).Error
	if err != nil {
		t.Fatalf("Failed to create account: %v", err)
	}

	return account
}

// ========== List Tests ==========

func TestList_NoTimelines(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/markers", nil) //nolint:noctx
	c.Set("account", createTestAccount(t, "testuser"))

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	// No timelines requested, so empty map
	assert.Empty(t, resp)
}

func TestList_TimelinesNotFound(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/markers?timelines[]=home&timelines[]=notifications", nil) //nolint:noctx
	c.Set("account", createTestAccount(t, "testuser"))

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	// Should have entries for both timelines but with empty/zero values
	assert.Contains(t, resp, "home")
	assert.Contains(t, resp, "notifications")
}

func TestList_ExistingMarker(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "testuser")

	// Create a marker with timeline data
	now := time.Now()

	marker := &models.Marker{
		AccountId: account.Id,
		Timelines: map[string]*models.MarkerTimelineItem{
			"home": { //nolint:goconst
				LastReadId: 12345,
				UpdatedAt:  &now,
				Version:    1,
			},
			"notifications": { //nolint:goconst
				LastReadId: 67890,
				UpdatedAt:  &now,
				Version:    3,
			},
		},
	}
	if err := testDB.GormDB.Create(marker).Error; err != nil {
		t.Fatalf("Failed to create marker: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/markers?timelines[]=home", nil) //nolint:noctx
	c.Set("account", account)

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]json.RawMessage

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "home")
	assert.NotContains(t, resp, "notifications")

	var homeItem models.MarkerTimelineItem

	err = json.Unmarshal(resp["home"], &homeItem)
	require.NoError(t, err)
	assert.Equal(t, uint64(12345), homeItem.LastReadId)
	assert.Equal(t, uint64(1), homeItem.Version)
}

func TestList_ExistingMarker_MultipleTimelines(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "testuser")

	// Create a marker with only "home" timeline
	now := time.Now()

	marker := &models.Marker{
		AccountId: account.Id,
		Timelines: map[string]*models.MarkerTimelineItem{
			"home": {
				LastReadId: 999,
				UpdatedAt:  &now,
				Version:    1,
			},
		},
	}
	if err := testDB.GormDB.Create(marker).Error; err != nil {
		t.Fatalf("Failed to create marker: %v", err)
	}

	// Request both home and notifications - notifications doesn't exist so should be empty
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/markers?timelines[]=home&timelines[]=notifications", nil) //nolint:noctx
	c.Set("account", account)

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]json.RawMessage

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Len(t, resp, 2)

	var homeItem models.MarkerTimelineItem

	err = json.Unmarshal(resp["home"], &homeItem)
	require.NoError(t, err)
	assert.Equal(t, uint64(999), homeItem.LastReadId)

	// notifications should have zero values since it doesn't exist
	var notifItem models.MarkerTimelineItem

	err = json.Unmarshal(resp["notifications"], &notifItem)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), notifItem.LastReadId)
}

// ========== Save Tests ==========

func TestSave_CreateNewMarker(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "testuser")

	body := SaveRequest{
		"home": SaveRequestMarkerItem{
			LastReadId: "12345",
		},
		"notifications": SaveRequestMarkerItem{
			LastReadId: "67890",
		},
	}
	bodyBytes, _ := json.Marshal(body) //nolint:errchkjson // test data is safe

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/markers", bytes.NewReader(bodyBytes)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Save, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]json.RawMessage

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp, "home")
	assert.Contains(t, resp, "notifications")

	var homeItem models.MarkerTimelineItem

	err = json.Unmarshal(resp["home"], &homeItem)
	require.NoError(t, err)
	assert.Equal(t, uint64(12345), homeItem.LastReadId)
	assert.Equal(t, uint64(1), homeItem.Version)

	// Verify marker was persisted in DB
	var dbMarker models.Marker

	err = testDB.GormDB.Where("account_id = ?", account.Id).First(&dbMarker).Error
	require.NoError(t, err)
	assert.Equal(t, account.Id, dbMarker.AccountId)
	assert.Contains(t, dbMarker.Timelines, "home")
	assert.Equal(t, uint64(12345), dbMarker.Timelines["home"].LastReadId)
}

func TestSave_UpdateExistingMarker(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "testuser")

	// Create an existing marker
	now := time.Now()

	marker := &models.Marker{
		AccountId: account.Id,
		Timelines: map[string]*models.MarkerTimelineItem{
			"home": {
				LastReadId: 100,
				UpdatedAt:  &now,
				Version:    2,
			},
		},
	}
	if err := testDB.GormDB.Create(marker).Error; err != nil {
		t.Fatalf("Failed to create marker: %v", err)
	}

	// Save an update with a new last_read_id and a new timeline
	body := SaveRequest{
		"home": SaveRequestMarkerItem{
			LastReadId: "200",
		},
		"notifications": SaveRequestMarkerItem{
			LastReadId: "300",
		},
	}
	bodyBytes, _ := json.Marshal(body) //nolint:errchkjson // test data is safe

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/markers", bytes.NewReader(bodyBytes)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Save, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]json.RawMessage

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)

	var homeItem models.MarkerTimelineItem

	err = json.Unmarshal(resp["home"], &homeItem)
	require.NoError(t, err)
	assert.Equal(t, uint64(200), homeItem.LastReadId)
	// Version should have been incremented from 2 to 3
	assert.Equal(t, uint64(3), homeItem.Version)

	// Verify in DB
	var dbMarker models.Marker

	err = testDB.GormDB.Where("account_id = ?", account.Id).First(&dbMarker).Error
	require.NoError(t, err)
	assert.Equal(t, uint64(200), dbMarker.Timelines["home"].LastReadId)
	assert.Equal(t, uint64(300), dbMarker.Timelines["notifications"].LastReadId)
	assert.Equal(t, uint64(3), dbMarker.Timelines["home"].Version)
	assert.Equal(t, uint64(1), dbMarker.Timelines["notifications"].Version)
}

func TestSave_InvalidLastReadId(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "testuser")

	body := SaveRequest{
		"home": SaveRequestMarkerItem{
			LastReadId: "not-a-number",
		},
	}
	bodyBytes, _ := json.Marshal(body) //nolint:errchkjson // test data is safe

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/markers", bytes.NewReader(bodyBytes)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Save, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSave_InvalidJSON(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "testuser")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/markers", bytes.NewReader([]byte("invalid json"))) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Save, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSave_NoAccountInContext(t *testing.T) {
	setupTestDB(t)

	body := SaveRequest{
		"home": SaveRequestMarkerItem{
			LastReadId: "12345",
		},
	}
	bodyBytes, _ := json.Marshal(body) //nolint:errchkjson // test data is safe

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/markers", bytes.NewReader(bodyBytes)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	// No account set in context - MustGet will panic

	assert.Panics(t, func() {
		callHandler(Save, c)
	})
}

func TestList_NoAccountInContext(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/markers?timelines[]=home", nil) //nolint:noctx
	// No account set in context - MustGet will panic

	assert.Panics(t, func() {
		callHandler(List, c)
	})
}

func TestSave_EmptyBody(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "testuser")

	// Empty JSON object is valid but creates no timeline entries
	bodyBytes := []byte("{}")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/markers", bytes.NewReader(bodyBytes)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Save, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]json.RawMessage

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Empty(t, resp)
}

func TestSave_NegativeLastReadId(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "testuser")

	body := SaveRequest{
		"home": SaveRequestMarkerItem{
			LastReadId: "-1",
		},
	}
	bodyBytes, _ := json.Marshal(body) //nolint:errchkjson // test data is safe

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/markers", bytes.NewReader(bodyBytes)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Save, c)

	// ParseUint with negative number should fail -> 400
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
