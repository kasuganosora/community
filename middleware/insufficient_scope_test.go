package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A valid token that lacks a scope must get 403 (never 401, which clients
// treat as logout), on both the first request and later cache hits.
func TestReadOnlyTokensGetForbiddenOnWrites(t *testing.T) {
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.User{}, &models.Account{}, &models.AuthToken{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)
	useTestRedis(t)
	old := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = old })
	conn := setup.GormDB()
	user := models.User{Id: 81, UUID: "scope-user", UserName: "scopeuser", EmailHash: "scope-email"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Account{Id: 82, UserId: user.Id, Username: user.UserName}).Error)

	issue := func(scopes *models.ApplicationScope, personal bool) string {
		row, raw := models.NewAuthToken(user.Id, models.AuthTokenTypeOauth, 0, 0, scopes, 0, config.C.EncryptedKey)
		require.NotEmpty(t, raw)
		if personal {
			row.ExternalClient = models.PersonalTokenClient
		}
		require.NoError(t, conn.Create(row).Error)
		return raw
	}
	read := &models.ApplicationScope{ReadScope: models.AppScopeRead}
	write := &models.ApplicationScope{ReadScope: models.AppScopeRead, WriteScope: models.AppScopeWrite}
	readPAT, readOAuth := issue(read, true), issue(read, false)
	writePAT, writeOAuth := issue(write, true), issue(write, false)

	r := gin.New()
	r.Use(AuthMiddleware(), CheckAuthMiddleware())
	ok := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	r.GET("/api/v1/timelines/home", CheckScopeMiddleware(Scope{Type: models.AppScopeTypeRead, Value: models.AppScopeReadStatuses}), ok)
	r.POST("/api/v1/statuses", CheckScopeMiddleware(Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteStatuses}), ok)
	r.POST("/api/v1/statuses/:id/favourite", CheckScopeMiddleware(Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteFavourites}), ok)
	r.PATCH("/api/v1/accounts/update_credentials", ok) // no granular guard
	r.DELETE("/api/v1/statuses/:id", ok)               // no granular guard
	r.POST("/api/v1/statuses/:id/translate", ok)

	do := func(method, path, token string) (int, map[string]any) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		r.ServeHTTP(w, req)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}
	writes := [][2]string{{"POST", "/api/v1/statuses"}, {"POST", "/api/v1/statuses/1/favourite"}, {"PATCH", "/api/v1/accounts/update_credentials"}, {"DELETE", "/api/v1/statuses/1"}}
	for name, token := range map[string]string{"personal": readPAT, "oauth": readOAuth} {
		for round := 0; round < 3; round++ { // first is a cache miss, the rest cache hits
			code, _ := do("GET", "/api/v1/timelines/home", token)
			require.Equal(t, 204, code, name)
			for _, wr := range writes {
				code, body := do(wr[0], wr[1], token)
				require.Equal(t, 403, code, "%s %s %s round %d", name, wr[0], wr[1], round)
				require.Equal(t, InsufficientScopeError, body["error"])
			}
		}
	}
	code, _ := do("POST", "/api/v1/statuses/1/translate", readOAuth)
	require.Equal(t, 204, code, "translate needs only read")
	for name, token := range map[string]string{"personal": writePAT, "oauth": writeOAuth} {
		for round := 0; round < 2; round++ {
			for _, wr := range writes {
				code, _ := do(wr[0], wr[1], token)
				require.Equal(t, 204, code, "%s %s %s", name, wr[0], wr[1])
			}
		}
	}
	// Granular tokens: only the matching Mastodon scope may write.
	granular := func(write, other uint64) string {
		return issue(&models.ApplicationScope{ReadScope: models.AppScopeRead, WriteScope: write, OtherScope: other}, true)
	}
	r.POST("/api/v1/accounts/:id/block", ok)
	r.POST("/api/v1/accounts/:id/follow", ok)
	for _, tc := range []struct {
		token  string
		method string
		path   string
		want   int
	}{
		{granular(models.AppScopeWriteAccounts, 0), "PATCH", "/api/v1/accounts/update_credentials", 204},
		{granular(models.AppScopeWriteAccounts, 0), "POST", "/api/v1/accounts/1/block", 403},
		{granular(models.AppScopeWriteAccounts, 0), "POST", "/api/v1/statuses", 403},
		{granular(models.AppScopeWriteFavourites, 0), "POST", "/api/v1/statuses/1/favourite", 204},
		{granular(models.AppScopeWriteStatuses, 0), "POST", "/api/v1/statuses", 204},
		{granular(models.AppScopeWriteStatuses, 0), "POST", "/api/v1/statuses/1/favourite", 403},
		{granular(models.AppScopeWriteBlocks, 0), "POST", "/api/v1/accounts/1/block", 204},
		{granular(0, models.AppScopeFollow), "POST", "/api/v1/accounts/1/follow", 204},
		{granular(0, models.AppScopePush), "POST", "/api/v1/accounts/1/follow", 403},
	} {
		code, _ := do(tc.method, tc.path, tc.token)
		require.Equal(t, tc.want, code, "%s %s", tc.method, tc.path)
	}
	for _, bad := range []string{"not-a-token", readPAT[:len(readPAT)-4] + "AAAA"} {
		code, _ := do("POST", "/api/v1/statuses", bad)
		require.Equal(t, 401, code)
	}
}
