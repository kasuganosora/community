package homeserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

type unifiedEnv struct {
	r *gin.Engine
}

func setupUnifiedEnv(t *testing.T) *unifiedEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	setupMinimalTestDB(t)
	conn := db.GetDB()
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.AuthToken{}, &models.Application{}, &models.PersonalAccessToken{}, &models.UserSecretData{}, &models.UserTwoFactor{}))
	require.NoError(t, conn.Create(&models.User{Id: 7, UUID: "u7", UserName: "bot", EmailHash: "h7", Status: models.UserStatusNormal}).Error)
	require.NoError(t, conn.Create(&models.Account{UserId: 7, Username: "bot"}).Error)
	old := config.C.EncryptedKey
	config.C.EncryptedKey = "unified-token-test-key-0123456789"
	t.Cleanup(func() { config.C.EncryptedKey = old })
	hs := minimalHS("test.local")
	hs.Auth = NewAuthService(hs)
	hs.Account = NewAccountService(hs)
	restore := resetGlobalHS(hs)
	t.Cleanup(restore)

	r := gin.New()
	api := r.Group("/_matrix/client/v3")
	api.Use(matrixAuthMiddleware())
	api.GET("/account/whoami", handleWhoami)
	api.PUT("/rooms/:roomId/send/:type/:txn", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"event_id": "$x"}) })
	api.POST("/logout", handleLogout)
	return &unifiedEnv{r: r}
}

func mintAPIToken(t *testing.T, names []string, expiresIn int) (string, *models.AuthToken) {
	t.Helper()
	scopes, _, ok := models.ParsePersonalTokenScopes(names)
	require.True(t, ok)
	row, raw := models.NewAuthToken(7, models.AuthTokenTypeOauth, 0, 0, &scopes, expiresIn, config.C.EncryptedKey)
	require.NotEmpty(t, raw)
	require.NoError(t, db.GetDB().Create(row).Error)
	return raw, row
}

func (e *unifiedEnv) do(method, path, token string, query bool) *httptest.ResponseRecorder {
	if query {
		path += "?access_token=" + url.QueryEscape(token)
	}
	req := httptest.NewRequest(method, path, nil)
	if !query {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func errcode(t *testing.T, w *httptest.ResponseRecorder) string {
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	s, _ := body["errcode"].(string)
	return s
}

func TestUnifiedTokenWithoutMatrixScopeRejected(t *testing.T) {
	e := setupUnifiedEnv(t)
	raw, _ := mintAPIToken(t, []string{"read", "write", "follow", "push"}, 0)
	for _, q := range []bool{false, true} {
		w := e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", raw, q)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.Equal(t, "M_UNKNOWN_TOKEN", errcode(t, w))
	}
	var n int64
	db.GetDB().Model(&MatrixUserSession{}).Count(&n)
	require.Zero(t, n, "no device for a token without Matrix scope")
}

func TestUnifiedTokenWhoamiStableDevice(t *testing.T) {
	e := setupUnifiedEnv(t)
	raw, row := mintAPIToken(t, []string{"read:matrix", "write:matrix"}, 0)
	var first string
	for i, q := range []bool{false, true} {
		w := e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", raw, q)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var resp WhoamiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, "@bot:test.local", resp.UserID)
		require.Equal(t, unifiedDeviceID(row.Token), resp.DeviceID)
		if i == 0 {
			first = resp.DeviceID
		}
		require.Equal(t, first, resp.DeviceID)
	}
	var sessions []MatrixUserSession
	require.NoError(t, db.GetDB().Find(&sessions).Error)
	require.Len(t, sessions, 1)
	require.NotEqual(t, raw, sessions[0].AccessToken, "raw API token never stored in Matrix tables")
	w := e.do(http.MethodPut, "/_matrix/client/v3/rooms/!r:test.local/send/m.room.message/1", raw, false)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestUnifiedTokenReadOnlyBlocksSend(t *testing.T) {
	e := setupUnifiedEnv(t)
	raw, _ := mintAPIToken(t, []string{"read:matrix"}, 0)
	require.Equal(t, http.StatusOK, e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", raw, false).Code)
	for _, q := range []bool{false, true} {
		w := e.do(http.MethodPut, "/_matrix/client/v3/rooms/!r:test.local/send/m.room.message/1", raw, q)
		require.Equal(t, http.StatusForbidden, w.Code)
		require.Equal(t, "M_FORBIDDEN", errcode(t, w))
	}
	raw2, _ := mintAPIToken(t, []string{"write:matrix"}, 0)
	require.Equal(t, http.StatusForbidden, e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", raw2, false).Code)
}

func TestUnifiedTokenRevokedAndExpiredRejected(t *testing.T) {
	e := setupUnifiedEnv(t)
	raw, row := mintAPIToken(t, []string{"read:matrix", "write:matrix"}, 0)
	require.Equal(t, http.StatusOK, e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", raw, false).Code)
	_, err := models.RevokeTokenByTokenStr(db.GetDB(), raw)
	require.NoError(t, err)
	w := e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", raw, false)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, "M_UNKNOWN_TOKEN", errcode(t, w))
	var n int64
	db.GetDB().Model(&MatrixUserSession{}).Where("device_id = ?", unifiedDeviceID(row.Token)).Count(&n)
	require.Zero(t, n, "bound device removed after revoke")

	raw2, row2 := mintAPIToken(t, []string{"read:matrix"}, 3600)
	require.NoError(t, db.GetDB().Model(&models.AuthToken{}).Where("token = ?", row2.Token).Update("expired_at", time.Now().Add(-time.Minute)).Error)
	require.Equal(t, http.StatusUnauthorized, e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", raw2, false).Code)

	// Suspended owner.
	raw3, _ := mintAPIToken(t, []string{"read:matrix"}, 0)
	require.NoError(t, db.GetDB().Model(&models.User{}).Where("id = 7").Update("is_ban", true).Error)
	require.Equal(t, http.StatusUnauthorized, e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", raw3, false).Code)
}

func TestUnifiedTokenEditedScopeRemovalTakesEffect(t *testing.T) {
	e := setupUnifiedEnv(t)
	raw, row := mintAPIToken(t, []string{"read:matrix"}, 0)
	require.NoError(t, db.GetDB().Model(&models.AuthToken{}).Where("token = ?", row.Token).Update("external_client", models.PersonalTokenClient).Error)
	bits := models.ScopeBits{ReadScope: models.AppScopeRead}
	require.NoError(t, db.GetDB().Create(&models.PersonalAccessToken{ID: "p1", UserID: 7, Name: "bot", TokenHash: row.Token, Scopes: []string{"read"}, ScopeBits: &bits, ScopeSource: models.ScopeSourceEdit}).Error)
	require.Equal(t, http.StatusUnauthorized, e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", raw, false).Code)
}

func TestUnifiedTokenLogoutKeepsAPIToken(t *testing.T) {
	e := setupUnifiedEnv(t)
	raw, row := mintAPIToken(t, []string{"read:matrix", "write:matrix"}, 0)
	require.Equal(t, http.StatusOK, e.do(http.MethodPost, "/_matrix/client/v3/logout", raw, false).Code)
	var tok models.AuthToken
	require.NoError(t, db.GetDB().Where("token = ?", row.Token).First(&tok).Error)
	require.Nil(t, tok.Revoke, "Matrix logout must not revoke the API token")
	var n int64
	db.GetDB().Model(&MatrixUserSession{}).Count(&n)
	require.Zero(t, n, "logout removes the bound device session")
	require.Equal(t, http.StatusOK, e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", raw, false).Code)
}

func TestNativeMatrixTokenUnchanged(t *testing.T) {
	e := setupUnifiedEnv(t)
	resp, merr := GetHomeServer().Auth.createSession(models.Account{Id: 1, UserId: 7, Username: "bot"}, nil, nil, false)
	require.Nil(t, merr)
	w := e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", resp.AccessToken, false)
	require.Equal(t, http.StatusOK, w.Code)
	var who WhoamiResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &who))
	require.Equal(t, resp.DeviceID, who.DeviceID)
	require.Equal(t, http.StatusOK, e.do(http.MethodPut, "/_matrix/client/v3/rooms/!r:test.local/send/m.room.message/1", resp.AccessToken, false).Code)
	require.Equal(t, http.StatusUnauthorized, e.do(http.MethodGet, "/_matrix/client/v3/account/whoami", "garbage-token-value-xxxxxxxx", false).Code)
}
