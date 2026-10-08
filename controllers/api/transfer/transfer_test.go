package transfer

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	data "github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { database.SetTestMode(); gin.SetMode(gin.TestMode); os.Exit(m.Run()) }
func transferDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, e := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, e)
	sql, e := db.DB()
	require.NoError(t, e)
	sql.SetMaxOpenConns(1)
	old := database.GetDB()
	database.SetTestDB(db)
	t.Cleanup(func() { database.SetTestDB(old); sql.Close() })
	require.NoError(t, db.AutoMigrate(&models.User{}, &data.Job{}))
	for _, u := range []models.User{{Id: 1, UUID: "owner", UserName: "owner", EmailHash: "h1", Status: models.UserStatusNormal}, {Id: 2, UUID: "other", UserName: "other", EmailHash: "h2", Status: models.UserStatusNormal}, {Id: 3, UUID: "admin", UserName: "admin", EmailHash: "h3", Status: models.UserStatusNormal, IsAdmin: true}} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&u).Error)
	}
	return db
}
func TestTransferJobOwnerScopeAndAdminIsolation(t *testing.T) {
	db := transferDB(t)
	id := uuid.NewString()
	require.NoError(t, db.Create(&data.Job{ID: id, OwnerUserID: 1, OwnerAccountID: 10, Scope: "personal", State: "ready", Directory: "SECRET_PATH", UploadSHA256: "SECRET_HASH", LeaseToken: "SECRET_LEASE"}).Error)
	for _, tc := range []struct {
		name, scope   string
		user, account uint64
		status        int
	}{{"owner", "personal", 1, 10, 200}, {"other user", "personal", 2, 20, 404}, {"same user other account", "personal", 1, 11, 404}, {"nonadmin site", "site", 1, 10, 403}, {"admin cannot steal personal job", "site", 3, 30, 404}, {"unauthenticated", "personal", 0, 0, 401}} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if tc.user > 0 {
					c.Set("account", &models.Account{Id: tc.account, UserId: tc.user})
				}
			})
			r.GET("/jobs/:id", Get(tc.scope))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/jobs/"+id, nil))
			require.Equal(t, tc.status, w.Code)
			require.NotContains(t, w.Body.String(), "SECRET_")
		})
	}
}
func TestTransferOAuthRequiresWholeDataScope(t *testing.T) {
	for _, tc := range []struct {
		name, path, method string
		token              *models.AuthTokenData
		status             int
	}{{"anonymous", "/export", "GET", nil, 401}, {"profile only", "/export", "GET", &models.AuthTokenData{AuthType: models.AuthTokenTypeOauth, Scopes: &models.ApplicationScope{ReadScope: models.AppScopeReadAccounts}}, 403}, {"full read", "/export", "GET", &models.AuthTokenData{AuthType: models.AuthTokenTypeOauth, Scopes: &models.ApplicationScope{ReadScope: models.AppScopeRead}}, 204}, {"read cannot import", "/imports", "POST", &models.AuthTokenData{AuthType: models.AuthTokenTypeOauth, Scopes: &models.ApplicationScope{ReadScope: models.AppScopeRead}}, 403}, {"full write", "/imports", "POST", &models.AuthTokenData{AuthType: models.AuthTokenTypeOauth, Scopes: &models.ApplicationScope{WriteScope: models.AppScopeWrite}}, 204}, {"no admin scope", "/api/v1/admin/imports", "GET", &models.AuthTokenData{AuthType: models.AuthTokenTypeOauth, Scopes: &models.ApplicationScope{ReadScope: models.AppScopeRead}}, 403}, {"web", "/export", "GET", &models.AuthTokenData{AuthType: models.AuthTokenTypeWeb}, 204}} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if tc.token != nil {
					c.Set("token_data", tc.token)
				}
			}, AuthorizeTransfer)
			r.Handle(tc.method, tc.path, func(c *gin.Context) { c.Status(204) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, tc.status, w.Code)
		})
	}
}
func TestTransferListOnlyOwnedScope(t *testing.T) {
	db := transferDB(t)
	for _, j := range []data.Job{{ID: uuid.NewString(), OwnerUserID: 1, OwnerAccountID: 10, Scope: "personal"}, {ID: uuid.NewString(), OwnerUserID: 2, OwnerAccountID: 20, Scope: "personal"}, {ID: uuid.NewString(), OwnerUserID: 1, OwnerAccountID: 10, Scope: "site"}} {
		require.NoError(t, db.Create(&j).Error)
	}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("account", &models.Account{Id: 10, UserId: 1}) })
	r.GET("/jobs", List("personal"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/jobs", nil))
	require.Equal(t, 200, w.Code)
	var jobs []data.Job
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &jobs))
	require.Len(t, jobs, 1)
	require.Equal(t, "personal", jobs[0].Scope)
}

func TestTransferExecuteRejectsChangedRevisionAndUnknownJSON(t *testing.T) {
	db := transferDB(t)
	id := uuid.NewString()
	require.NoError(t, db.Create(&data.Job{ID: id, OwnerUserID: 1, OwnerAccountID: 10, Scope: "personal", State: "ready", Revision: "approved", Report: &data.Report{CanExecute: true}}).Error)
	for _, tc := range []struct {
		body   string
		status int
	}{{`{"revision":"changed"}`, 409}, {`{"revision":"approved","owner_user_id":2}`, 422}, {`{"revision":"approved"}{}`, 422}} {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("account", &models.Account{Id: 10, UserId: 1}) })
		r.POST("/jobs/:id", Execute("personal"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/jobs/"+id, strings.NewReader(tc.body)))
		require.Equal(t, tc.status, w.Code)
		var j data.Job
		require.NoError(t, db.First(&j, "id = ?", id).Error)
		require.Equal(t, "ready", j.State)
	}
}
func TestTransferDeleteCannotRemoveActiveJob(t *testing.T) {
	db := transferDB(t)
	for _, state := range []string{"running", "validating"} {
		id := uuid.NewString()
		require.NoError(t, db.Create(&data.Job{ID: id, OwnerUserID: 1, OwnerAccountID: 10, Scope: "personal", State: state}).Error)
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("account", &models.Account{Id: 10, UserId: 1}) })
		r.DELETE("/jobs/:id", Delete("personal"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("DELETE", "/jobs/"+id, nil))
		require.Equal(t, 409, w.Code)
		var count int64
		require.NoError(t, db.Model(&data.Job{}).Where("id = ?", id).Count(&count).Error)
		require.EqualValues(t, 1, count)
	}
}
