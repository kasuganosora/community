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
	"github.com/kasuganosora/community/controllers/api/account"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// realFlowServer wires /oauth/token, /oauth/revoke, verify_credentials and
// logout the way router.go does.
func realFlowServer() *gin.Engine {
	r := gin.New()
	r.POST("/oauth/token", Token)
	r.POST("/oauth/revoke", Revoke)
	auth := r.Group("/api/v1", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())
	auth.GET("/accounts/verify_credentials", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	auth.GET("/accounts/logout", account.Logout)
	auth.POST("/accounts/logout", account.Logout)

	return r
}

// issueViaTokenEndpoint runs the real authorization_code exchange and
// returns an access token containing a '+' (standard base64), retrying a
// few times since the ciphertext is random.
func issueViaTokenEndpoint(t *testing.T, r *gin.Engine, app *OAuthApplication, accountID uint64, wantPlus bool) string {
	t.Helper()

	for range 64 {
		code := generateAuthorizationCode(app.Id, accountID, "https://example.com/cb", "read write", "", "", "")
		require.NotEmpty(t, code)

		form := url.Values{
			"grant_type": {"authorization_code"}, "client_id": {app.ClientId}, "client_secret": {app.ClientSecret},
			"code": {code}, "redirect_uri": {"https://example.com/cb"},
		}
		req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())

		var resp TokenResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		if !wantPlus || strings.Contains(resp.AccessToken, "+") {
			return resp.AccessToken
		}
	}

	t.Fatal("could not obtain a token containing '+'")

	return ""
}

func TestRevokeWithClientCredentialsViaRealTokenFlow(t *testing.T) {
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })

	cases := map[string]func(app *OAuthApplication, tok string) *http.Request{
		"form encoded with client creds": func(app *OAuthApplication, tok string) *http.Request {
			body := url.Values{"client_id": {app.ClientId}, "client_secret": {app.ClientSecret}, "token": {tok}}.Encode()
			req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			return req
		},
		// curl -d "client_id=..&client_secret=..&token=$TOKEN": '+' arrives as space.
		"raw unencoded form with client creds": func(app *OAuthApplication, tok string) *http.Request {
			body := "client_id=" + app.ClientId + "&client_secret=" + app.ClientSecret + "&token=" + tok
			req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			return req
		},
		"json with client creds": func(app *OAuthApplication, tok string) *http.Request {
			b, _ := json.Marshal(map[string]string{"client_id": app.ClientId, "client_secret": app.ClientSecret, "token": tok})
			req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(string(b)))
			req.Header.Set("Content-Type", "application/json")

			return req
		},
	}

	for name, send := range cases {
		t.Run(name, func(t *testing.T) {
			env := newOAuthTestEnv(t)
			mr := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			t.Cleanup(middleware.SetTokenCacheRedisForTest(client))

			app := createTestApp(t, env, "RevokeApp", "https://example.com/cb")
			acct := createAccountInContext(t, env).(*models.Account)
			r := realFlowServer()

			tok := issueViaTokenEndpoint(t, r, app, acct.Id, true)
			require.Equal(t, 200, verify(r, tok))

			w := httptest.NewRecorder()
			r.ServeHTTP(w, send(app, tok))
			require.Equal(t, 200, w.Code, w.Body.String())

			require.Equal(t, 401, verify(r, tok), "revoked token must stop working")
		})
	}
}

func TestLogoutAcceptsOAuthToken(t *testing.T) {
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			env := newOAuthTestEnv(t)
			mr := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			t.Cleanup(middleware.SetTokenCacheRedisForTest(client))

			app := createTestApp(t, env, "LogoutApp", "https://example.com/cb")
			acct := createAccountInContext(t, env).(*models.Account)
			r := realFlowServer()

			tokens := map[string]string{"oauth": issueViaTokenEndpoint(t, r, app, acct.Id, false)}
			webRow, webTok := models.NewAuthToken(acct.UserId, models.AuthTokenTypeWeb, 0, 0, nil, 0, config.C.EncryptedKey)
			require.NoError(t, env.gdb.Create(webRow).Error)
			tokens["web"] = webTok

			for kind, tok := range tokens {
				require.Equal(t, 200, verify(r, tok), kind)

				req := httptest.NewRequest(method, "/api/v1/accounts/logout", nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, 200, w.Code, "%s: %s", kind, w.Body.String())

				require.Equal(t, 401, verify(r, tok), "%s token must be revoked by logout", kind)
			}
		})
	}
}
