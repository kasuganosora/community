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
