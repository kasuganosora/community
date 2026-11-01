package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
)

func TestDelegatedAdminCapabilityAndOAuthScopeBothRequired(t *testing.T) {
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.Role{}, &models.RoleUser{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)
	role := models.Role{Name: "dashboardonly", Permissions: models.PermissionViewDashboard}
	require.NoError(t, setup.GormDB().Create(&role).Error)
	binding := models.RoleUser{RoleID: uint64(role.ID), UserID: 71}
	require.NoError(t, setup.GormDB().Create(&binding).Error)
	request := func(path string, oauth, scope bool) int {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			token := &models.AuthTokenData{AuthType: models.AuthTokenTypeWeb}
			if oauth {
				token.AuthType = models.AuthTokenTypeOauth
				token.Scopes = &models.ApplicationScope{}
				if scope {
					token.Scopes.AddScope(models.AppScopeTypeAdmin, models.AppScopeAdminRead)
				}
			}
			c.Set("token_data", token)
			c.Set("user", &models.User{Id: 71})
			c.Next()
		})
		router.GET(path, CheckScopeMiddleware(Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead}), func(c *gin.Context) { c.Status(204) })
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Code
	}
	require.Equal(t, 204, request("/api/v1/admin/measures", false, false))
	require.Equal(t, 403, request("/api/v1/admin/invites", false, false))
	require.Equal(t, 403, request("/api/v1/admin/data/imports", false, false))
	require.Equal(t, 403, request("/api/v1/admin/measures", true, false))
	require.Equal(t, 204, request("/api/v1/admin/measures", true, true))
	require.NoError(t, setup.GormDB().Delete(&binding).Error)
	require.Equal(t, 403, request("/api/v1/admin/measures", false, false))
}
func TestAdminCapabilityPathBoundaries(t *testing.T) {
	for _, path := range []string{"/api/v1/admin/appeals", "/api/v2/admin/appeals/1/resolve", "/api/v1/admin/new-account-reviews", "/api/v2/admin/new-account-reviews/1"} {
		require.EqualValues(t, models.PermissionManageReports, adminRoutePermission(path))
	}
	require.EqualValues(t, 0, adminRoutePermission("/api/v1/admin/invites-private"))
	require.EqualValues(t, 0, adminRoutePermission("/api/v1/admin/unknown"))
	require.EqualValues(t, models.PermissionDevops, adminRoutePermission("/api/v1/admin/federation/jobs/1"))
	require.EqualValues(t, models.PermissionDevops, adminRoutePermission("/api/v1/admin/federation/queue-stats"))
	require.EqualValues(t, models.PermissionDevops, adminRoutePermission("/api/v1/admin/federation/queue-bulk/delivery"))
	require.EqualValues(t, models.PermissionManageFederation, adminRoutePermission("/api/v1/admin/federation/instances"))
	require.EqualValues(t, models.PermissionManageWebhooks, adminRoutePermission("/api/v1/admin/webhook-deliveries"))
	require.EqualValues(t, models.PermissionDevops, adminRoutePermission("/api/v1/admin/seo/sitemap"))
}

func TestReportModeratorsCanReachAppealsAndPostReviews(t *testing.T) {
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.Role{}, &models.RoleUser{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)
	role := models.Role{Name: "reviewer", Permissions: models.PermissionManageReports}
	require.NoError(t, setup.GormDB().Create(&role).Error)
	require.NoError(t, setup.GormDB().Create(&models.RoleUser{RoleID: uint64(role.ID), UserID: 71}).Error)
	for _, path := range []string{"/api/v1/admin/appeals", "/api/v1/admin/new-account-reviews"} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set("token_data", &models.AuthTokenData{AuthType: models.AuthTokenTypeWeb})
			c.Set("user", &models.User{Id: 71})
			c.Next()
		})
		router.GET(path, CheckScopeMiddleware(Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminReadReports}), func(c *gin.Context) { c.Status(204) })
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 204, w.Code, path)
	}
}
