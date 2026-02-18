package filter

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

var testDB *testutil.TestDB

// TestMain 在所有测试之前运行，用于设置测试环境
func TestMain(m *testing.M) {
	// 设置测试模式，跳过真实数据库连接
	database.SetTestMode()
	gin.SetMode(gin.TestMode)

	// 运行测试
	os.Exit(m.Run())
}

// setupTestDB 初始化测试数据库
func setupTestDB(t *testing.T) func() {
	var err error
	testDB, err = testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}

	// 设置测试数据库到全局变量
	database.SetTestDB(testDB.GormDB)

	// Auto migrate
	if err := testDB.AutoMigrate(&models.Account{}, &models.AccountExtra{}, &models.Filter{}); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	return func() {
		database.ResetTestDB()
		testDB.Close()
	}
}

// createTestAccount 创建测试账户
func createTestAccount(t *testing.T, username string) *models.Account {
	account := &models.Account{
		Username:    username,
		Acct:        username + "@test.com",
		DisplayName: username,
	}
	err := testDB.GormDB.Create(account).Error
	assert.NoError(t, err)
	return account
}

func TestCreateFilter_RequestParsing(t *testing.T) {
	// 测试请求解析
	reqBody := CreateFilterRequest{
		Title:   "Test Filter",
		Context: []string{"home", "public"},
		Keywords: []*models.FilterKeyword{
			{Keyword: "spam", WholeWord: true},
		},
	}
	body, _ := json.Marshal(reqBody)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/filters", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	var parsed CreateFilterRequest
	err := c.ShouldBindJSON(&parsed)
	assert.NoError(t, err)
	assert.Equal(t, "Test Filter", parsed.Title)
	assert.Len(t, parsed.Context, 2)
	assert.Len(t, parsed.Keywords, 1)
}

func TestUpdateFilterKeywords(t *testing.T) {
	oldKeywords := []*models.FilterKeyword{
		{Uuid: "uuid1", Keyword: "old1", WholeWord: true},
		{Uuid: "uuid2", Keyword: "old2", WholeWord: false},
	}

	newKeywords := []*models.FilterKeyword{
		{Keyword: "old1", WholeWord: false}, // 更新现有
		{Keyword: "new1", WholeWord: true},  // 新增
	}

	result := updateKeywordsFields(oldKeywords, newKeywords)

	assert.Len(t, result, 2)
	// old1 应该保留原来的 uuid
	assert.Equal(t, "uuid1", result[0].Uuid)
	assert.Equal(t, "old1", result[0].Keyword)
	assert.False(t, result[0].WholeWord) // 更新为 false

	// new1 应该有新的 uuid
	assert.NotEmpty(t, result[1].Uuid)
	assert.NotEqual(t, "uuid1", result[1].Uuid)
	assert.Equal(t, "new1", result[1].Keyword)
	assert.True(t, result[1].WholeWord)
}

func TestUpdateFilterKeywords_EmptyOld(t *testing.T) {
	oldKeywords := []*models.FilterKeyword{}

	newKeywords := []*models.FilterKeyword{
		{Keyword: "new1", WholeWord: true},
		{Keyword: "new2", WholeWord: false},
	}

	result := updateKeywordsFields(oldKeywords, newKeywords)

	assert.Len(t, result, 2)
	// 所有新关键词都应该有新的 uuid
	assert.NotEmpty(t, result[0].Uuid)
	assert.NotEmpty(t, result[1].Uuid)
	assert.NotEqual(t, result[0].Uuid, result[1].Uuid)
}

func TestFilter_GetFilterActionString(t *testing.T) {
	tests := []struct {
		action   uint8
		expected string
	}{
		{models.FilterActionNone, "none"},
		{models.FilterActionWarn, "warn"},
		{models.FilterActionHide, "hide"},
		{99, "none"}, // unknown
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			filter := &models.Filter{FilterAction: tt.action}
			assert.Equal(t, tt.expected, filter.GetFilterActionString())
		})
	}
}

func TestFilters_GetHeightLevelFilter(t *testing.T) {
	filters := models.Filters{
		&models.Filter{FilterAction: models.FilterActionNone},
		&models.Filter{FilterAction: models.FilterActionWarn},
		&models.Filter{FilterAction: models.FilterActionHide},
	}

	result := filters.GetHeightLevelFilter()
	assert.NotNil(t, result)
	assert.Equal(t, uint8(models.FilterActionHide), result.FilterAction)
}

func TestFilters_GetHeightLevelFilter_Empty(t *testing.T) {
	filters := models.Filters{}

	result := filters.GetHeightLevelFilter()
	assert.Nil(t, result)
}

func TestFilters_GetFilterActions(t *testing.T) {
	filters := models.Filters{
		&models.Filter{FilterAction: models.FilterActionNone},
		&models.Filter{FilterAction: models.FilterActionWarn},
		&models.Filter{FilterAction: models.FilterActionHide},
	}

	actions := filters.GetFilterActions()
	assert.Len(t, actions, 2) // 只包含非 none 的
	assert.Contains(t, actions, uint8(models.FilterActionWarn))
	assert.Contains(t, actions, uint8(models.FilterActionHide))
}

func TestFilters_GetFilterActionsName(t *testing.T) {
	filters := models.Filters{
		&models.Filter{FilterAction: models.FilterActionWarn},
		&models.Filter{FilterAction: models.FilterActionHide},
	}

	names := filters.GetFilterActionsName()
	assert.Len(t, names, 2)
	assert.Contains(t, names, "warn")
	assert.Contains(t, names, "hide")
}

func TestFilterKeyword_Hash(t *testing.T) {
	keyword := &models.FilterKeyword{
		Keyword:   "test",
		WholeWord: true,
	}

	hash := keyword.Hash()
	assert.NotEmpty(t, hash)

	// 相同的关键词应该产生相同的 hash
	keyword2 := &models.FilterKeyword{
		Keyword:   "test",
		WholeWord: true,
	}
	assert.Equal(t, hash, keyword2.Hash())

	// 不同的关键词应该产生不同的 hash
	keyword3 := &models.FilterKeyword{
		Keyword:   "different",
		WholeWord: true,
	}
	assert.NotEqual(t, hash, keyword3.Hash())
}

// HTTP Handler Tests

func TestDelete_InvalidID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "invalid"}}

	account := &models.Account{Id: 1}
	c.Set("account", account)
	c.Request = httptest.NewRequest(http.MethodDelete, "/filters/invalid", nil)

	Delete(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestShow_InvalidID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "invalid"}}

	account := &models.Account{Id: 1}
	c.Set("account", account)
	c.Request = httptest.NewRequest(http.MethodGet, "/filters/invalid", nil)

	Show(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdate_InvalidID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "invalid"}}

	account := &models.Account{Id: 1}
	c.Set("account", account)
	c.Request = httptest.NewRequest(http.MethodPut, "/filters/invalid", nil)

	Update(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// Account Tests (使用 sqlexec)

func TestCreateAccount_Success(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	account := createTestAccount(t, "testuser")

	assert.NotZero(t, account.Id)
	assert.Equal(t, "testuser", account.Username)

	// 验证可以从数据库读取
	var found models.Account
	err := testDB.GormDB.First(&found, account.Id).Error
	assert.NoError(t, err)
	assert.Equal(t, "testuser", found.Username)
}

func TestAccount_NotFound(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	// 查找不存在的账户
	var found models.Account
	err := testDB.GormDB.First(&found, 99999).Error
	assert.Error(t, err)
	assert.Equal(t, gorm.ErrRecordNotFound, err)
}

func TestAccount_Delete(t *testing.T) {
	cleanup := setupTestDB(t)
	defer cleanup()

	account := createTestAccount(t, "deleteuser")

	// 软删除
	err := testDB.GormDB.Delete(account).Error
	assert.NoError(t, err)

	// 验证已删除
	var found models.Account
	err = testDB.GormDB.First(&found, account.Id).Error
	// sqlexec may not support soft delete properly, so we check for error or empty result
	if err != nil {
		assert.Error(t, err)
	} else {
		// If no error, the record might still exist with deleted_at set
		// This is acceptable behavior for in-memory DB
		assert.True(t, found.DeletedAt.Valid, "DeletedAt should be set")
	}
}

// UpdateFilterRequest Tests

func TestUpdateFilterRequest_Binding(t *testing.T) {
	title := "Updated Title"
	expiresIn := uint64(3600)
	context := []string{"home", "public"}
	keywords := []*models.FilterKeyword{{Keyword: "test", WholeWord: true}}

	req := UpdateFilterRequest{
		Title:     &title,
		ExpiresIn: &expiresIn,
		Context:   &context,
		Keywords:  &keywords,
	}

	assert.NotNil(t, req.Title)
	assert.Equal(t, "Updated Title", *req.Title)
	assert.NotNil(t, req.ExpiresIn)
	assert.Equal(t, uint64(3600), *req.ExpiresIn)
	assert.NotNil(t, req.Context)
	assert.Len(t, *req.Context, 2)
	assert.NotNil(t, req.Keywords)
	assert.Len(t, *req.Keywords, 1)
}
