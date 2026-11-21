package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGranularOAuthGrantRoundTrip(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = client.Close() })
	t.Cleanup(middleware.SetTokenCacheRedisForTest(client))
	account := createAccountInContext(t, env).(*models.Account)
	r := realFlowServer()
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	for path, bits := range map[string]uint64{"/statuses": models.AppScopeReadStatuses, "/follows": models.AppScopeReadFollows} {
		r.GET(path, middleware.AuthMiddleware(), middleware.CheckAuthMiddleware(), middleware.CheckScopeMiddleware(middleware.Scope{Type: models.AppScopeTypeRead, Value: bits}), ok)
	}
	auth := r.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	auth.GET("/legacy-unscoped-read", ok)
	auth.GET("/api/v1/push/subscription", ok)
	auth.GET("/api/v1/media/:id", ok)
	auth.POST("/api/v1/media", ok)
	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	get := func(path, access string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+access)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	const callback = "https://example.com/callback"
	const registered = "read:statuses profile read:follows"
	form := url.Values{"client_name": {"wire test"}, "redirect_uris": {callback}, "scopes": {registered + " read:statuses"}}
	w := post("/api/v1/apps", form)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var application CreateAppResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &application))
	var app OAuthApplication
	require.NoError(t, env.gdb.First(&app, application.Id).Error)
	_, allowed := normalizeScope(&app, "read")
	require.False(t, allowed)
	_, allowed = normalizeScope(&app, "read:accounts")
	require.False(t, allowed)

	// An earlier release stored "read" for a user who approved read:statuses.
	// Fresh consent replaces that ambiguous value instead of merging it.
	require.NoError(t, env.gdb.Create(&models.ApplicationUserAuth{ApplicationId: app.Id, UserId: int64(account.UserId), Scopes: "read"}).Error)
	require.False(t, priorGrantCovers(app.Id, account.UserId, "read:follows"))
	require.NoError(t, rememberGrant(app.Id, account.UserId, "read:statuses"))
	require.True(t, priorGrantCovers(app.Id, account.UserId, "read:statuses"))
	require.False(t, priorGrantCovers(app.Id, account.UserId, "read:follows"))

	code := generateAuthorizationCode(app.Id, account.Id, callback, "read:statuses", "", "", "")
	require.NotEmpty(t, code)
	form = url.Values{"grant_type": {"authorization_code"}, "client_id": {app.ClientId}, "client_secret": {app.ClientSecret}, "code": {code}, "redirect_uri": {callback}}
	w = post("/oauth/token", form)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var token TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &token))
	require.Equal(t, "read:statuses", token.Scope)
	require.Equal(t, http.StatusOK, get("/statuses", token.AccessToken).Code)
	require.Equal(t, http.StatusForbidden, get("/follows", token.AccessToken).Code)

	w = get("/api/v1/apps/verify_credentials", token.AccessToken)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var verified ApplicationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &verified))
	require.Equal(t, strings.Fields(registered), verified.Scopes)

	form = url.Values{"grant_type": {"refresh_token"}, "client_id": {app.ClientId}, "client_secret": {app.ClientSecret}, "refresh_token": {token.RefreshToken}, "scope": {"read:follows"}}
	require.Equal(t, http.StatusBadRequest, post("/oauth/token", form).Code)
	form.Del("scope")
	w = post("/oauth/token", form)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var rotated TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rotated))
	require.Equal(t, "read:statuses", rotated.Scope)
	require.Equal(t, http.StatusForbidden, get("/follows", rotated.AccessToken).Code)
	require.Equal(t, http.StatusOK, get("/statuses", rotated.AccessToken).Code)
	require.Equal(t, http.StatusUnauthorized, get("/statuses", token.AccessToken).Code)

	// Pre-existing "follow" tokens retain precisely the original bits on rotation.
	legacy, raw := models.NewAuthToken(account.UserId, models.AuthTokenTypeOauth, app.Id, 0, &models.ApplicationScope{OtherScope: models.AppScopeFollow}, 0, config.C.EncryptedKey)
	require.NoError(t, env.gdb.Create(legacy).Error)
	form.Set("refresh_token", raw)
	w = post("/oauth/token", form)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rotated))
	data, err := models.GetAuthTokenData(rotated.AccessToken, config.C.EncryptedKey)
	require.NoError(t, err)
	require.Equal(t, &models.ApplicationScope{OtherScope: models.AppScopeFollow}, data.Scopes)

	for _, name := range []string{"profile", "profile push", "profile write:media"} {
		scopes, valid := models.ParseOAuthScopes(name)
		require.True(t, valid)
		row, raw := models.NewAuthToken(account.UserId, models.AuthTokenTypeOauth, app.Id, 0, &scopes, 0, config.C.EncryptedKey)
		require.NoError(t, env.gdb.Create(row).Error)
		require.Equal(t, http.StatusForbidden, get("/legacy-unscoped-read", raw).Code, name)
		require.Equal(t, http.StatusOK, get("/api/v1/accounts/verify_credentials", raw).Code, name)
		if name == "profile push" {
			require.Equal(t, http.StatusOK, get("/api/v1/push/subscription", raw).Code)
		}
		if name == "profile write:media" {
			require.Equal(t, http.StatusOK, get("/api/v1/media/2", raw).Code)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/media", nil)
			req.Header.Set("Authorization", "Bearer "+raw)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
		}
	}
}

func TestLegacyFollowRegistrationCapsDefaultAndExplicitGrants(t *testing.T) {
	env := newOAuthTestEnv(t)
	account := createAccountInContext(t, env).(*models.Account)
	app := createTestApp(t, env, "Legacy follow", "https://example.com/callback")
	legacy := models.ApplicationScope{OtherScope: models.AppScopeFollow}
	app.Scopes = legacy
	require.NoError(t, env.gdb.Save(app).Error)
	_, ok := normalizeScope(app, "follow read:follows")
	require.False(t, ok)
	for _, raw := range []string{"", "follow"} {
		scope, allowed := normalizeScope(app, raw)
		require.True(t, allowed)
		require.Equal(t, "follow", scope)
		code := generateAuthorizationCode(app.Id, account.Id, app.RedirectUri, scope, "", "", "")
		c, w := newFormContext(t, http.MethodPost, url.Values{
			"grant_type": {"authorization_code"}, "client_id": {app.ClientId}, "client_secret": {app.ClientSecret}, "code": {code}, "redirect_uri": {app.RedirectUri},
		})
		Token(c)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var token TokenResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &token))
		data, err := models.GetAuthTokenData(token.AccessToken, config.C.EncryptedKey)
		require.NoError(t, err)
		require.Equal(t, &legacy, data.Scopes)
	}
}
