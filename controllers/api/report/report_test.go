package report

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

// callHandler recovers from panic("abort") used by controllers.AbortIf/AbortIfError
func callHandler(fn func(*gin.Context), c *gin.Context) {
	defer func() {
		if r := recover(); r == "abort" {
			// expected - handler aborted early
			_ = r
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
		&models.Report{},
		&models.Status{},
		&models.StatusContent{},
		&models.Rule{},
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

// createTestStatus creates a test status in the DB
func createTestStatus(t *testing.T, accountId uint64) *models.Status {
	t.Helper()
	now := time.Now()
	status := &models.Status{
		AccountId:  accountId,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}
	if err := testDB.GormDB.Create(status).Error; err != nil {
		t.Fatalf("Failed to create status: %v", err)
	}
	return status
}

// createTestRule creates a test rule in the DB
func createTestRule(t *testing.T, text string) *models.Rule {
	t.Helper()
	rule := &models.Rule{
		Text:     text,
		Priority: 1,
	}
	if err := testDB.GormDB.Create(rule).Error; err != nil {
		t.Fatalf("Failed to create rule: %v", err)
	}
	return rule
}

// createTestReport creates a test report in the DB
func createTestReport(t *testing.T, reporterId, targetId uint64) *models.Report {
	t.Helper()
	report := &models.Report{
		AccountId:       reporterId,
		TargetAccountId: targetId,
		Comment:         "test report",
	}
	if err := testDB.GormDB.Create(report).Error; err != nil {
		t.Fatalf("Failed to create report: %v", err)
	}
	return report
}

// newContextWithAccount creates a gin context with account set
func newContextWithAccount(t *testing.T, w *httptest.ResponseRecorder, method, path string, body []byte) (*gin.Context, *models.Account) {
	t.Helper()
	account := createTestAccount(t, "testuser")
	c, _ := gin.CreateTestContext(w)
	if body != nil {
		c.Request = httptest.NewRequest(method, path, bytes.NewBuffer(body))
		c.Request.Header.Set("Content-Type", "application/json")
	} else {
		c.Request = httptest.NewRequest(method, path, nil)
	}
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

// ========== Create Tests ==========

func TestCreate_Success(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")

	body, _ := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "spam account",
		Category:  "spam",
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Create, c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.NotEmpty(t, resp["id"])
}

func TestCreate_WithStatusIds(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")
	status := createTestStatus(t, target.Id)

	body, _ := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "bad status",
		Category:  "violation",
		StatusIds: []string{strconv.FormatUint(status.Id, 10)},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Create, c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCreate_WithRuleIds(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")
	rule := createTestRule(t, "No spam")

	body, _ := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "rule violation",
		Category:  "other",
		RuleIds:   []string{strconv.FormatUint(rule.Id, 10)},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Create, c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCreate_InvalidJSON(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodPost, "/reports", []byte("invalid"))

	callHandler(Create, c)

	assert.Equal(t, 422, w.Code)
}

func TestCreate_MissingAccountId(t *testing.T) {
	setupTestDB(t)
	body, _ := json.Marshal(CreateRequest{
		Comment:  "no account",
		Category: "spam",
	})
	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodPost, "/reports", body)

	callHandler(Create, c)

	assert.Equal(t, 422, w.Code)
}

func TestCreate_TargetNotFound(t *testing.T) {
	setupTestDB(t)
	body, _ := json.Marshal(CreateRequest{
		AccountId: "99999",
		Comment:   "test",
		Category:  "spam",
	})
	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodPost, "/reports", body)

	callHandler(Create, c)

	assert.Equal(t, 422, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "not found")
}

func TestCreate_InvalidCategory(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")

	body, _ := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "test",
		Category:  "invalid_category",
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Create, c)

	assert.Equal(t, 422, w.Code)
}

func TestCreate_StatusNotFound(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")

	body, _ := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "test",
		Category:  "spam",
		StatusIds: []string{"99999"},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Create, c)

	assert.Equal(t, 422, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "statuses not found")
}

func TestCreate_RuleNotFound(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")

	body, _ := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "test",
		Category:  "spam",
		RuleIds:   []string{"99999"},
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Create, c)

	assert.Equal(t, 422, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "rule not found")
}

// ========== GetReport Tests ==========

func TestGetReport_Success(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	report := createTestReport(t, reporter.Id, target.Id)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodGet, "/reports/"+strconv.FormatUint(report.Id, 10), map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})

	callHandler(GetReport, c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, strconv.FormatUint(report.Id, 10), resp["id"])
}

func TestGetReport_InvalidID(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodGet, "/reports/abc", map[string]string{
		"id": "abc",
	})

	callHandler(GetReport, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetReport_NotFound(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodGet, "/reports/99999", map[string]string{
		"id": "99999",
	})

	callHandler(GetReport, c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ========== List Tests ==========

func TestList_Success(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	createTestReport(t, reporter.Id, target.Id)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodGet, "/reports", nil)

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(resp), 1)
}

func TestList_Empty(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodGet, "/reports", nil)

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Len(t, resp, 0)
}

func TestList_WithLimit(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	createTestReport(t, reporter.Id, target.Id)
	createTestReport(t, reporter.Id, target.Id)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/reports?limit=1", nil)
	c.Set("account", createTestAccount(t, "admin"))

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp []interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(resp), 1)
}

func TestList_ResolvedFilter(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	r := createTestReport(t, reporter.Id, target.Id)

	// Mark as resolved
	now := time.Now()
	testDB.GormDB.Model(r).Update("action_taken_at", &now)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/reports?resolved=true", nil)
	c.Set("account", createTestAccount(t, "admin"))

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)
}

// ========== Update Tests ==========

func TestUpdate_Success_Category(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	report := createTestReport(t, reporter.Id, target.Id)

	category := "spam"
	body, _ := json.Marshal(UpdateRequest{
		Category: &category,
	})
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(Update, c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUpdate_Success_RuleIds(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	rule := createTestRule(t, "No spam")
	report := createTestReport(t, reporter.Id, target.Id)

	ruleIds := []string{strconv.FormatUint(rule.Id, 10)}
	body, _ := json.Marshal(UpdateRequest{
		RuleIds: &ruleIds,
	})
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(Update, c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUpdate_InvalidJSON(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	report := createTestReport(t, reporter.Id, target.Id)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), bytes.NewBuffer([]byte("invalid json")))
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(Update, c)

	assert.Equal(t, 422, w.Code)
}

func TestUpdate_InvalidCategory(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	report := createTestReport(t, reporter.Id, target.Id)

	category := "invalid"
	body, _ := json.Marshal(UpdateRequest{
		Category: &category,
	})
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(Update, c)

	assert.Equal(t, 422, w.Code)
}

func TestUpdate_RuleNotFound(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	report := createTestReport(t, reporter.Id, target.Id)

	ruleIds := []string{"99999"}
	body, _ := json.Marshal(UpdateRequest{
		RuleIds: &ruleIds,
	})
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(Update, c)

	assert.Equal(t, 422, w.Code)
}

func TestUpdate_NotFound(t *testing.T) {
	setupTestDB(t)
	category := "spam"
	body, _ := json.Marshal(UpdateRequest{
		Category: &category,
	})
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPut, "/reports/99999", map[string]string{
		"id": "99999",
	})
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/99999", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(Update, c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ========== Delete Tests ==========

func TestDelete_Success(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	report := createTestReport(t, reporter.Id, target.Id)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodDelete, "/reports/"+strconv.FormatUint(report.Id, 10), map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})

	callHandler(Delete, c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestDelete_InvalidID(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodDelete, "/reports/abc", map[string]string{
		"id": "abc",
	})

	callHandler(Delete, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDelete_NotFound(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodDelete, "/reports/99999", map[string]string{
		"id": "99999",
	})

	callHandler(Delete, c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ========== AssignToSelf Tests ==========

func TestAssignToSelf_Success(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	report := createTestReport(t, reporter.Id, target.Id)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/"+strconv.FormatUint(report.Id, 10)+"/assign_to_self", map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})

	callHandler(AssignToSelf, c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp["id"])
}

func TestAssignToSelf_InvalidID(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/abc/assign_to_self", map[string]string{
		"id": "abc",
	})

	callHandler(AssignToSelf, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAssignToSelf_NotFound(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/99999/assign_to_self", map[string]string{
		"id": "99999",
	})

	callHandler(AssignToSelf, c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ========== Unassign Tests ==========

func TestUnassign_Success(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	report := createTestReport(t, reporter.Id, target.Id)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/"+strconv.FormatUint(report.Id, 10)+"/unassign", map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})

	callHandler(Unassign, c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUnassign_InvalidID(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/abc/unassign", map[string]string{
		"id": "abc",
	})

	callHandler(Unassign, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUnassign_NotFound(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/99999/unassign", map[string]string{
		"id": "99999",
	})

	callHandler(Unassign, c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ========== Resolve Tests ==========

func TestResolve_Success(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	report := createTestReport(t, reporter.Id, target.Id)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/"+strconv.FormatUint(report.Id, 10)+"/resolve", map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})

	callHandler(Resolve, c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, true, resp["action_taken"])
}

func TestResolve_InvalidID(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/abc/resolve", map[string]string{
		"id": "abc",
	})

	callHandler(Resolve, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestResolve_NotFound(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/99999/resolve", map[string]string{
		"id": "99999",
	})

	callHandler(Resolve, c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ========== Reopen Tests ==========

func TestReopen_Success(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	report := createTestReport(t, reporter.Id, target.Id)

	// First resolve the report
	now := time.Now()
	testDB.GormDB.Model(report).Update("action_taken_at", &now)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/"+strconv.FormatUint(report.Id, 10)+"/reopen", map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})

	callHandler(Reopen, c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, false, resp["action_taken"])
}

func TestReopen_InvalidID(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/abc/reopen", map[string]string{
		"id": "abc",
	})

	callHandler(Reopen, c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestReopen_NotFound(t *testing.T) {
	setupTestDB(t)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPost, "/reports/99999/reopen", map[string]string{
		"id": "99999",
	})

	callHandler(Reopen, c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
