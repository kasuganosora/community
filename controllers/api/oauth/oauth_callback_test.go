package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPKCE_S256RequiredForPublicClient(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "Public", "https://example.com/cb")
	account := createAccountInContext(t, env).(*models.Account)

	verifier := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~abcd"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	code := generateAuthorizationCode(app.Id, account.Id, "https://example.com/cb", "read", challenge, "S256", "")
	require.NotEmpty(t, code)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", app.ClientId)
	form.Set("code", code)
	form.Set("redirect_uri", "https://example.com/cb")
	form.Set("code_verifier", verifier)

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken)
	assert.Equal(t, "read", resp.Scope)
}

func TestAuthorizeRedirectIncludesIssuer(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	account := createAccountInContext(t, env).(*models.Account)
	require.NoError(t, env.gdb.Create(&models.ApplicationUserAuth{ApplicationId: app.Id, UserId: int64(account.UserId)}).Error)

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", app.ClientId)
	q.Set("redirect_uri", "https://example.com/cb")
	q.Set("state", "xyz")
	c, w := newGETContext(t, q)
	c.Set("authenticated", true)
	c.Set("account", account)
	AuthorizePage(c)

	loc := w.Header().Get("Location")
	parsed, err := url.Parse(loc)
	require.NoError(t, err)
	assert.NotEmpty(t, parsed.Query().Get("code"))
	assert.Equal(t, "xyz", parsed.Query().Get("state"))
	assert.NotEmpty(t, parsed.Query().Get("iss"))
}

func TestMiAuthCallbackUsesSessionQuery(t *testing.T) {
	env := newOAuthTestEnv(t)
	account := createAccountInContext(t, env).(*models.Account)

	c, w := newFormContext(t, http.MethodPost, url.Values{
		"name":       {"Milktea"},
		"callback":   {"https://app.example/cb"},
		"permission": {"write:notes,read:account"},
	})
	c.Params = gin.Params{{Key: "session", Value: "session-id-1"}}
	c.Set("authenticated", true)
	c.Set("account", account)
	MiAuthDecision(c)

	loc := w.Header().Get("Location")
	parsed, err := url.Parse(loc)
	require.NoError(t, err)
	assert.Equal(t, "session-id-1", parsed.Query().Get("session"))
	assert.Empty(t, parsed.Query().Get("code"))
	assert.Empty(t, parsed.Query().Get("token"))

	check, cw := newJSONContext(t, http.MethodPost, map[string]any{})
	check.Params = gin.Params{{Key: "session", Value: "session-id-1"}}
	MiAuthCheck(check)
	require.Equal(t, http.StatusOK, cw.Code, cw.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(cw.Body.Bytes(), &body))
	assert.Equal(t, true, body["ok"])
	assert.NotEmpty(t, body["token"])
}

func TestLegacyMisskeyCallbackUsesTokenQuery(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "Legacy", "https://app.example/done")
	account := createAccountInContext(t, env).(*models.Account)
	row := &models.MisskeyAuthSession{
		Session:       "legacy-session-1",
		Kind:          "legacy",
		AppName:       app.Name,
		CallbackURL:   "https://app.example/done",
		Permission:    "read",
		ApplicationId: app.Id,
	}
	require.NoError(t, env.gdb.Create(row).Error)

	c, w := newFormContext(t, http.MethodPost, url.Values{})
	c.Params = gin.Params{{Key: "token", Value: row.Session}}
	c.Set("authenticated", true)
	c.Set("account", account)
	LegacyAuthDecision(c)

	loc := w.Header().Get("Location")
	parsed, err := url.Parse(loc)
	require.NoError(t, err)
	assert.Equal(t, row.Session, parsed.Query().Get("token"))
	assert.Empty(t, parsed.Query().Get("code"))
	assert.Empty(t, parsed.Query().Get("session"))
}
