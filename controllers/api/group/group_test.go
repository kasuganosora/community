package group

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
			// expected
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

	// AutoMigrate models without tinyint type issues
	err = testDB.AutoMigrate(
		&models.User{},
		&models.Account{},
		&models.AccountExtra{},
	)
	require.NoError(t, err)

	// Create groups-related tables manually with INTEGER types for tinyint columns
	// (AutoMigrate creates type:tinyint columns as BOOLEAN in sqlexec)
	err = tdb.CreateTable(`CREATE TABLE IF NOT EXISTS groups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		owner_id INTEGER,
		name VARCHAR(255),
		description TEXT,
		avatar VARCHAR(512),
		type INTEGER DEFAULT 0,
		visibility VARCHAR(20) DEFAULT 'private',
		matrix_room_id VARCHAR(255),
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	)`)
	require.NoError(t, err)

	err = tdb.CreateTable(`CREATE TABLE IF NOT EXISTS group_members (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		group_id INTEGER,
		account_id INTEGER,
		matrix_user_id VARCHAR(255),
		display_name VARCHAR(255),
		role INTEGER DEFAULT 0,
		invited_by INTEGER,
		matrix_invited_by VARCHAR(255),
		joined_at DATETIME,
		created_at DATETIME,
		updated_at DATETIME,
		deleted_at DATETIME
	)`)
	require.NoError(t, err)

	err = tdb.CreateTable(`CREATE TABLE IF NOT EXISTS group_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		group_id INTEGER,
		sender_id INTEGER,
		matrix_sender_id VARCHAR(255),
		content TEXT,
		matrix_event_id VARCHAR(255),
		created_at DATETIME,
		deleted_at DATETIME
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

func createTestGroup(t *testing.T, ownerID uint64) *models.Group {
	t.Helper()
	group := &models.Group{
		OwnerId:     ownerID,
		Name:        "Test Group " + testutil.RandomString(4),
		Description: "A test group",
		Type:        models.GroupTypeLocal,
		Visibility:  "private",
	}
	err := testDB.Create(group).Error
	require.NoError(t, err)

	// Add owner as member
	member := &models.GroupMember{
		GroupId:   group.Id,
		AccountId: &ownerID,
		Role:      models.GroupMemberRoleOwner,
	}
	testDB.Create(member)

	return group
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
	}
	return c, w
}

func setParams(c *gin.Context, params gin.Params) {
	c.Params = params
}

// ===================== CreateGroup Tests =====================

func TestCreateGroup(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)

	t.Run("success - private group", func(t *testing.T) {
		body, _ := json.Marshal(CreateGroupRequest{
			Name:        "Private Group",
			Description: "A private group",
			Visibility:  "private",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", body, account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusCreated, w.Code)

		var resp map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "Private Group", resp["name"])
		assert.Equal(t, "private", resp["visibility"])
	})

	t.Run("success - public group", func(t *testing.T) {
		body, _ := json.Marshal(CreateGroupRequest{
			Name:        "Public Group",
			Description: "A public group",
			Visibility:  "public",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", body, account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusCreated, w.Code)

		var resp map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "public", resp["visibility"])
	})

	t.Run("success - default visibility is private", func(t *testing.T) {
		body, _ := json.Marshal(CreateGroupRequest{
			Name:        "Default Group",
			Description: "Default visibility",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", body, account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusCreated, w.Code)

		var resp map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "private", resp["visibility"])
	})

	t.Run("success - with bridge_to_matrix flag (matrix not enabled)", func(t *testing.T) {
		body, _ := json.Marshal(CreateGroupRequest{
			Name:           "Bridge Group",
			Description:    "Bridged group",
			BridgeToMatrix: true,
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", body, account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusCreated, w.Code)

		// Should fall back to local since matrix is not enabled
		var resp map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(0), resp["type"]) // GroupTypeLocal = 0
	})

	t.Run("verifies group created with owner member", func(t *testing.T) {
		body, _ := json.Marshal(CreateGroupRequest{
			Name:        "Verify Group",
			Description: "With member check",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", body, account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusCreated, w.Code)

		// Check the response has an ID
		var resp map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		groupID, ok := resp["id"].(float64)
		assert.True(t, ok)
		assert.Greater(t, groupID, float64(0))

		// Check owner member was created
		var memberCount int64
		testDB.Model(&models.GroupMember{}).Where("group_id = ? AND account_id = ?", uint64(groupID), account.Id).Count(&memberCount)
		assert.Equal(t, int64(1), memberCount)
	})

	t.Run("missing name - empty string", func(t *testing.T) {
		body, _ := json.Marshal(CreateGroupRequest{
			Name: "",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", body, account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("name too long - 101 chars", func(t *testing.T) {
		longName := make([]byte, 101)
		for i := range longName {
			longName[i] = 'a'
		}
		body, _ := json.Marshal(CreateGroupRequest{
			Name: string(longName),
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", body, account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("name at max length - 100 chars", func(t *testing.T) {
		name := make([]byte, 100)
		for i := range name {
			name[i] = 'a'
		}
		body, _ := json.Marshal(CreateGroupRequest{
			Name: string(name),
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", body, account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusCreated, w.Code)
	})

	t.Run("invalid visibility", func(t *testing.T) {
		body, _ := json.Marshal(CreateGroupRequest{
			Name:       "Bad Visibility",
			Visibility: "invalid",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", body, account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("description too long - 501 chars", func(t *testing.T) {
		longDesc := make([]byte, 501)
		for i := range longDesc {
			longDesc[i] = 'a'
		}
		body, _ := json.Marshal(CreateGroupRequest{
			Name:        "Long Desc Group",
			Description: string(longDesc),
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", body, account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("description at max length - 500 chars", func(t *testing.T) {
		desc := make([]byte, 500)
		for i := range desc {
			desc[i] = 'a'
		}
		body, _ := json.Marshal(CreateGroupRequest{
			Name:        "Max Desc Group",
			Description: string(desc),
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", body, account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusCreated, w.Code)
	})

	t.Run("no account in context panics", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.NotNil(t, r)
		}()
		body, _ := json.Marshal(CreateGroupRequest{
			Name: "No Account Group",
		})
		c, _ := newContext(t, http.MethodPost, "/api/v1/groups", body, nil)
		callHandler(CreateGroup, c)
	})

	t.Run("invalid json body", func(t *testing.T) {
		c, w := newContext(t, http.MethodPost, "/api/v1/groups", []byte("not json"), account)
		callHandler(CreateGroup, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// ===================== InviteMember Tests =====================

func TestInviteMember(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	group := createTestGroup(t, account.Id)

	otherUser := createTestUser(t)
	otherAccount := createTestAccount(t, otherUser.Id)

	t.Run("success - by account_id", func(t *testing.T) {
		accountID := otherAccount.Id
		body, _ := json.Marshal(InviteMemberRequest{
			AccountId: &accountID,
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/invite", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(InviteMember, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify member was added
		var member models.GroupMember
		err := testDB.Where("group_id = ? AND account_id = ?", group.Id, otherAccount.Id).First(&member).Error
		assert.NoError(t, err)
		assert.Equal(t, models.GroupMemberRoleMember, member.Role)
	})

	t.Run("success - by matrix_user_id", func(t *testing.T) {
		matrixID := "@test:matrix.org"
		body, _ := json.Marshal(InviteMemberRequest{
			MatrixUserId: &matrixID,
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/invite", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(InviteMember, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var member models.GroupMember
		err := testDB.Where("group_id = ? AND matrix_user_id = ?", group.Id, matrixID).First(&member).Error
		assert.NoError(t, err)
	})

	t.Run("success - invite as admin role", func(t *testing.T) {
		thirdUser := createTestUser(t)
		thirdAccount := createTestAccount(t, thirdUser.Id)

		accountID := thirdAccount.Id
		body, _ := json.Marshal(InviteMemberRequest{
			AccountId: &accountID,
			Role:      int(models.GroupMemberRoleAdmin),
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/invite", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(InviteMember, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var member models.GroupMember
		err := testDB.Where("group_id = ? AND account_id = ?", group.Id, thirdAccount.Id).First(&member).Error
		assert.NoError(t, err)
		assert.Equal(t, models.GroupMemberRoleAdmin, member.Role)
	})

	t.Run("success - invalid role defaults to member", func(t *testing.T) {
		thirdUser := createTestUser(t)
		thirdAccount := createTestAccount(t, thirdUser.Id)

		accountID := thirdAccount.Id
		body, _ := json.Marshal(InviteMemberRequest{
			AccountId: &accountID,
			Role:      999, // invalid role
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/invite", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(InviteMember, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var member models.GroupMember
		err := testDB.Where("group_id = ? AND account_id = ?", group.Id, thirdAccount.Id).First(&member).Error
		assert.NoError(t, err)
		assert.Equal(t, models.GroupMemberRoleMember, member.Role) // defaults to member
	})

	t.Run("invalid group id - non numeric", func(t *testing.T) {
		accountID := otherAccount.Id
		body, _ := json.Marshal(InviteMemberRequest{
			AccountId: &accountID,
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/abc/invite", body, account)
		setParams(c, gin.Params{{Key: "id", Value: "abc"}})
		callHandler(InviteMember, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("no account_id or matrix_user_id", func(t *testing.T) {
		body, _ := json.Marshal(InviteMemberRequest{})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/invite", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(InviteMember, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("no permission - regular member cannot invite", func(t *testing.T) {
		// Create a new group and add otherAccount as regular member
		newGroup := createTestGroup(t, account.Id)
		memberAccID := otherAccount.Id
		_, _ = json.Marshal(InviteMemberRequest{
			AccountId: &memberAccID,
		})
		// Add otherAccount as regular member
		member := &models.GroupMember{
			GroupId:   newGroup.Id,
			AccountId: &otherAccount.Id,
			Role:      models.GroupMemberRoleMember,
		}
		testDB.Create(member)

		thirdUser := createTestUser(t)
		thirdAccount := createTestAccount(t, thirdUser.Id)
		thirdAccID := thirdAccount.Id
		body2, _ := json.Marshal(InviteMemberRequest{
			AccountId: &thirdAccID,
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(newGroup.Id, 10)+"/invite", body2, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(newGroup.Id, 10)}})
		callHandler(InviteMember, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("admin can invite members", func(t *testing.T) {
		// Make otherAccount an admin
		adminMember := &models.GroupMember{
			GroupId:   group.Id,
			AccountId: &otherAccount.Id,
			Role:      models.GroupMemberRoleAdmin,
		}
		testDB.Create(adminMember)

		thirdUser := createTestUser(t)
		thirdAccount := createTestAccount(t, thirdUser.Id)

		accountID := thirdAccount.Id
		body, _ := json.Marshal(InviteMemberRequest{
			AccountId: &accountID,
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/invite", body, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(InviteMember, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify member was added
		var member models.GroupMember
		err := testDB.Where("group_id = ? AND account_id = ?", group.Id, thirdAccount.Id).First(&member).Error
		assert.NoError(t, err)
	})
}

// ===================== RemoveMember Tests =====================

func TestRemoveMember(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	group := createTestGroup(t, account.Id)

	otherUser := createTestUser(t)
	otherAccount := createTestAccount(t, otherUser.Id)

	// Add other as member
	member := &models.GroupMember{
		GroupId:   group.Id,
		AccountId: &otherAccount.Id,
		Role:      models.GroupMemberRoleMember,
	}
	testDB.Create(member)

	t.Run("success - owner removes member", func(t *testing.T) {
		c, w := newContext(t, http.MethodDelete, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/members/"+strconv.FormatUint(member.Id, 10), nil, account)
		setParams(c, gin.Params{
			{Key: "id", Value: strconv.FormatUint(group.Id, 10)},
			{Key: "member_id", Value: strconv.FormatUint(member.Id, 10)},
		})
		callHandler(RemoveMember, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify member was removed (soft delete)
		var count int64
		testDB.Model(&models.GroupMember{}).Where("id = ?", member.Id).Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("invalid group id - non numeric", func(t *testing.T) {
		c, w := newContext(t, http.MethodDelete, "/api/v1/groups/abc/members/1", nil, account)
		setParams(c, gin.Params{
			{Key: "id", Value: "abc"},
			{Key: "member_id", Value: "1"},
		})
		callHandler(RemoveMember, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid member id - non numeric", func(t *testing.T) {
		c, w := newContext(t, http.MethodDelete, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/members/abc", nil, account)
		setParams(c, gin.Params{
			{Key: "id", Value: strconv.FormatUint(group.Id, 10)},
			{Key: "member_id", Value: "abc"},
		})
		callHandler(RemoveMember, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("no permission - regular member cannot remove", func(t *testing.T) {
		// Recreate the member since previous test deleted it
		newMember := &models.GroupMember{
			GroupId:   group.Id,
			AccountId: &otherAccount.Id,
			Role:      models.GroupMemberRoleMember,
		}
		testDB.Create(newMember)

		thirdUser := createTestUser(t)
		thirdAccount := createTestAccount(t, thirdUser.Id)
		thirdMember := &models.GroupMember{
			GroupId:   group.Id,
			AccountId: &thirdAccount.Id,
			Role:      models.GroupMemberRoleMember,
		}
		testDB.Create(thirdMember)

		c, w := newContext(t, http.MethodDelete, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/members/"+strconv.FormatUint(thirdMember.Id, 10), nil, otherAccount)
		setParams(c, gin.Params{
			{Key: "id", Value: strconv.FormatUint(group.Id, 10)},
			{Key: "member_id", Value: strconv.FormatUint(thirdMember.Id, 10)},
		})
		callHandler(RemoveMember, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("admin can remove member", func(t *testing.T) {
		// Make otherAccount an admin
		adminMember := &models.GroupMember{
			GroupId:   group.Id,
			AccountId: &otherAccount.Id,
			Role:      models.GroupMemberRoleAdmin,
		}
		testDB.Create(adminMember)

		thirdUser := createTestUser(t)
		thirdAccount := createTestAccount(t, thirdUser.Id)
		removeMember := &models.GroupMember{
			GroupId:   group.Id,
			AccountId: &thirdAccount.Id,
			Role:      models.GroupMemberRoleMember,
		}
		testDB.Create(removeMember)

		c, w := newContext(t, http.MethodDelete, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/members/"+strconv.FormatUint(removeMember.Id, 10), nil, otherAccount)
		setParams(c, gin.Params{
			{Key: "id", Value: strconv.FormatUint(group.Id, 10)},
			{Key: "member_id", Value: strconv.FormatUint(removeMember.Id, 10)},
		})
		callHandler(RemoveMember, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("remove non-existent member", func(t *testing.T) {
		c, w := newContext(t, http.MethodDelete, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/members/99999", nil, account)
		setParams(c, gin.Params{
			{Key: "id", Value: strconv.FormatUint(group.Id, 10)},
			{Key: "member_id", Value: "99999"},
		})
		callHandler(RemoveMember, c)

		assert.Equal(t, http.StatusOK, w.Code) // still 200, just nothing to delete
	})
}

// ===================== GetGroup Tests =====================

func TestGetGroup(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	group := createTestGroup(t, account.Id)

	t.Run("success", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10), nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(GetGroup, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, group.Name, resp["name"])
	})

	t.Run("success - returns group with correct fields", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10), nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(GetGroup, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, float64(group.Id), resp["id"])
		assert.Equal(t, group.Name, resp["name"])
		assert.Equal(t, group.Description, resp["description"])
		assert.Equal(t, group.Visibility, resp["visibility"])
	})

	t.Run("invalid group id - non numeric", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/groups/abc", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "abc"}})
		callHandler(GetGroup, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("group not found", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/groups/99999", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "99999"}})
		callHandler(GetGroup, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// ===================== GetMyGroups Tests =====================

func TestGetMyGroups(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)

	t.Run("success - with groups", func(t *testing.T) {
		createTestGroup(t, account.Id)
		createTestGroup(t, account.Id)

		c, w := newContext(t, http.MethodGet, "/api/v1/groups/mine", nil, account)
		callHandler(GetMyGroups, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var groups []interface{}
		err := json.Unmarshal(w.Body.Bytes(), &groups)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(groups), 2)
	})

	t.Run("success - no groups", func(t *testing.T) {
		newUser := createTestUser(t)
		newAccount := createTestAccount(t, newUser.Id)

		c, w := newContext(t, http.MethodGet, "/api/v1/groups/mine", nil, newAccount)
		callHandler(GetMyGroups, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var groups []interface{}
		err := json.Unmarshal(w.Body.Bytes(), &groups)
		require.NoError(t, err)
		assert.Equal(t, 0, len(groups))
	})

	t.Run("no account in context panics", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.NotNil(t, r)
		}()
		c, _ := newContext(t, http.MethodGet, "/api/v1/groups/mine", nil, nil)
		callHandler(GetMyGroups, c)
	})

	t.Run("success - returns correct group names", func(t *testing.T) {
		newUser := createTestUser(t)
		newAccount := createTestAccount(t, newUser.Id)
		group1 := createTestGroup(t, newAccount.Id)

		c, w := newContext(t, http.MethodGet, "/api/v1/groups/mine", nil, newAccount)
		callHandler(GetMyGroups, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var groups []map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &groups)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(groups), 1)
		assert.Equal(t, group1.Name, groups[0]["name"])
	})
}

// ===================== GetGroupMessages Tests =====================

func TestGetGroupMessages(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	group := createTestGroup(t, account.Id)

	t.Run("success - member can see messages", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(GetGroupMessages, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var messages []interface{}
		err := json.Unmarshal(w.Body.Bytes(), &messages)
		require.NoError(t, err)
		assert.Equal(t, 0, len(messages)) // no messages yet
	})

	t.Run("success - with limit", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages?limit=10", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(GetGroupMessages, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - limit defaults to 50 when 0", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages?limit=0", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(GetGroupMessages, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("success - limit defaults to 50 when over 100", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages?limit=200", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(GetGroupMessages, c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("invalid group id - non numeric", func(t *testing.T) {
		c, w := newContext(t, http.MethodGet, "/api/v1/groups/abc/messages", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "abc"}})
		callHandler(GetGroupMessages, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not a member - forbidden", func(t *testing.T) {
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)

		c, w := newContext(t, http.MethodGet, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages", nil, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(GetGroupMessages, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("no account in context panics", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.NotNil(t, r)
		}()
		c, _ := newContext(t, http.MethodGet, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages", nil, nil)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(GetGroupMessages, c)
	})

	t.Run("success - member can see messages after sending", func(t *testing.T) {
		// Create a message
		msg := &models.GroupMessage{
			GroupId:  group.Id,
			SenderId: &account.Id,
			Content:  "Hello world",
		}
		testDB.Create(msg)

		c, w := newContext(t, http.MethodGet, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(GetGroupMessages, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var messages []map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &messages)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(messages), 1)
	})
}

// ===================== SendGroupMessage Tests =====================

func TestSendGroupMessage(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)
	group := createTestGroup(t, account.Id)

	t.Run("success", func(t *testing.T) {
		body, _ := json.Marshal(SendMessageRequest{
			Content: "Hello, group!",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(SendGroupMessage, c)

		assert.Equal(t, http.StatusCreated, w.Code)

		// Verify message was created
		var count int64
		testDB.Model(&models.GroupMessage{}).Where("group_id = ?", group.Id).Count(&count)
		assert.Equal(t, int64(1), count)
	})

	t.Run("success - response contains message data", func(t *testing.T) {
		body, _ := json.Marshal(SendMessageRequest{
			Content: "Test message content",
		})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(SendGroupMessage, c)

		assert.Equal(t, http.StatusCreated, w.Code)

		var resp map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "Test message content", resp["content"])
	})

	t.Run("invalid group id - non numeric", func(t *testing.T) {
		body, _ := json.Marshal(SendMessageRequest{Content: "Hi"})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/abc/messages", body, account)
		setParams(c, gin.Params{{Key: "id", Value: "abc"}})
		callHandler(SendGroupMessage, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing content - empty string", func(t *testing.T) {
		body, _ := json.Marshal(SendMessageRequest{Content: ""})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(SendGroupMessage, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("not a member - forbidden", func(t *testing.T) {
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)

		body, _ := json.Marshal(SendMessageRequest{Content: "Hi"})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages", body, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(SendGroupMessage, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("no account in context panics", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.NotNil(t, r)
		}()
		body, _ := json.Marshal(SendMessageRequest{Content: "Hi"})
		c, _ := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages", body, nil)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(SendGroupMessage, c)
	})

	t.Run("invalid json body", func(t *testing.T) {
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages", []byte("not json"), account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(SendGroupMessage, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("success - long message content", func(t *testing.T) {
		longContent := make([]byte, 5000)
		for i := range longContent {
			longContent[i] = 'a'
		}
		body, _ := json.Marshal(SendMessageRequest{Content: string(longContent)})
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/messages", body, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(SendGroupMessage, c)

		assert.Equal(t, http.StatusCreated, w.Code)
	})
}

// ===================== DeleteGroup Tests =====================

func TestDeleteGroup(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)

	t.Run("success", func(t *testing.T) {
		group := createTestGroup(t, account.Id)

		c, w := newContext(t, http.MethodDelete, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10), nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(DeleteGroup, c)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify group is soft-deleted
		var found models.Group
		err := testDB.Unscoped().First(&found, group.Id).Error
		assert.NoError(t, err)
		assert.NotNil(t, found.DeletedAt)
	})

	t.Run("success - group no longer queryable", func(t *testing.T) {
		group := createTestGroup(t, account.Id)

		c, _ := newContext(t, http.MethodDelete, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10), nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(DeleteGroup, c)

		// Try to find it with normal query
		var found models.Group
		err := testDB.First(&found, group.Id).Error
		assert.Error(t, err)
	})

	t.Run("invalid group id - non numeric", func(t *testing.T) {
		c, w := newContext(t, http.MethodDelete, "/api/v1/groups/abc", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "abc"}})
		callHandler(DeleteGroup, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("group not found", func(t *testing.T) {
		c, w := newContext(t, http.MethodDelete, "/api/v1/groups/99999", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "99999"}})
		callHandler(DeleteGroup, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("not owner - forbidden", func(t *testing.T) {
		group := createTestGroup(t, account.Id)
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)

		c, w := newContext(t, http.MethodDelete, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10), nil, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(DeleteGroup, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("no account in context panics", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.NotNil(t, r)
		}()
		c, _ := newContext(t, http.MethodDelete, "/api/v1/groups/1", nil, nil)
		setParams(c, gin.Params{{Key: "id", Value: "1"}})
		callHandler(DeleteGroup, c)
	})

	t.Run("success - verifies response message", func(t *testing.T) {
		group := createTestGroup(t, account.Id)

		c, w := newContext(t, http.MethodDelete, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10), nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(DeleteGroup, c)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "Group deleted successfully", resp["message"])
	})
}

// ===================== BridgeGroup Tests =====================

func TestBridgeGroup(t *testing.T) {
	setupTestDB(t)

	user := createTestUser(t)
	account := createTestAccount(t, user.Id)

	t.Run("invalid group id - non numeric", func(t *testing.T) {
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/abc/bridge", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "abc"}})
		callHandler(BridgeGroup, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("group not found", func(t *testing.T) {
		c, w := newContext(t, http.MethodPost, "/api/v1/groups/99999/bridge", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: "99999"}})
		callHandler(BridgeGroup, c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("not owner - forbidden", func(t *testing.T) {
		group := createTestGroup(t, account.Id)
		otherUser := createTestUser(t)
		otherAccount := createTestAccount(t, otherUser.Id)

		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/bridge", nil, otherAccount)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(BridgeGroup, c)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("matrix not enabled", func(t *testing.T) {
		group := createTestGroup(t, account.Id)

		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/bridge", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(BridgeGroup, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "not enabled")
	})

	t.Run("already bridged", func(t *testing.T) {
		group := createTestGroup(t, account.Id)
		matrixRoomID := "!test:matrix.org"
		testDB.Model(&models.Group{}).Where("id = ?", group.Id).Update("matrix_room_id", matrixRoomID)

		c, w := newContext(t, http.MethodPost, "/api/v1/groups/"+strconv.FormatUint(group.Id, 10)+"/bridge", nil, account)
		setParams(c, gin.Params{{Key: "id", Value: strconv.FormatUint(group.Id, 10)}})
		callHandler(BridgeGroup, c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "already bridged")
	})

	t.Run("no account in context panics", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.NotNil(t, r)
		}()
		c, _ := newContext(t, http.MethodPost, "/api/v1/groups/1/bridge", nil, nil)
		setParams(c, gin.Params{{Key: "id", Value: "1"}})
		callHandler(BridgeGroup, c)
	})
}
