package appeal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/newacctmod"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func appealDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "appeal.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&newacctmod.ReviewJob{}, &newacctmod.Appeal{}))
	database.SetTestMode()
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	return conn
}

func call(t *testing.T, handler gin.HandlerFunc, account *models.Account, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if account != nil {
		c.Set("account", account)
	}
	handler(c)
	c.Writer.WriteHeaderNow()
	return w
}

func TestCreateAppealForAutomaticSuspension(t *testing.T) {
	conn := appealDB(t)
	require.NoError(t, conn.Create(&newacctmod.ReviewJob{
		StatusID: 15, AccountID: 7, State: "done", Verdict: "suspend", NextAttemptAt: time.Now(),
	}).Error)
	account := &models.Account{Id: 7, Username: "held", IsBan: true}
	w := call(t, Create, account, http.MethodPost, "/api/v1/appeals", `{"text":"this was a mistake"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "open", body["state"])
	require.Equal(t, "15", body["status_id"])

	again := call(t, Create, account, http.MethodPost, "/api/v1/appeals", `{"text":"again"}`)
	require.Equal(t, http.StatusUnprocessableEntity, again.Code)
}

func TestCreateAppealRejectsOpenAccountAndMissingCase(t *testing.T) {
	_ = appealDB(t)
	open := &models.Account{Id: 3, Username: "ok"}
	w := call(t, Create, open, http.MethodPost, "/api/v1/appeals", `{"text":"hello"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)

	banned := &models.Account{Id: 4, Username: "banned", IsBan: true}
	missing := call(t, Create, banned, http.MethodPost, "/api/v1/appeals", `{"text":"hello"}`)
	require.Equal(t, http.StatusNotFound, missing.Code)
}

func resolveCall(t *testing.T, admin *models.Account, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/appeals/1/resolve", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: "1"}}
	ctx.Set("account", admin)
	Resolve(ctx)
	return w
}

func TestResolveAppealOnce(t *testing.T) {
	conn := appealDB(t)
	row, err := newacctmod.CreateAppeal(conn, 7, 15, "please review")
	require.NoError(t, err)
	admin := &models.Account{Id: 1, Username: "mod"}
	w := resolveCall(t, admin, `{"accept":true,"note":"ok"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var saved newacctmod.Appeal
	require.NoError(t, conn.First(&saved, row.ID).Error)
	require.Equal(t, "accepted", saved.State)
	require.Equal(t, uint64(1), saved.ResolvedByID)

	w2 := resolveCall(t, admin, `{"accept":false}`)
	require.Equal(t, http.StatusUnprocessableEntity, w2.Code)
}

func TestAppealContextExplainsEligibilityAndClosedCases(t *testing.T) {
	conn := appealDB(t)
	account := &models.Account{Id: 7, Username: "held", IsBan: true}
	request := func() map[string]any {
		w := call(t, List, account, "GET", "/api/v1/appeals?include_context=true", "")
		require.Equal(t, 200, w.Code, w.Body.String())
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return body
	}
	require.Nil(t, request()["suspension"])
	require.NoError(t, conn.Create(&newacctmod.ReviewJob{StatusID: 15, AccountID: 7, State: "done", Verdict: "suspend", NextAttemptAt: time.Now()}).Error)
	require.Equal(t, true, request()["suspension"].(map[string]any)["can_appeal"])
	row, err := newacctmod.CreateAppeal(conn, 7, 15, "please review")
	require.NoError(t, err)
	_, err = newacctmod.ResolveAppeal(conn, row.ID, 1, false, "reviewed")
	require.NoError(t, err)
	require.Equal(t, false, request()["suspension"].(map[string]any)["can_appeal"])
	account.IsBan = false
	require.Equal(t, false, request()["suspended"])
	require.Nil(t, request()["suspension"])
	legacy := call(t, List, account, "GET", "/api/v1/appeals", "")
	var items []any
	require.NoError(t, json.Unmarshal(legacy.Body.Bytes(), &items))
	require.Len(t, items, 1)
}

func TestReviewsListShowsHeldPostAndModeratorActionIsValidated(t *testing.T) {
	conn := appealDB(t)
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.Status{}, &models.StatusContent{}))
	account := &models.Account{Id: 7, Username: "alice", Acct: "alice", DisplayName: "Alice"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(account).Error)
	post := &models.Status{Id: 15, AccountId: 7, LocalOnly: true, Content: models.StatusContent{Content: "needs review"}}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(post).Error)
	job := &newacctmod.ReviewJob{StatusID: 15, AccountID: 7, State: "done", Verdict: "flag", NextAttemptAt: time.Now()}
	require.NoError(t, conn.Create(job).Error)
	w := call(t, Reviews, account, "GET", "/api/v1/admin/new-account-reviews", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var body struct {
		Items   []map[string]any `json:"items"`
		HasMore bool             `json:"has_more"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
	require.Equal(t, "alice", body.Items[0]["acct"])
	require.Equal(t, "needs review", body.Items[0]["text"])
	require.Equal(t, true, body.Items[0]["held"])
	require.Equal(t, true, body.Items[0]["actionable"])
	require.False(t, body.HasMore)
	w = call(t, Review, nil, "POST", "/api/v1/admin/new-account-reviews/1", `{"action":"approve"}`)
	require.Equal(t, 401, w.Code)
	w = call(t, Review, account, "POST", "/api/v1/admin/new-account-reviews/invalid", `{"action":"delete"}`)
	require.Equal(t, 422, w.Code)
}

func TestUserLevelSuspensionDoesNotSendAppealPageBackHome(t *testing.T) {
	_ = appealDB(t)
	w := call(t, func(c *gin.Context) {
		c.Set("user", &models.User{IsBan: true})
		List(c)
	}, &models.Account{Id: 7}, "GET", "/api/v1/appeals?include_context=true", "")
	require.Equal(t, 200, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, true, body["suspended"])
	require.Nil(t, body["suspension"])
}
