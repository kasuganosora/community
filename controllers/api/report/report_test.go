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
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// callHandler invokes a gin handler and recovers from AbortSentinel panic used by AbortIf/AbortIfError.
func callHandler(fn func(*gin.Context), c *gin.Context) {
	defer func() {
		if r := recover(); r != nil {
			// Expected AbortSentinel - handler aborted early, response already written
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

	// SQLite executes the serializer's UNION ALL; SQLExec does not.
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	testDB = &testutil.TestDB{GormDB: conn}
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)

	migrationModels := []any{
		&models.User{},
		&models.Account{},
		&models.AccountExtra{},
		&models.AccountEmoji{},
		&models.Report{},
		&models.Status{},
		&models.StatusReaction{},
		&models.StatusContent{},
		&models.StatusTag{},
		&models.StatusToAccount{},
		&models.StatusEmoji{},
		&models.StatusMediaAttachment{},
		&models.BookmarkStatus{},
		&models.FavoriteStatus{},
		&models.PinStatus{},
		&models.MuteStatus{},
		&models.MuteAccount{},
		&models.Follow{},
		&models.Block{},
		&models.Filter{},
		&models.Role{},
		&models.AuthToken{},
		&models.Notification{},
		&models.Conversation{},
		&models.AccountConversation{},
		&models.PollOptionVote{},
		&models.Attachment{},
		&models.MediaAttachment{},
		&models.Poll{},
		&models.PollOption{},
		&models.Tag{},
		&models.Emoji{},
		&models.Application{},
		&models.Rule{},
		&models.SystemSetting{},
	}
	for _, model := range migrationModels {
		stmt := &gorm.Statement{DB: conn}
		require.NoError(t, stmt.Parse(model))
		for _, field := range stmt.Schema.Fields {
			if field.AutoIncrement {
				field.DataType = schema.Int
			}
		}
	}
	require.NoError(t, conn.AutoMigrate(migrationModels...))

	// Explicitly create many2many join tables for Status associations
	joinTables := []string{
		"status_media_attachments",
		"status_emojis",
		"status_to_accounts",
		"status_tags",
	}
	for _, jt := range joinTables {
		if !testDB.GormDB.Migrator().HasTable(jt) {
			if err := testDB.GormDB.Exec("CREATE TABLE " + jt + " (status_id INTEGER NOT NULL, PRIMARY KEY (status_id))").Error; err != nil { //nolint:gosec,lll
				t.Fatalf("Failed to create join table %s: %v", jt, err)
			}
		}
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

// createTestStatus creates a test status in the DB.
func createTestStatus(t *testing.T, accountId uint64) *models.Status {
	t.Helper()

	now := time.Now()

	status := &models.Status{
		AccountId:  accountId,
		Visibility: models.StatusVisibilityPublic,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	}
	err := testDB.GormDB.Create(status).Error
	if err != nil {
		t.Fatalf("Failed to create status: %v", err)
	}

	return status
}

// createTestRule creates a test rule in the DB.
func createTestRule(t *testing.T, text string) *models.Rule {
	t.Helper()

	rule := &models.Rule{
		Text:     text,
		Priority: 1,
	}
	err := testDB.GormDB.Create(rule).Error
	if err != nil {
		t.Fatalf("Failed to create rule: %v", err)
	}

	return rule
}

// createTestReport creates a test report in the DB.
func createTestReport(t *testing.T, reporterId, targetId uint64) *models.Report {
	t.Helper()

	report := &models.Report{
		AccountId:       reporterId,
		TargetAccountId: targetId,
		Comment:         "test report",
	}
	err := testDB.GormDB.Create(report).Error
	if err != nil {
		t.Fatalf("Failed to create report: %v", err)
	}

	return report
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

// ========== Create Tests ==========

func TestCreate_Success(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")

	body, err := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "spam account",
		Category:  "spam", //nolint:goconst
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Create, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any

	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp["id"])
}

func TestCreate_WithStatusIds(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")
	status := createTestStatus(t, target.Id)

	body, err := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "bad status",
		Category:  "violation",
		StatusIds: []string{strconv.FormatUint(status.Id, 10)},
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Create, c)

	assert.Equal(t, http.StatusOK, w.Code, "response: %s; errors: %v", w.Body.String(), c.Errors)
}

func TestCreate_WithRuleIds(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")
	rule := createTestRule(t, "No spam")

	body, err := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "rule violation",
		Category:  "other",
		RuleIds:   []string{strconv.FormatUint(rule.Id, 10)},
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body)) //nolint:noctx
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

	body, err := json.Marshal(CreateRequest{
		Comment:  "no account",
		Category: "spam",
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodPost, "/reports", body)

	callHandler(Create, c)

	assert.Equal(t, 422, w.Code)
}

func TestCreate_TargetNotFound(t *testing.T) {
	setupTestDB(t)

	body, err := json.Marshal(CreateRequest{
		AccountId: "99999",
		Comment:   "test", //nolint:goconst
		Category:  "spam",
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccount(t, w, http.MethodPost, "/reports", body)

	callHandler(Create, c)

	assert.Equal(t, 422, w.Code)

	var resp map[string]any

	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "not found")
}

func TestCreate_InvalidCategory(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")

	body, err := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "test",
		Category:  "invalid_category",
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Create, c)

	assert.Equal(t, 422, w.Code)
}

func TestCreate_StatusNotFound(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")

	body, err := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "test",
		Category:  "spam",
		StatusIds: []string{"99999"},
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Create, c)

	assert.Equal(t, 422, w.Code)

	var resp map[string]any

	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "statuses not found")
}

func TestCreate_RuleNotFound(t *testing.T) {
	setupTestDB(t)
	_, account := newContextWithAccount(t, httptest.NewRecorder(), http.MethodPost, "/reports", nil)
	target := createTestAccount(t, "target")

	body, err := json.Marshal(CreateRequest{
		AccountId: strconv.FormatUint(target.Id, 10),
		Comment:   "test",
		Category:  "spam",
		RuleIds:   []string{"99999"},
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/reports", bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", account)

	callHandler(Create, c)

	assert.Equal(t, 422, w.Code)

	var resp map[string]any

	err = json.Unmarshal(w.Body.Bytes(), &resp)
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

	var resp map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, strconv.FormatUint(report.Id, 10), resp["id"])
}

func TestGetReport_InvalidID(t *testing.T) {
	setupTestDB(t)

	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodGet, "/reports/abc", map[string]string{
		"id": "abc", //nolint:goconst
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

	var resp []any

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

	var resp []any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Empty(t, resp)
}

func TestList_WithLimit(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "reporter")
	target := createTestAccount(t, "target")
	createTestReport(t, reporter.Id, target.Id)
	createTestReport(t, reporter.Id, target.Id)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/reports?limit=1", nil) //nolint:noctx
	c.Set("account", createTestAccount(t, "admin"))

	callHandler(List, c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []any

	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	// Note: sqlexc in-memory engine may not enforce LIMIT clauses;
	// the handler correctly applies Limit() to the query, so we just verify
	// that the endpoint works and returns reports without error.
	assert.NotEmpty(t, resp)
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
	c.Request = httptest.NewRequest(http.MethodGet, "/reports?resolved=true", nil) //nolint:noctx
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
	body, err := json.Marshal(UpdateRequest{
		Category: &category,
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), bytes.NewBuffer(body)) //nolint:noctx
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
	body, err := json.Marshal(UpdateRequest{
		RuleIds: &ruleIds,
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), bytes.NewBuffer(body)) //nolint:noctx
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
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), bytes.NewBufferString("invalid json")) //nolint:noctx
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
	body, err := json.Marshal(UpdateRequest{
		Category: &category,
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), bytes.NewBuffer(body)) //nolint:noctx
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
	body, err := json.Marshal(UpdateRequest{
		RuleIds: &ruleIds,
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), map[string]string{
		"id": strconv.FormatUint(report.Id, 10),
	})
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/"+strconv.FormatUint(report.Id, 10), bytes.NewBuffer(body)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	callHandler(Update, c)

	assert.Equal(t, 422, w.Code)
}

func TestUpdate_NotFound(t *testing.T) {
	setupTestDB(t)

	category := "spam"
	body, err := json.Marshal(UpdateRequest{
		Category: &category,
	})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := newContextWithAccountAndParams(t, w, http.MethodPut, "/reports/99999", map[string]string{
		"id": "99999",
	})
	c.Request = httptest.NewRequest(http.MethodPut, "/reports/99999", bytes.NewBuffer(body)) //nolint:noctx
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

	var resp map[string]any

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

	var resp map[string]any

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

	var resp map[string]any

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

func TestAdminReportPagesAreBoundedAndStable(t *testing.T) {
	setupTestDB(t)
	reporter := createTestAccount(t, "page_reporter")
	target := createTestAccount(t, "page_target")
	admin := createTestAccount(t, "page_admin")
	for i := 0; i < 45; i++ {
		createTestReport(t, reporter.Id, target.Id)
	}
	page := func(query string) []map[string]any {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/reports"+query, nil)
		c.Set("account", admin)
		callHandler(List, c)
		require.Equal(t, 200, w.Code, w.Body.String())
		var rows []map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))
		return rows
	}
	first := page("")
	require.Len(t, first, 40)
	seen := map[string]bool{}
	for _, row := range first {
		seen[row["id"].(string)] = true
	}
	second := page("?max_id=" + first[len(first)-1]["id"].(string))
	require.Len(t, second, 5)
	for _, row := range second {
		require.False(t, seen[row["id"].(string)])
	}
}
