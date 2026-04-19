package conversation

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
)

// callHandler recovers from panic("abort") used by controllers.AbortIf/AbortIfError
func callHandler(fn func(*gin.Context), c *gin.Context) {
	defer func() {
		if r := recover(); r == "abort" {
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
		&models.Status{},
		&models.StatusContent{},
		&models.Conversation{},
		&models.AccountConversation{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}
}

// createTestAccount creates a test account in the DB
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
	if err := testDB.GormDB.Create(account).Error; err != nil {
		t.Fatalf("Failed to create account: %v", err)
	}
	return account
}

// createTestConversation creates a conversation with the given accounts
func createTestConversation(t *testing.T, accountIds []uint64, statusIds []uint64) *models.Conversation {
	t.Helper()
	now := time.Now()
	conversation := &models.Conversation{
		AccountsIds: accountIds,
		StatusesIds: statusIds,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	if err := testDB.GormDB.Create(conversation).Error; err != nil {
		t.Fatalf("Failed to create conversation: %v", err)
	}
	return conversation
}

// newContextWithAccount creates a gin context with account set
func newContextWithAccount(t *testing.T, w *httptest.ResponseRecorder, method, path string) (*gin.Context, *models.Account) {
	t.Helper()
	account := createTestAccount(t, "testuser")
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, nil)
	c.Set("account", account)
	return c, account
}

// newContextWithAccountAndParams creates a gin context with account and path params
func newContextWithAccountAndParams(t *testing.T, w *httptest.ResponseRecorder, method, path string, params map[string]string) (*gin.Context, *models.Account) {
	t.Helper()
	account := createTestAccount(t, "testuser")
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, nil)
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
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodGet, "/conversations")
	otherAccount := createTestAccount(t, "otheruser")

	// Create a conversation between the two accounts
	conv := createTestConversation(t, []uint64{account.Id, otherAccount.Id}, nil)

	// Verify the AccountConversation was created by AfterSave hook
	var ac models.AccountConversation
	err := testDB.GormDB.Where("account_id = ? AND conversation_id = ?", account.Id, conv.Id).First(&ac).Error
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/conversations", nil)
	c.Set("account", account)

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestList_Empty(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodGet, "/conversations")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/conversations", nil)
	c.Set("account", account)

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Len(t, resp, 0)
}

func TestList_WithLimit(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodGet, "/conversations")
	otherAccount := createTestAccount(t, "otheruser")

	createTestConversation(t, []uint64{account.Id, otherAccount.Id}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/conversations?limit=1", nil)
	c.Set("account", account)

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestList_WithSinceId(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodGet, "/conversations")
	otherAccount := createTestAccount(t, "otheruser")

	conv := createTestConversation(t, []uint64{account.Id, otherAccount.Id}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/conversations?since_id="+strconv.FormatUint(conv.Id, 10), nil)
	c.Set("account", account)

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestList_InvalidMinId(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodGet, "/conversations")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/conversations?min_id=abc", nil)
	c.Set("account", account)

	callHandler(List, c)

	// Error is set via c.Error(), not JSON response, so no abort
	// The handler just calls c.Error and returns, leaving status 200 but with errors
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, len(c.Errors) > 0)
}

func TestList_InvalidMaxId(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodGet, "/conversations")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/conversations?max_id=abc", nil)
	c.Set("account", account)

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, len(c.Errors) > 0)
}

// ========== MarkRead Tests ==========

func TestMarkRead_Success(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/conversations/1/read")
	otherAccount := createTestAccount(t, "otheruser")

	// Create a conversation with a status
	statusId := uint64(42)
	conv := createTestConversation(t, []uint64{account.Id, otherAccount.Id}, []uint64{statusId})

	// Verify the AccountConversation was created by AfterSave hook
	var ac models.AccountConversation
	err := testDB.GormDB.Where("account_id = ? AND conversation_id = ?", account.Id, conv.Id).First(&ac).Error
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/conversations/"+strconv.FormatUint(conv.Id, 10)+"/read", map[string]string{
		"id": strconv.FormatUint(conv.Id, 10),
	})

	callHandler(MarkRead, c)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify last_status_id was updated
	var updated models.AccountConversation
	testDB.GormDB.First(&updated, ac.Id)
	assert.Equal(t, statusId, updated.LastStatusId)
}

func TestMarkRead_EmptyID(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/conversations//read")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/conversations//read", nil)
	c.Set("account", account)

	callHandler(MarkRead, c)

	assert.True(t, len(c.Errors) > 0)
}

func TestMarkRead_InvalidID(t *testing.T) {
	setupTestDB(t)
	_, _ = newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/conversations/abc/read")

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/conversations/abc/read", map[string]string{
		"id": "abc",
	})

	callHandler(MarkRead, c)

	assert.True(t, len(c.Errors) > 0)
}

func TestMarkRead_NotFound(t *testing.T) {
	setupTestDB(t)
	_, _ = newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/conversations/99999/read")

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/conversations/99999/read", map[string]string{
		"id": "99999",
	})

	callHandler(MarkRead, c)

	assert.True(t, len(c.Errors) > 0)
}

// ========== Remove Tests ==========

func TestRemove_Success(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodDelete, "/conversations/1")
	otherAccount := createTestAccount(t, "otheruser")

	conv := createTestConversation(t, []uint64{account.Id, otherAccount.Id}, nil)

	// Verify the AccountConversation was created
	var ac models.AccountConversation
	err := testDB.GormDB.Where("account_id = ? AND conversation_id = ?", account.Id, conv.Id).First(&ac).Error
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodDelete, "/conversations/"+strconv.FormatUint(conv.Id, 10), map[string]string{
		"id": strconv.FormatUint(conv.Id, 10),
	})

	callHandler(Remove, c)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify the AccountConversation was deleted
	var deleted models.AccountConversation
	err = testDB.GormDB.Where("account_id = ? AND conversation_id = ?", account.Id, conv.Id).First(&deleted).Error
	assert.Error(t, err) // not found
}

func TestRemove_OnlyAccountLeft(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodDelete, "/conversations/1")

	// Create a conversation with only this account
	conv := createTestConversation(t, []uint64{account.Id}, nil)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodDelete, "/conversations/"+strconv.FormatUint(conv.Id, 10), map[string]string{
		"id": strconv.FormatUint(conv.Id, 10),
	})

	callHandler(Remove, c)

	assert.Equal(t, http.StatusOK, w.Code)

	// The conversation should be deleted because AccountsIds is now empty (BeforeSave hook)
	var deleted models.Conversation
	err := testDB.GormDB.First(&deleted, conv.Id).Error
	assert.Error(t, err) // not found - conversation was auto-deleted
}

func TestRemove_EmptyID(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodDelete, "/conversations//remove")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/conversations//remove", nil)
	c.Set("account", account)

	callHandler(Remove, c)

	assert.True(t, len(c.Errors) > 0)
}

func TestRemove_InvalidID(t *testing.T) {
	setupTestDB(t)
	_, _ = newContextWithAccount(t, httptest.NewRecorder(), http.MethodDelete, "/conversations/abc/remove")

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodDelete, "/conversations/abc/remove", map[string]string{
		"id": "abc",
	})

	callHandler(Remove, c)

	assert.True(t, len(c.Errors) > 0)
}

func TestRemove_NotFound(t *testing.T) {
	setupTestDB(t)
	_, _ = newContextWithAccount(t, httptest.NewRecorder(), http.MethodDelete, "/conversations/99999/remove")

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodDelete, "/conversations/99999/remove", map[string]string{
		"id": "99999",
	})

	callHandler(Remove, c)

	assert.True(t, len(c.Errors) > 0)
}
