package oauth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// revokeTestServer wires /oauth/revoke and an AuthMiddleware-protected
// verify endpoint like router.go does.
func revokeTestServer() *gin.Engine {
	r := gin.New()
	r.POST("/oauth/revoke", Revoke)
	r.GET("/api/v1/accounts/verify_credentials", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware(), func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})

	return r
}

func issueToken(t *testing.T, env *oauthTestEnv, kind uint8) (string, *models.AuthToken) {
	t.Helper()

	// Keep the fixture within column limits: PostgreSQL enforces
	// varchar(36) on users.uuid, SQLite does not.
	id := uuid.NewString()
	user := models.User{UUID: id, UserName: fmt.Sprintf("revoker%d", kind), EmailHash: "revoke-" + id}
	require.NoError(t, env.gdb.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error)
	account := models.Account{UserId: user.Id, Username: user.UserName}
	require.NoError(t, env.gdb.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)

	row, raw := models.NewAuthToken(user.Id, kind, 0, 0, nil, 0, config.C.EncryptedKey)
	require.NotEmpty(t, raw)
	require.NoError(t, env.gdb.Create(row).Error)

	return raw, row
}

func verify(r *gin.Engine, token string) int {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/verify_credentials", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	return w.Code
}

func TestRevokeInvalidatesTokenImmediately(t *testing.T) {
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })

	cases := []struct {
		name  string
		redis bool
		send  func(token string) *http.Request
	}{
		{"form token, no cache", false, formRevoke},
		{"form token, redis cache", true, formRevoke},
		{"json token, redis cache", true, func(tok string) *http.Request {
			req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(`{"token":"`+tok+`"}`))
			req.Header.Set("Content-Type", "application/json")

			return req
		}},
		{"bearer only, redis cache", true, func(tok string) *http.Request {
			req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", nil)
			req.Header.Set("Authorization", "Bearer "+tok)

			return req
		}},
		{"client creds mismatch still revokes own token", true, func(tok string) *http.Request {
			req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(url.Values{"token": {tok}, "client_id": {"other"}, "client_secret": {"x"}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			return req
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newOAuthTestEnv(t)
			require.NoError(t, env.gdb.AutoMigrate(&models.User{}, &models.Account{}))

			var client *redis.Client
			if tc.redis {
				mr := miniredis.RunT(t)
				client = redis.NewClient(&redis.Options{Addr: mr.Addr()})
				t.Cleanup(func() { _ = client.Close() })
			}
			t.Cleanup(middleware.SetTokenCacheRedisForTest(client))

			for _, kind := range []uint8{models.AuthTokenTypeWeb, models.AuthTokenTypeOauth} {
				token, row := issueToken(t, env, kind)
				r := revokeTestServer()

				// Warm the cache with a "valid" entry.
				require.Equal(t, 200, verify(r, token))
				require.Equal(t, 200, verify(r, token))

				w := httptest.NewRecorder()
				r.ServeHTTP(w, tc.send(token))
				require.Equal(t, 200, w.Code)
				require.JSONEq(t, `{}`, w.Body.String())

				require.Equal(t, 401, verify(r, token), "revoked token must be rejected immediately")

				var stored models.AuthToken
				require.NoError(t, env.gdb.Where("token = ?", row.Token).First(&stored).Error)
				require.NotNil(t, stored.Revoke)
			}
		})
	}
}

func formRevoke(tok string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(url.Values{"token": {tok}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return req
}

func TestRevokeUnknownTokenReturnsEmptyOK(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(middleware.SetTokenCacheRedisForTest(nil))
	r := revokeTestServer()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, formRevoke("does-not-exist-token-value"))
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{}`, w.Body.String())

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/oauth/revoke", nil))
	require.Equal(t, 400, w.Code)
}

func TestRevokeCacheRaceDoesNotResurrectToken(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	t.Cleanup(middleware.SetTokenCacheRedisForTest(client))

	middleware.InvalidateTokenCache("racy-token")
	// A slow request that read the row before the revoke must not overwrite
	// the revoked marker with "valid".
	require.NoError(t, client.SetNX(t.Context(), "token_cache:x", "valid", 0).Err())
	keys := mr.Keys()
	for _, k := range keys {
		if k != "token_cache:x" {
			v, _ := mr.Get(k)
			require.Equal(t, "revoked", v)
		}
	}
}
