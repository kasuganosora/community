package contact

import (
	"bytes"
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

// callHandler recovers from panic("abort") used by controllers.AbortIf/AbortIfError.
func callHandler(fn func(*gin.Context), c *gin.Context) {
	defer func() {
		if r := recover(); r != nil && r != "abort" {
			_ = r // expected - handler aborted early
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
		&models.User{},
		&models.Account{},
		&models.AccountExtra{},
		&models.Contact{},
		&models.ContactRequest{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}
}

// createTestAccount creates a test account in the DB.
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

// newContextWithAccount creates a gin context with account set.
func newContextWithAccount(t *testing.T, w *httptest.ResponseRecorder, method, path string, body []byte) (*gin.Context, *models.Account) { //nolint:unparam // param reserved for future use
	t.Helper()
	account := createTestAccount(t, "testuser")

	c, _ := gin.CreateTestContext(w)
	if body != nil {
		c.Request = httptest.NewRequest(method, path, bytes.NewBuffer(body)) //nolint:noctx
		c.Request.Header.Set("Content-Type", "application/json")
	} else {
		c.Request = httptest.NewRequest(method, path, nil) //nolint:noctx
	}

	c.Set("account", account)

	return c, account
}

// newContextWithAccountAndParams creates a gin context with account and path params.
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

// ========== AddContact Tests ==========

func TestAddContact_Success_Local(t *testing.T) {
	setupTestDB(t)

	targetId := uint64(999)
	body, err := json.Marshal(AddContactRequest{
		ContactAccountId: &targetId,
		DisplayName:      "Test Friend",
		Message:          "Hi!",
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodPost, "/api/v1/contacts", body)

	callHandler(AddContact, c)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any

	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp["id"])
	assert.Equal(t, "local", resp["type"])
	assert.Equal(t, "Test Friend", resp["display_name"])
}

func TestAddContact_Success_Matrix(t *testing.T) {
	setupTestDB(t)

	matrixId := "@user:matrix.org"
	body, err := json.Marshal(AddContactRequest{
		MatrixUserId: &matrixId,
		DisplayName:  "Matrix User",
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodPost, "/api/v1/contacts", body)

	callHandler(AddContact, c)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any

	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "matrix", resp["type"])
}

func TestAddContact_InvalidJSON(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodPost, "/api/v1/contacts", []byte("invalid json"))

	callHandler(AddContact, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAddContact_NoIdProvided(t *testing.T) {
	setupTestDB(t)

	body, err := json.Marshal(AddContactRequest{
		DisplayName: "No ID",
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodPost, "/api/v1/contacts", body)

	callHandler(AddContact, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp["error"], "Either contact_account_id or matrix_user_id")
}

func TestAddContact_DuplicateContact(t *testing.T) {
	setupTestDB(t)

	targetId := uint64(999)
	account := createTestAccount(t, "owner")

	// Pre-create the contact
	contact := &models.Contact{
		AccountId:        account.Id,
		ContactType:      models.ContactTypeLocal,
		ContactAccountId: &targetId,
		Status:           models.ContactStatusAccepted,
	}
	testDB.GormDB.Create(contact)

	body, err := json.Marshal(AddContactRequest{
		ContactAccountId: &targetId,
		DisplayName:      "Dup",
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/contacts", bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(AddContact, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp["error"], "already exists")
}

// ========== GetContacts Tests ==========

func TestGetContacts_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "owner")
	targetId := uint64(999)

	// Create accepted contacts
	contact := &models.Contact{
		AccountId:        account.Id,
		ContactType:      models.ContactTypeLocal,
		ContactAccountId: &targetId,
		DisplayName:      "Friend",
		Status:           models.ContactStatusAccepted,
	}
	testDB.GormDB.Create(contact)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/contacts", nil) //nolint:noctx
	c.Set("account", account)

	callHandler(GetContacts, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Len(t, resp, 1)
	assert.Equal(t, "Friend", resp[0]["display_name"])
	assert.Equal(t, "local", resp[0]["type"])
}

func TestGetContacts_Empty(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "owner")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/contacts", nil) //nolint:noctx
	c.Set("account", account)

	callHandler(GetContacts, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Empty(t, resp)
}

// ========== RemoveContact Tests ==========

func TestRemoveContact_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "owner")
	targetId := uint64(999)

	contact := &models.Contact{
		AccountId:        account.Id,
		ContactType:      models.ContactTypeLocal,
		ContactAccountId: &targetId,
		Status:           models.ContactStatusAccepted,
	}
	testDB.GormDB.Create(contact)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodDelete, "/api/v1/contacts/"+strconv.FormatUint(contact.Id, 10), map[string]string{
		"id": strconv.FormatUint(contact.Id, 10),
	})

	callHandler(RemoveContact, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp["message"], "removed")
}

func TestRemoveContact_InvalidID(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodDelete, "/api/v1/contacts/abc", map[string]string{
		"id": "abc", //nolint:goconst
	})

	callHandler(RemoveContact, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRemoveContact_NotFound(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodDelete, "/api/v1/contacts/9999", map[string]string{
		"id": "9999",
	})

	callHandler(RemoveContact, c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ========== GetPendingRequests Tests ==========

func TestGetPendingRequests_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "owner")
	requesterId := uint64(888)

	request := &models.ContactRequest{
		FromAccountId: &requesterId,
		ToAccountId:   &account.Id,
		Status:        models.ContactStatusPending,
	}
	testDB.GormDB.Create(request)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/contacts/requests", nil) //nolint:noctx
	c.Set("account", account)

	callHandler(GetPendingRequests, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Len(t, resp, 1)
}

func TestGetPendingRequests_Empty(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "owner")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/contacts/requests", nil) //nolint:noctx
	c.Set("account", account)

	callHandler(GetPendingRequests, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Empty(t, resp)
}

// ========== AcceptRequest Tests ==========

func TestAcceptRequest_InvalidID(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/contacts/requests/abc/accept", map[string]string{
		"id": "abc",
	})

	callHandler(AcceptRequest, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAcceptRequest_MatrixNotEnabled(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/contacts/requests/1/accept", map[string]string{
		"id": "1",
	})

	callHandler(AcceptRequest, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp["error"], "not enabled")
}

// ========== RejectRequest Tests ==========

func TestRejectRequest_InvalidID(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/contacts/requests/abc/reject", map[string]string{
		"id": "abc",
	})

	callHandler(RejectRequest, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRejectRequest_MatrixNotEnabled(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/contacts/requests/1/reject", map[string]string{
		"id": "1",
	})

	callHandler(RejectRequest, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp["error"], "not enabled")
}

// ========== BlockContact Tests ==========

func TestBlockContact_Success(t *testing.T) {
	setupTestDB(t)
	account := createTestAccount(t, "owner")
	targetId := uint64(999)

	contact := &models.Contact{
		AccountId:        account.Id,
		ContactType:      models.ContactTypeLocal,
		ContactAccountId: &targetId,
		Status:           models.ContactStatusAccepted,
	}
	testDB.GormDB.Create(contact)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/contacts/"+strconv.FormatUint(contact.Id, 10)+"/block", map[string]string{
		"id": strconv.FormatUint(contact.Id, 10),
	})

	callHandler(BlockContact, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp["message"], "blocked")

	// Verify the contact is now blocked
	var updated models.Contact

	testDB.GormDB.First(&updated, contact.Id)
	assert.Equal(t, models.ContactStatusBlocked, updated.Status)
}

func TestBlockContact_InvalidID(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/contacts/abc/block", map[string]string{
		"id": "abc",
	})

	callHandler(BlockContact, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBlockContact_NotFound(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/api/v1/contacts/9999/block", map[string]string{
		"id": "9999",
	})

	callHandler(BlockContact, c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ========== LookupMatrixUser Tests ==========

func TestLookupMatrixUser_InvalidAccountID(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodGet, "/api/v1/contacts/lookup/abc", map[string]string{
		"account_id": "abc", //nolint:goconst
	})

	callHandler(LookupMatrixUser, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLookupMatrixUser_MatrixNotEnabled(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodGet, "/api/v1/contacts/lookup/1", map[string]string{
		"account_id": "1",
	})

	callHandler(LookupMatrixUser, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp["error"], "not enabled")
}

// ========== SearchLocalUser Tests ==========

func TestSearchLocalUser_NoMatrixUserID(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/contacts/search", nil) //nolint:noctx
	c.Set("account", &models.Account{Id: 1})

	callHandler(SearchLocalUser, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp["error"], "matrix_user_id")
}

func TestSearchLocalUser_MatrixNotEnabled(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/contacts/search?matrix_user_id=@test:matrix.org", nil) //nolint:noctx
	c.Set("account", &models.Account{Id: 1})

	callHandler(SearchLocalUser, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]any

	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp["error"], "not enabled")
}
