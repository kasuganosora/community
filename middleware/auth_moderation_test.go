package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExistingWebAndOAuthTokensRespectCurrentModeration(t *testing.T) {
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.User{}, &models.Account{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)
	useTestRedis(t)
	old := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = old })
	conn := setup.GormDB()
	user := models.User{Id: 71, UUID: "moderation-token", UserName: "moderationtoken", EmailHash: "moderation-email"}
	account := models.Account{Id: 72, UserId: user.Id, Username: user.UserName}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)
	for _, kind := range []uint8{models.AuthTokenTypeWeb, models.AuthTokenTypeOauth} {
		token := (models.AuthTokenData{UserId: user.Id, AuthType: kind, Scopes: &models.ApplicationScope{ReadScope: models.AppScopeRead}}).GetTokenStr(config.C.EncryptedKey)
		require.NotEmpty(t, token)
		require.NoError(t, globalRedisClient.Set(context.Background(), tokenCacheKey(token), "valid", tokenCacheTTL).Err())
		request := func() int {
			r := gin.New()
			r.Use(AuthMiddleware())
			r.GET("/protected", func(c *gin.Context) { c.Status(204) })
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			r.ServeHTTP(w, req)
			return w.Code
		}
		require.Equal(t, 204, request())
		require.NoError(t, conn.Model(&user).UpdateColumn("is_ban", true).Error)
		require.Equal(t, 401, request())
		require.NoError(t, conn.Model(&user).UpdateColumn("is_ban", false).Error)
		require.Equal(t, 204, request())
		require.NoError(t, conn.Model(&user).UpdateColumn("status", models.UserStatusRejected).Error)
		require.Equal(t, 401, request())
		require.NoError(t, conn.Model(&user).UpdateColumn("status", models.UserStatusNormal).Error)
		require.Equal(t, 204, request())
		require.NoError(t, conn.Model(&account).UpdateColumn("is_ban", true).Error)
		require.Equal(t, 401, request())
		require.NoError(t, conn.Model(&account).UpdateColumn("is_ban", false).Error)
		require.Equal(t, 204, request())
	}
}

func TestSuspendedAccountCanReachAppealRouteOnly(t *testing.T) {
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.User{}, &models.Account{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)
	useTestRedis(t)
	old := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = old })
	conn := setup.GormDB()
	user := models.User{Id: 81, UUID: "appeal-token", UserName: "appealtoken", EmailHash: "appeal-email", IsBan: true, Status: models.UserStatusNormal}
	account := models.Account{Id: 82, UserId: user.Id, Username: user.UserName, IsBan: true}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)
	token := (models.AuthTokenData{UserId: user.Id, AuthType: models.AuthTokenTypeWeb}).GetTokenStr(config.C.EncryptedKey)
	require.NotEmpty(t, token)
	require.NoError(t, globalRedisClient.Set(context.Background(), tokenCacheKey(token), "valid", tokenCacheTTL).Err())

	r := gin.New()
	r.Use(AuthMiddleware())
	r.POST("/api/v1/appeals", func(c *gin.Context) { c.Status(204) })
	r.GET("/api/v1/appeals", func(c *gin.Context) { c.Status(204) })
	r.POST("/api/v1/reports", func(c *gin.Context) { c.Status(204) })
	call := func(method, path string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)
		return w.Code
	}
	require.Equal(t, 204, call(http.MethodPost, "/api/v1/appeals"))
	require.Equal(t, 204, call(http.MethodGet, "/api/v1/appeals"))
	require.Equal(t, 401, call(http.MethodPost, "/api/v1/reports"))

	require.NoError(t, conn.Model(&user).UpdateColumn("status", models.UserStatusRejected).Error)
	require.Equal(t, 401, call(http.MethodPost, "/api/v1/appeals"))
}

func TestSuspendedAccountCookiePreservedForAppeal(t *testing.T) {
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.User{}, &models.Account{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)
	useTestRedis(t)
	old := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = old })

	conn := setup.GormDB()
	user := models.User{Id: 91, UUID: "appeal-cookie", UserName: "appealcookie", EmailHash: "appeal-cookie-email", IsBan: true, Status: models.UserStatusNormal}
	account := models.Account{Id: 92, UserId: user.Id, Username: user.UserName, IsBan: true}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)

	token := (models.AuthTokenData{UserId: user.Id, AuthType: models.AuthTokenTypeWeb}).GetTokenStr(config.C.EncryptedKey)
	require.NoError(t, globalRedisClient.Set(context.Background(), tokenCacheKey(token), "valid", tokenCacheTTL).Err())
	appCookie := &http.Cookie{Name: AppTokenCookie, Value: url.QueryEscape(token)}

	r := gin.New()
	r.Use(AuthMiddleware())
	r.GET("/api/v1/accounts/verify_credentials", func(c *gin.Context) { c.Status(200) })
	r.GET("/api/v1/appeals", func(c *gin.Context) { c.Status(204) })

	// 1. verify_credentials rejects with 401 but preserves app-token cookie so /appeals probe works.
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/verify_credentials", nil)
	req1.AddCookie(appCookie)
	r.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusUnauthorized, w1.Code)
	require.Empty(t, w1.Result().Cookies(), "suspended appeal-eligible account must keep its app-token cookie")

	// 2. Subsequent /api/v1/appeals request with the preserved cookie succeeds.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/appeals", nil)
	req2.AddCookie(appCookie)
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusNoContent, w2.Code)

	// 3. Non-appeal-eligible rejected user DOES have app-token cookie cleared.
	require.NoError(t, conn.Model(&user).UpdateColumn("status", models.UserStatusRejected).Error)
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/verify_credentials", nil)
	req3.AddCookie(appCookie)
	r.ServeHTTP(w3, req3)
	require.Equal(t, http.StatusUnauthorized, w3.Code)
	require.NotEmpty(t, w3.Result().Cookies(), "rejected account must clear app-token cookie")
}
