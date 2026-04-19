package list

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
	"gorm.io/gorm"
)

// callHandler recovers from panic("abort") used by gin.Abort()
func callHandler(fn func(*gin.Context), c *gin.Context) {
	defer func() {
		if r := recover(); r == "abort" {
			// expected - handler used c.Abort()
			_ = r
		}
	}()
	fn(c)
}

var (
	tdb    *testutil.TestDB
	testDB *gorm.DB
)

func TestMain(m *testing.M) {
	database.SetTestMode()
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func setupTestDB(t *testing.T) {
	t.Helper()
	var err error
	tdb, err = testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	testDB = tdb.GormDB
	database.SetTestDB(testDB)

	// AutoMigrate models without tinyint/uint8 type issues
	err = testDB.AutoMigrate(
		&models.User{},
		&models.Account{},
		&models.AccountExtra{},
	)
	require.NoError(t, err)

	// Create lists table manually with INTEGER type for replies_policy
	// (AutoMigrate creates uint8 columns as BOOLEAN in sqlexec)
	err = tdb.CreateTable(`CREATE TABLE IF NOT EXISTS lists (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title VARCHAR(255) NOT NULL DEFAULT '',
		account_id INTEGER NOT NULL DEFAULT 0,
		replies_policy INTEGER NOT NULL DEFAULT 0,
		last_viewed_at DATETIME,
		visibility INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	)`)
	require.NoError(t, err)

	err = tdb.CreateTable(`CREATE TABLE IF NOT EXISTS list_accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		list_id INTEGER NOT NULL,
		account_id INTEGER NOT NULL,
		created_at DATETIME
	)`)
	require.NoError(t, err)

	err = tdb.CreateTable(`CREATE TABLE IF NOT EXISTS list_follow_accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		list_id INTEGER NOT NULL,
		account_id INTEGER NOT NULL,
		created_at DATETIME
	)`)
	require.NoError(t, err)
}

func createTestUser(t *testing.T) *models.User {
	t.Helper()
	now := time.Now()
	user := &models.User{
		UserName:       "testuser_" + testutil.RandomString(8),
		EmailHash:      "testhash_" + testutil.RandomString(8),
		Status:         models.UserStatusNormal,
		IsMailVerified: true,
		CreatedAt:      &now,
		UpdatedAt:      &now,
	}
	err := testDB.Create(user).Error
	require.NoError(t, err)
	return user
}

func createTestAccount(t *testing.T, userID uint64) *models.Account {
	t.Helper()
	now := time.Now()
	account := &models.Account{
		UserId:      userID,
		Username:    "acct_" + testutil.RandomString(8),
		DisplayName: "Test Acct",
		Acct:        "testacct",
		Locked:      false,
		Bot:         false,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	err := testDB.Create(account).Error
	require.NoError(t, err)
	return account
}

func createTestList(t *testing.T, accountID uint64, visibility ...int) *models.List {
	t.Helper()
	list := &models.List{
		Title:         "Test List",
		AccountId:     accountID,
		RepliesPolicy: models.ListRepliesPolicyList,
		Visibility:    models.ListVisibilityPublic,
	}
	if len(visibility) > 0 {
		list.Visibility = visibility[0]
	}
	err := testDB.Create(list).Error
	require.NoError(t, err)
	return list
}

func newContext(t *testing.T, method, path string, body []byte, account *models.Account) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	c.Request = req
	if account != nil {
		c.Set("account", account)
		c.Set("accountId", account.Id)
	}
	return c, w
}

func setParams(c *gin.Context, params gin.Params) {
	c.Params = params
}

// ===================== GetListByParam Tests =====================

func TestGetListByParam(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	list := createTestList(t, account.Id)

	t.Run("valid list id", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/lists/"+strconv.FormatUint(list.Id, 10), nil)
		c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}}

		result, abort := GetListByParam(c)
		assert.False(t, abort)
		assert.NotNil(t, result)
		assert.Equal(t, list.Id, result.Id)
	})

	t.Run("invalid list id - zero", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/lists/0", nil)
		c.Params = gin.Params{{Key: "id", Value: "0"}}

		result, abort := GetListByParam(c)
		assert.True(t, abort)
		assert.Nil(t, result)
	})

	t.Run("invalid list id - negative number", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/lists/-1", nil)
		c.Params = gin.Params{{Key: "id", Value: "-1"}}

		result, abort := GetListByParam(c)
		assert.True(t, abort)
		assert.Nil(t, result)
	})

	t.Run("invalid list id - not a number", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/lists/abc", nil)
		c.Params = gin.Params{{Key: "id", Value: "abc"}}

		result, abort := GetListByParam(c)
		assert.True(t, abort)
		assert.Nil(t, result)
	})

	t.Run("invalid list id - empty string", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/lists/", nil)
		c.Params = gin.Params{{Key: "id", Value: ""}}

		result, abort := GetListByParam(c)
		assert.True(t, abort)
		assert.Nil(t, result)
	})

	t.Run("list not found", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/lists/99999", nil)
		c.Params = gin.Params{{Key: "id", Value: "99999"}}

		result, abort := GetListByParam(c)
		assert.True(t, abort)
		assert.Nil(t, result)
	})
}

// ===================== CreateList Tests =====================

func TestCreateList(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)

	t.Run("success - public with list policy", func(t *testing.T) {
		body, _ := json.Marshal(CreateListRequest{
			Title:         "My Public List",
			RepliesPolicy: "list",
			Visibility:    "public",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists", body, account)
		callHandler(CreateList, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify the list was created
		var lists []models.List
		testDB.Where("account_id = ?", account.Id).Find(&lists)
		assert.GreaterOrEqual(t, len(lists), 1)
	})

	t.Run("success - private with followed policy", func(t *testing.T) {
		body, _ := json.Marshal(CreateListRequest{
			Title:         "My Private List",
			RepliesPolicy: "followed",
			Visibility:    "private",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists", body, account)
		callHandler(CreateList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - followers with none policy", func(t *testing.T) {
		body, _ := json.Marshal(CreateListRequest{
			Title:         "My Followers List",
			RepliesPolicy: "none",
			Visibility:    "followers",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists", body, account)
		callHandler(CreateList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - default replies policy", func(t *testing.T) {
		body, _ := json.Marshal(CreateListRequest{
			Title:      "Default Replies List",
			Visibility: "public",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists", body, account)
		callHandler(CreateList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("missing title - empty string", func(t *testing.T) {
		body, _ := json.Marshal(CreateListRequest{
			Title:         "",
			RepliesPolicy: "list",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists", body, account)
		callHandler(CreateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid replies policy", func(t *testing.T) {
		body, _ := json.Marshal(CreateListRequest{
			Title:         "Test List",
			RepliesPolicy: "invalid",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists", body, account)
		callHandler(CreateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid visibility", func(t *testing.T) {
		body, _ := json.Marshal(CreateListRequest{
			Title:         "Test List",
			RepliesPolicy: "list",
			Visibility:    "unknown",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists", body, account)
		callHandler(CreateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("no account in context panics", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.NotNil(t, r)
		}()
		body, _ := json.Marshal(CreateListRequest{
			Title:         "Test List",
			RepliesPolicy: "list",
		})
		c, _ := newContext(t, http.MethodPost, "/api/v1/lists", body, nil)
		callHandler(CreateList, c)
	})

	t.Run("invalid json body", func(t *testing.T) {
		c, w := newContext(t, http.MethodPost, "/api/v1/lists", []byte("not json"), account)
		callHandler(CreateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("title at max length 64", func(t *testing.T) {
		title := make([]byte, 64)
		for i := range title {
			title[i] = 'a'
		}
		body, _ := json.Marshal(CreateListRequest{
			Title:         string(title),
			RepliesPolicy: "list",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists", body, account)
		callHandler(CreateList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("title exceeds max length 65", func(t *testing.T) {
		title := make([]byte, 65)
		for i := range title {
			title[i] = 'a'
		}
		body, _ := json.Marshal(CreateListRequest{
			Title:         string(title),
			RepliesPolicy: "list",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists", body, account)
		callHandler(CreateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// ===================== GetList Tests =====================

func TestGetList(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	list := createTestList(t, account.Id, models.ListVisibilityPublic)

	t.Run("success - public list", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(GetList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - public list without account", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), nil, nil)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(GetList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - owner can see private list", func(t *testing.T) {
		privateList := createTestList(t, account.Id, models.ListVisibilityPrivate)
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(privateList.Id, 10), nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(privateList.Id, 10)}})
		callHandler(GetList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - follower can see private list", func(t *testing.T) {
		privateList := createTestList(t, account.Id, models.ListVisibilityPrivate)
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)

		// Add as follower
		lfa := &models.ListFollowAccount{
			ListId:    privateList.Id,
			AccountId: otherAccount.Id,
		}
		testDB.Create(lfa)

		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(privateList.Id, 10), nil, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(privateList.Id, 10)}})
		callHandler(GetList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("forbidden - private list, non-owner non-follower", func(t *testing.T) {
		privateList := createTestList(t, account.Id, models.ListVisibilityPrivate)
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)

		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(privateList.Id, 10), nil, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(privateList.Id, 10)}})
		callHandler(GetList, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("invalid list id - non numeric", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/abc", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "abc"}})
		callHandler(GetList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid list id - zero", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/0", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "0"}})
		callHandler(GetList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("list not found", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/99999", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "99999"}})
		callHandler(GetList, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// ===================== GetMyLists Tests =====================

func TestGetMyLists(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)

	// Create some lists
	createTestList(t, account.Id)
	createTestList(t, account.Id)

	t.Run("success - returns lists", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists", nil, account)
		callHandler(GetMyLists, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var lists []interface{}
		err := json.Unmarshal(w.Body.Bytes(), &lists)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(lists), 2)
	})

	t.Run("no lists for new account", func(t *testing.T) {
		newUser := createTestUser(t)
		newAccount := createTestAccount(t, newUser.Id)
		c, w := newContext(t, http.MethodGet, "/api/v1/lists", nil, newAccount)
		callHandler(GetMyLists, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("no account in context panics", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.NotNil(t, r)
		}()
		c, _ := newContext(t, http.MethodGet, "/api/v1/lists", nil, nil)
		callHandler(GetMyLists, c)
	})
}

// ===================== UpdateList Tests =====================

func TestUpdateList(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	list := createTestList(t, account.Id)

	t.Run("success - update title", func(t *testing.T) {
		title := "Updated Title"
		body, _ := json.Marshal(UpdateListRequest{
			Title: &title,
		})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify title was updated
		var updated models.List
		testDB.First(&updated, list.Id)
		assert.Equal(t, "Updated Title", updated.Title)
	})

	t.Run("success - update replies policy", func(t *testing.T) {
		policy := "followed"
		body, _ := json.Marshal(UpdateListRequest{
			RepliesPolicy: &policy,
		})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - update visibility to private", func(t *testing.T) {
		visibility := "private"
		body, _ := json.Marshal(UpdateListRequest{
			Visibility: &visibility,
		})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var updated models.List
		testDB.First(&updated, list.Id)
		assert.Equal(t, models.ListVisibilityPrivate, updated.Visibility)
	})

	t.Run("success - update visibility to followers", func(t *testing.T) {
		visibility := "followers"
		body, _ := json.Marshal(UpdateListRequest{
			Visibility: &visibility,
		})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - update multiple fields", func(t *testing.T) {
		title := "Multi Update"
		policy := "none"
		visibility := "public"
		body, _ := json.Marshal(UpdateListRequest{
			Title:         &title,
			RepliesPolicy: &policy,
			Visibility:    &visibility,
		})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("invalid list id - non numeric", func(t *testing.T) {
		title := "Updated"
		body, _ := json.Marshal(UpdateListRequest{Title: &title})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/abc", body, account)
		setParams(c, gin.Params{{Key: "id", Value: "abc"}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid list id - zero", func(t *testing.T) {
		title := "Updated"
		body, _ := json.Marshal(UpdateListRequest{Title: &title})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/0", body, account)
		setParams(c, gin.Params{{Key: "id", Value: "0"}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid body - not json", func(t *testing.T) {
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), []byte("not json"), account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid replies policy", func(t *testing.T) {
		policy := "invalid"
		body, _ := json.Marshal(UpdateListRequest{RepliesPolicy: &policy})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid visibility", func(t *testing.T) {
		visibility := "invalid"
		body, _ := json.Marshal(UpdateListRequest{Visibility: &visibility})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("list not found", func(t *testing.T) {
		title := "Updated"
		body, _ := json.Marshal(UpdateListRequest{Title: &title})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/99999", body, account)
		setParams(c, gin.Params{{Key: "id", Value: "99999"}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("permission denied - wrong owner", func(t *testing.T) {
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)

		title := "Hacked"
		body, _ := json.Marshal(UpdateListRequest{Title: &title})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("title too short (empty)", func(t *testing.T) {
		title := ""
		body, _ := json.Marshal(UpdateListRequest{Title: &title})
		c, w := newContext(t, http.MethodPut, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UpdateList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// ===================== DeleteList Tests =====================

func TestDeleteList(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)

	t.Run("success", func(t *testing.T) {
		list := createTestList(t, account.Id)
		body, _ := json.Marshal(UpdateListRequest{})

		c, w := newContext(t, http.MethodDelete, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(DeleteList, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify list is soft-deleted
		var found models.List
		err := testDB.Unscoped().First(&found, list.Id).Error
		assert.NoError(t, err)
		assert.NotNil(t, found.DeletedAt)
	})

	t.Run("success - list no longer queryable", func(t *testing.T) {
		list := createTestList(t, account.Id)
		body, _ := json.Marshal(UpdateListRequest{})

		c, _ := newContext(t, http.MethodDelete, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(DeleteList, c)

		// Try to find it with normal query
		var found models.List
		err := testDB.First(&found, list.Id).Error
		assert.Error(t, err)
	})

	t.Run("invalid list id - non numeric", func(t *testing.T) {
		body, _ := json.Marshal(UpdateListRequest{})
		c, w := newContext(t, http.MethodDelete, "/api/v1/lists/abc", body, account)
		setParams(c, gin.Params{{Key: "id", Value: "abc"}})
		callHandler(DeleteList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid list id - zero", func(t *testing.T) {
		body, _ := json.Marshal(UpdateListRequest{})
		c, w := newContext(t, http.MethodDelete, "/api/v1/lists/0", body, account)
		setParams(c, gin.Params{{Key: "id", Value: "0"}})
		callHandler(DeleteList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid body - not json", func(t *testing.T) {
		c, w := newContext(t, http.MethodDelete, "/api/v1/lists/1", []byte("bad"), account)
		setParams(c, gin.Params{{Key: "id", Value: "1"}})
		callHandler(DeleteList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("list not found", func(t *testing.T) {
		body, _ := json.Marshal(UpdateListRequest{})
		c, w := newContext(t, http.MethodDelete, "/api/v1/lists/99999", body, account)
		setParams(c, gin.Params{{Key: "id", Value: "99999"}})
		callHandler(DeleteList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("permission denied - wrong owner", func(t *testing.T) {
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)
		list := createTestList(t, account.Id)

		body, _ := json.Marshal(UpdateListRequest{})
		c, w := newContext(t, http.MethodDelete, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10), body, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(DeleteList, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

// ===================== GetListAccounts Tests =====================

func TestGetListAccounts(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	list := createTestList(t, account.Id, models.ListVisibilityPublic)

	otherUser := createTestUser(t)
	otherAccount := createTestAccount(t, otherUser.Id)

	// Add other account to the list
	la := &models.ListAccount{
		ListId:    list.Id,
		AccountId: otherAccount.Id,
	}
	err := testDB.Create(la).Error
	require.NoError(t, err)

	t.Run("success - returns accounts in list", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(GetListAccounts, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - with limit", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts?limit=1", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(GetListAccounts, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - with max_id", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts?max_id=999", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(GetListAccounts, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - with since_id", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts?since_id=1", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(GetListAccounts, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("list not found", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/99999/accounts", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "99999"}})
		callHandler(GetListAccounts, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("forbidden - private list non-owner non-follower", func(t *testing.T) {
		privateList := createTestList(t, account.Id, models.ListVisibilityPrivate)
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(privateList.Id, 10)+"/accounts", nil, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(privateList.Id, 10)}})
		callHandler(GetListAccounts, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("success - empty list accounts", func(t *testing.T) {
		emptyList := createTestList(t, account.Id, models.ListVisibilityPublic)
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(emptyList.Id, 10)+"/accounts", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(emptyList.Id, 10)}})
		callHandler(GetListAccounts, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// ===================== AddAccountToList Tests =====================

func TestAddAccountToList(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	list := createTestList(t, account.Id)

	otherUser := createTestUser(t)
	otherAccount := createTestAccount(t, otherUser.Id)

	t.Run("success - add single account", func(t *testing.T) {
		body, _ := json.Marshal(AddAccountsToListRequest{
			AccountIds: []string{strconv.FormatUint(otherAccount.Id, 10)},
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(AddAccountToList, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify account was added
		var count int64
		testDB.Model(&models.ListAccount{}).Where("list_id = ? AND account_id = ?", list.Id, otherAccount.Id).Count(&count)
		assert.Equal(t, int64(1), count)
	})

	t.Run("success - add multiple accounts", func(t *testing.T) {
		thirdUser := createTestUser(t)
		thirdAccount := createTestAccount(t, thirdUser.Id)

		body, _ := json.Marshal(AddAccountsToListRequest{
			AccountIds: []string{
				strconv.FormatUint(otherAccount.Id, 10),
				strconv.FormatUint(thirdAccount.Id, 10),
			},
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(AddAccountToList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - skips invalid account ids", func(t *testing.T) {
		body, _ := json.Marshal(AddAccountsToListRequest{
			AccountIds: []string{
				"not-a-number",
				strconv.FormatUint(otherAccount.Id, 10),
			},
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(AddAccountToList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("invalid body - not json", func(t *testing.T) {
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts", []byte("bad"), account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(AddAccountToList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not owner - forbidden", func(t *testing.T) {
		body, _ := json.Marshal(AddAccountsToListRequest{
			AccountIds: []string{strconv.FormatUint(otherAccount.Id, 10)},
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts", body, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(AddAccountToList, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("empty account ids", func(t *testing.T) {
		body, _ := json.Marshal(AddAccountsToListRequest{
			AccountIds: []string{},
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(AddAccountToList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("all accounts already exist", func(t *testing.T) {
		// Add account first
		la := &models.ListAccount{ListId: list.Id, AccountId: otherAccount.Id}
		testDB.Create(la)

		body, _ := json.Marshal(AddAccountsToListRequest{
			AccountIds: []string{strconv.FormatUint(otherAccount.Id, 10)},
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(AddAccountToList, c)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "already exist")
	})

	t.Run("invalid account ids - all non-numeric", func(t *testing.T) {
		body, _ := json.Marshal(AddAccountsToListRequest{
			AccountIds: []string{"not-a-number", "also-invalid"},
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(AddAccountToList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("success - adds only non-existing accounts from mixed list", func(t *testing.T) {
		thirdUser := createTestUser(t)
		thirdAccount := createTestAccount(t, thirdUser.Id)

		// Add one account first
		la := &models.ListAccount{ListId: list.Id, AccountId: otherAccount.Id}
		testDB.Create(la)

		body, _ := json.Marshal(AddAccountsToListRequest{
			AccountIds: []string{
				strconv.FormatUint(otherAccount.Id, 10),
				strconv.FormatUint(thirdAccount.Id, 10),
			},
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(AddAccountToList, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify third account was added
		var count int64
		testDB.Model(&models.ListAccount{}).Where("list_id = ? AND account_id = ?", list.Id, thirdAccount.Id).Count(&count)
		assert.Equal(t, int64(1), count)
	})
}

// ===================== RemoveAccountFromList Tests =====================

func TestRemoveAccountFromList(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	list := createTestList(t, account.Id)

	otherUser := createTestUser(t)
	otherAccount := createTestAccount(t, otherUser.Id)

	t.Run("success", func(t *testing.T) {
		// Add account first
		la := &models.ListAccount{ListId: list.Id, AccountId: otherAccount.Id}
		testDB.Create(la)

		c, w := newContext(t, http.MethodDelete, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts/"+strconv.FormatUint(otherAccount.Id, 10), nil, account)
		setParams(c, gin.Params{
			{Key: "id", Value: strconv.FormatUint(list.Id, 10)},
			{Key: "account_id", Value: strconv.FormatUint(otherAccount.Id, 10)},
		})
		callHandler(RemoveAccountFromList, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify account was removed
		var count int64
		testDB.Model(&models.ListAccount{}).Where("list_id = ? AND account_id = ?", list.Id, otherAccount.Id).Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("list not found", func(t *testing.T) {
		c, w := newContext(t, http.MethodDelete, "/api/v1/lists/99999/accounts/1", nil, account)
		setParams(c, gin.Params{
			{Key: "id", Value: "99999"},
			{Key: "account_id", Value: "1"},
		})
		callHandler(RemoveAccountFromList, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("not owner - forbidden", func(t *testing.T) {
		la := &models.ListAccount{ListId: list.Id, AccountId: otherAccount.Id}
		testDB.Create(la)

		c, w := newContext(t, http.MethodDelete, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts/"+strconv.FormatUint(otherAccount.Id, 10), nil, otherAccount)
		setParams(c, gin.Params{
			{Key: "id", Value: strconv.FormatUint(list.Id, 10)},
			{Key: "account_id", Value: strconv.FormatUint(otherAccount.Id, 10)},
		})
		callHandler(RemoveAccountFromList, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("invalid account id - non numeric", func(t *testing.T) {
		c, w := newContext(t, http.MethodDelete, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts/abc", nil, account)
		setParams(c, gin.Params{
			{Key: "id", Value: strconv.FormatUint(list.Id, 10)},
			{Key: "account_id", Value: "abc"},
		})
		callHandler(RemoveAccountFromList, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("account not in list - not found", func(t *testing.T) {
		c, w := newContext(t, http.MethodDelete, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/accounts/99999", nil, account)
		setParams(c, gin.Params{
			{Key: "id", Value: strconv.FormatUint(list.Id, 10)},
			{Key: "account_id", Value: "99999"},
		})
		callHandler(RemoveAccountFromList, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// ===================== GetListFollowers Tests =====================

func TestGetListFollowers(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	list := createTestList(t, account.Id, models.ListVisibilityPublic)

	otherUser := createTestUser(t)
	otherAccount := createTestAccount(t, otherUser.Id)

	t.Run("success - owner is auto-followed", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/followers", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(GetListFollowers, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - with additional followers", func(t *testing.T) {
		lfa := &models.ListFollowAccount{
			ListId:    list.Id,
			AccountId: otherAccount.Id,
		}
		err := testDB.Create(lfa).Error
		require.NoError(t, err)

		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/followers", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(GetListFollowers, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - with limit", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/followers?limit=1", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(GetListFollowers, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("list not found", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/lists/99999/followers", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "99999"}})
		callHandler(GetListFollowers, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("forbidden - private list non-owner non-follower", func(t *testing.T) {
		privateList := createTestList(t, account.Id, models.ListVisibilityPrivate)

		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(privateList.Id, 10)+"/followers", nil, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(privateList.Id, 10)}})
		callHandler(GetListFollowers, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("success - follower can see followers", func(t *testing.T) {
		privateList := createTestList(t, account.Id, models.ListVisibilityPrivate)

		// Add other as follower
		lfa := &models.ListFollowAccount{
			ListId:    privateList.Id,
			AccountId: otherAccount.Id,
		}
		testDB.Create(lfa)

		c, w := newContext(t, http.MethodGet, "/api/v1/lists/"+strconv.FormatUint(privateList.Id, 10)+"/followers", nil, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(privateList.Id, 10)}})
		callHandler(GetListFollowers, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// ===================== FollowList Tests =====================

func TestFollowList(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	list := createTestList(t, account.Id, models.ListVisibilityPublic)

	t.Run("success - non-owner follows public list", func(t *testing.T) {
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)

		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/follow", nil, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(FollowList, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify follow record was created
		var count int64
		testDB.Model(&models.ListFollowAccount{}).Where("list_id = ? AND account_id = ?", list.Id, otherAccount.Id).Count(&count)
		assert.Equal(t, int64(1), count)
	})

	t.Run("already followed", func(t *testing.T) {
		// Owner is auto-followed via AfterCreate hook
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/follow", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(FollowList, c)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "already followed")
	})

	t.Run("private list cannot follow", func(t *testing.T) {
		privateList := createTestList(t, account.Id, models.ListVisibilityPrivate)

		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(privateList.Id, 10)+"/follow", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(privateList.Id, 10)}})
		callHandler(FollowList, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("followers list can be followed", func(t *testing.T) {
		followersList := createTestList(t, account.Id, models.ListVisibilityFollowers)
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)

		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(followersList.Id, 10)+"/follow", nil, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(followersList.Id, 10)}})
		callHandler(FollowList, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("list not found", func(t *testing.T) {
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/99999/follow", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "99999"}})
		callHandler(FollowList, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("invalid list id", func(t *testing.T) {
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/abc/follow", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "abc"}})
		callHandler(FollowList, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// ===================== UnfollowList Tests =====================

func TestUnfollowList(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	list := createTestList(t, account.Id, models.ListVisibilityPublic)

	t.Run("success - unfollow existing follow", func(t *testing.T) {
		// Owner is auto-followed via AfterCreate, so unfollow should work
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/unfollow", nil, account)
		c.Set("accountId", account.Id)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UnfollowList, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify follow record was removed
		var count int64
		testDB.Model(&models.ListFollowAccount{}).Where("list_id = ? AND account_id = ?", list.Id, account.Id).Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("not followed - returns ok", func(t *testing.T) {
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)

		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(list.Id, 10)+"/unfollow", nil, otherAccount)
		c.Set("accountId", otherAccount.Id)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(list.Id, 10)}})
		callHandler(UnfollowList, c)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "not followed")
	})

	t.Run("list not found", func(t *testing.T) {
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/99999/unfollow", nil, account)
		c.Set("accountId", account.Id)
		setParams(c, gin.Params{{Key: "id", Value: "99999"}})
		callHandler(UnfollowList, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("invalid list id", func(t *testing.T) {
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/abc/unfollow", nil, account)
		c.Set("accountId", account.Id)
		setParams(c, gin.Params{{Key: "id", Value: "abc"}})
		callHandler(UnfollowList, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("success - follow then unfollow", func(t *testing.T) {
		newList := createTestList(t, account.Id, models.ListVisibilityPublic)
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)

		// Follow first
		lfa := &models.ListFollowAccount{ListId: newList.Id, AccountId: otherAccount.Id}
		testDB.Create(lfa)

		// Then unfollow
		c, w := newContext(t, http.MethodPost, "/api/v1/lists/"+strconv.FormatUint(newList.Id, 10)+"/unfollow", nil, otherAccount)
		c.Set("accountId", otherAccount.Id)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(newList.Id, 10)}})
		callHandler(UnfollowList, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var count int64
		testDB.Model(&models.ListFollowAccount{}).Where("list_id = ? AND account_id = ?", newList.Id, otherAccount.Id).Count(&count)
		assert.Equal(t, int64(0), count)
	})
}
