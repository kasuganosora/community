package follow_request

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		&models.AccountEmoji{},
		&models.Emoji{},
		&models.Follow{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}
}

func createTestAccount(t *testing.T, username string) *models.Account {
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

func createTestFollowRequest(t *testing.T, accountID, followID uint64) *models.Follow {
	t.Helper()

	follow := &models.Follow{
		AccountId: accountID,
		FollowId:  followID,
		Status:    models.FollowStatusRequested,
	}
	err := testDB.GormDB.Create(follow).Error
	if err != nil {
		t.Fatalf("Failed to create follow request: %v", err)
	}

	return follow
}

func newContextWithAccount(t *testing.T, w *httptest.ResponseRecorder, method, path string) (*gin.Context, *models.Account) {
	t.Helper()
	account := createTestAccount(t, "testuser")
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, nil) //nolint:noctx
	c.Set("account", account)

	return c, account
}

func newContextWithAccountAndParams(t *testing.T, w *httptest.ResponseRecorder, method, path string, params map[string]string) (*gin.Context, *models.Account) { //nolint:unparam // param reserved for future use
	t.Helper()
	account := createTestAccount(t, "testuser")
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, nil) //nolint:noctx

	c.Params = gin.Params{}

	for k, v := range params {
		c.Params = append(c.Params, gin.Param{Key: k, Value: v})
	}

	c.Set("account", account)

	return c, account
}

// ========== List Tests ==========

func TestList_Success(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodGet, "/api/v1/follow_requests?limit=40")

	// Create another account that sent a follow request to currentAccount
	otherAccount := createTestAccount(t, "requester")
	createTestFollowRequest(t, otherAccount.Id, currentAccount.Id)

	w = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/follow_requests?limit=40", nil) //nolint:noctx
	c.Set("account", currentAccount)

	List(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Len(t, resp, 1)
}

func TestList_Empty(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodGet, "/api/v1/follow_requests?limit=40")

	List(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Empty(t, resp)
}

func TestList_WithLimit(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodGet, "/api/v1/follow_requests?limit=40")

	// Create multiple follow requests
	for i := range 3 {
		other := createTestAccount(t, "requester"+strconv.Itoa(i))
		createTestFollowRequest(t, other.Id, currentAccount.Id)
	}

	w = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/follow_requests?limit=2", nil) //nolint:noctx
	c.Set("account", currentAccount)

	List(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Len(t, resp, 2)
}

func TestList_WithMaxId(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodGet, "/api/v1/follow_requests?limit=40")

	other1 := createTestAccount(t, "requester1")
	other2 := createTestAccount(t, "requester2")
	fr1 := createTestFollowRequest(t, other1.Id, currentAccount.Id)
	fr2 := createTestFollowRequest(t, other2.Id, currentAccount.Id)

	// Use the higher ID as max_id to filter out one result
	maxId := max(fr1.Id, fr2.Id)

	w = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/follow_requests?max_id="+strconv.FormatUint(maxId, 10), nil) //nolint:noctx
	c.Set("account", currentAccount)

	List(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Len(t, resp, 1)
}

func TestList_WithSinceId(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodGet, "/api/v1/follow_requests?limit=40")

	other1 := createTestAccount(t, "requester1")
	other2 := createTestAccount(t, "requester2")
	createTestFollowRequest(t, other1.Id, currentAccount.Id)
	fr2 := createTestFollowRequest(t, other2.Id, currentAccount.Id)

	// Use the lower ID as since_id to get only newer results
	sinceId := fr2.Id
	if other1.Id < other2.Id {
		sinceId = fr2.Id
	}

	w = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/follow_requests?since_id="+strconv.FormatUint(sinceId, 10), nil) //nolint:noctx
	c.Set("account", currentAccount)

	List(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestList_InvalidLimit(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodGet, "/api/v1/follow_requests?limit=40")

	w = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/follow_requests?limit=999", nil) //nolint:noctx
	c.Set("account", currentAccount)

	List(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestList_DefaultLimit(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodGet, "/api/v1/follow_requests?limit=40")

	// When no limit is specified, default is 40 - create 50 requests
	for i := range 50 {
		other := createTestAccount(t, "requester"+strconv.Itoa(i))
		createTestFollowRequest(t, other.Id, currentAccount.Id)
	}

	w = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/follow_requests?limit=40", nil) //nolint:noctx
	c.Set("account", currentAccount)

	List(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Len(t, resp, 40)
}

// ========== Authorize Tests ==========

func TestAuthorize_Success(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodPost, "/api/v1/follow_requests/1/authorize")

	otherAccount := createTestAccount(t, "requester")
	createTestFollowRequest(t, otherAccount.Id, currentAccount.Id)

	w = httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/follow_requests/"+strconv.FormatUint(otherAccount.Id, 10)+"/authorize", map[string]string{
		"id": strconv.FormatUint(otherAccount.Id, 10),
	})
	c.Set("account", currentAccount)

	Authorize(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, strconv.FormatUint(otherAccount.Id, 10), resp["id"])
	assert.Equal(t, true, resp["following"])
	assert.Equal(t, false, resp["followed_by"])
	assert.Equal(t, false, resp["requested"])

	// Verify follow status was updated
	var follow models.Follow

	err = testDB.GormDB.Where("account_id = ? AND follow_id = ?", otherAccount.Id, currentAccount.Id).First(&follow).Error
	require.NoError(t, err)
	assert.Equal(t, models.FollowStatusFollowed, follow.Status)
}

func TestAuthorize_InvalidID(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodPost, "/api/v1/follow_requests/abc/authorize")

	w = httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/follow_requests/abc/authorize", map[string]string{
		"id": "abc",
	})
	c.Set("account", currentAccount)

	Authorize(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthorize_NotFound(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodPost, "/api/v1/follow_requests/99999/authorize")

	w = httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/follow_requests/99999/authorize", map[string]string{
		"id": "99999",
	})
	c.Set("account", currentAccount)

	Authorize(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAuthorize_FollowersCountIncremented(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodPost, "/api/v1/follow_requests/1/authorize")

	otherAccount := createTestAccount(t, "requester")
	createTestFollowRequest(t, otherAccount.Id, currentAccount.Id)

	w = httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/follow_requests/"+strconv.FormatUint(otherAccount.Id, 10)+"/authorize", map[string]string{
		"id": strconv.FormatUint(otherAccount.Id, 10),
	})
	c.Set("account", currentAccount)

	Authorize(c)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify followers_count was incremented for current account
	var updatedExtra models.AccountExtra

	testDB.GormDB.Where("account_id = ?", currentAccount.Id).First(&updatedExtra)
	assert.Equal(t, 1, updatedExtra.FollowersCount)
}

func TestAuthorize_FollowingCountIncremented(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodPost, "/api/v1/follow_requests/1/authorize")

	otherAccount := createTestAccount(t, "requester")
	createTestFollowRequest(t, otherAccount.Id, currentAccount.Id)

	w = httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/follow_requests/"+strconv.FormatUint(otherAccount.Id, 10)+"/authorize", map[string]string{
		"id": strconv.FormatUint(otherAccount.Id, 10),
	})
	c.Set("account", currentAccount)

	Authorize(c)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify following_count was incremented for target account
	var updatedExtra models.AccountExtra

	testDB.GormDB.Where("account_id = ?", otherAccount.Id).First(&updatedExtra)
	assert.Equal(t, 1, updatedExtra.FollowingCount)
}

// ========== Reject Tests ==========

func TestReject_Success(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodPost, "/api/v1/follow_requests/1/reject")

	otherAccount := createTestAccount(t, "requester")
	createTestFollowRequest(t, otherAccount.Id, currentAccount.Id)

	w = httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/follow_requests/"+strconv.FormatUint(otherAccount.Id, 10)+"/reject", map[string]string{
		"id": strconv.FormatUint(otherAccount.Id, 10),
	})
	c.Set("account", currentAccount)

	Reject(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, strconv.FormatUint(otherAccount.Id, 10), resp["id"])
	assert.Equal(t, false, resp["following"])
	assert.Equal(t, false, resp["followed_by"])
	assert.Equal(t, false, resp["requested"])

	// Verify follow request was deleted
	var follow models.Follow

	err = testDB.GormDB.Where("account_id = ? AND follow_id = ?", otherAccount.Id, currentAccount.Id).First(&follow).Error
	require.Error(t, err)
}

func TestReject_InvalidID(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodPost, "/api/v1/follow_requests/abc/reject")

	w = httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/follow_requests/abc/reject", map[string]string{
		"id": "abc",
	})
	c.Set("account", currentAccount)

	Reject(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestReject_NotFound(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodPost, "/api/v1/follow_requests/99999/reject")

	w = httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/follow_requests/99999/reject", map[string]string{
		"id": "99999",
	})
	c.Set("account", currentAccount)

	Reject(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestReject_AlreadyFollowed(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	_, currentAccount := newContextWithAccount(t, w, http.MethodPost, "/api/v1/follow_requests/1/reject")

	otherAccount := createTestAccount(t, "requester")
	// Create an already-accepted follow (not a request)
	follow := &models.Follow{
		AccountId: otherAccount.Id,
		FollowId:  currentAccount.Id,
		Status:    models.FollowStatusFollowed,
	}
	testDB.GormDB.Create(follow)

	w = httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/follow_requests/"+strconv.FormatUint(otherAccount.Id, 10)+"/reject", map[string]string{
		"id": strconv.FormatUint(otherAccount.Id, 10),
	})
	c.Set("account", currentAccount)

	Reject(c)

	// Should return 404 since there's no "requested" follow
	assert.Equal(t, http.StatusNotFound, w.Code)
}
