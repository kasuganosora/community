package oauth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============== parseScopes Tests ==============

func TestParseScopes_Empty(t *testing.T) {
	scopes := parseScopes("")
	assert.Equal(t, uint64(0), scopes.ReadScope)
	assert.Equal(t, uint64(0), scopes.WriteScope)
	assert.Equal(t, uint64(0), scopes.AdminScope)
	assert.Equal(t, uint64(0), scopes.OtherScope)
}

func TestParseScopes_Read(t *testing.T) {
	scopes := parseScopes("read")
	assert.True(t, scopes.ReadScope > 0, "read scope should be set")
}

func TestParseScopes_Write(t *testing.T) {
	scopes := parseScopes("write")
	assert.True(t, scopes.WriteScope > 0, "write scope should be set")
}

func TestParseScopes_Admin(t *testing.T) {
	scopes := parseScopes("admin")
	assert.True(t, scopes.AdminScope > 0, "admin scope should be set")
}

func TestParseScopes_Follow(t *testing.T) {
	scopes := parseScopes("follow")
	assert.True(t, scopes.OtherScope > 0, "follow scope should be set")
}

func TestParseScopes_Push(t *testing.T) {
	scopes := parseScopes("push")
	assert.True(t, scopes.OtherScope > 0, "push scope should be set")
}

func TestParseScopes_Multiple(t *testing.T) {
	scopes := parseScopes("read write follow")
	assert.True(t, scopes.ReadScope > 0, "read scope should be set")
	assert.True(t, scopes.WriteScope > 0, "write scope should be set")
	assert.True(t, scopes.OtherScope > 0, "follow scope should be set")
}

func TestParseScopes_Prefixed(t *testing.T) {
	scopes := parseScopes("read:accounts write:statuses")
	assert.True(t, scopes.ReadScope > 0, "read:accounts scope should set read")
	assert.True(t, scopes.WriteScope > 0, "write:statuses scope should set write")
}

func TestParseScopes_Unknown(t *testing.T) {
	scopes := parseScopes("unknown_scope")
	assert.Equal(t, uint64(0), scopes.ReadScope)
	assert.Equal(t, uint64(0), scopes.WriteScope)
	assert.Equal(t, uint64(0), scopes.AdminScope)
	assert.Equal(t, uint64(0), scopes.OtherScope)
}

// ============== CreateApp Tests ==============

func TestCreateApp_Success(t *testing.T) {
	env := newOAuthTestEnv(t)

	body := CreateAppRequest{
		ClientName:   "TestApp",
		RedirectUris: "https://example.com/callback",
		Scopes:       "read write",
		Website:      "https://example.com",
	}

	c, w := newJSONContext(t, http.MethodPost, body)
	CreateApp(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp CreateAppResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "TestApp", resp.Name)
	assert.Equal(t, "https://example.com", resp.Website)
	assert.Equal(t, "https://example.com/callback", resp.RedirectUri)
	assert.NotEmpty(t, resp.ClientId, "client_id should not be empty")
	assert.NotEmpty(t, resp.ClientSecret, "client_secret should not be empty")
	assert.NotEmpty(t, resp.Id, "id should not be empty")

	// Verify the application was persisted to the database
	var dbApp OAuthApplication
	require.NoError(t, env.gdb.Where("client_id = ?", resp.ClientId).First(&dbApp).Error)
	assert.Equal(t, "TestApp", dbApp.Name)
}

func TestCreateApp_InvalidJSON(t *testing.T) {
	setupTestDB(t)

	// Send malformed JSON
	c, w := newJSONContext(t, http.MethodPost, nil)
	// Overwrite request with invalid JSON
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("{invalid json"))) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/json")

	CreateApp(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateApp_MissingRequiredFields(t *testing.T) {
	setupTestDB(t)

	body := map[string]string{
		// Missing client_name and redirect_uris
		"website": "https://example.com",
	}

	c, w := newJSONContext(t, http.MethodPost, body)
	CreateApp(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ============== Authorize Tests ==============

func TestAuthorize_Unauthenticated(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("client_id", app.ClientId)
	form.Set("redirect_uri", "https://example.com/cb")

	c, w := newFormContext(t, http.MethodPost, form)
	// Note: "authenticated" defaults to false when not set
	Authorize(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthorize_InvalidClientId(t *testing.T) {
	env := newOAuthTestEnv(t)
	createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("client_id", "nonexistent-client-id")
	form.Set("redirect_uri", "https://example.com/cb")

	c, w := newFormContext(t, http.MethodPost, form)
	c.Set("authenticated", true)
	c.Set("account", createAccountInContext(t, env))

	Authorize(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthorize_InvalidRedirectUri(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("client_id", app.ClientId)
	form.Set("redirect_uri", "https://evil.com/callback")

	c, w := newFormContext(t, http.MethodPost, form)
	c.Set("authenticated", true)
	c.Set("account", createAccountInContext(t, env))

	Authorize(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthorize_MissingRequiredFields(t *testing.T) {
	setupTestDB(t)

	form := url.Values{}
	// Missing client_id and redirect_uri

	c, w := newFormContext(t, http.MethodPost, form)
	c.Set("authenticated", true)

	Authorize(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthorize_Deny(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("client_id", app.ClientId)
	form.Set("redirect_uri", "https://example.com/cb")
	form.Set("deny", "1")
	form.Set("state", "mystate")

	c, w := newFormContext(t, http.MethodPost, form)
	c.Set("authenticated", true)
	c.Set("account", createAccountInContext(t, env))

	Authorize(c)

	// gin's test ResponseRecorder may report status 200 for redirects on POST
	// requests because http.Redirect skips writing the body (so gin's
	// WriteHeaderNow is never triggered). The authoritative signal that a
	// redirect occurred is the Location header, so we assert on that.
	loc := w.Header().Get("Location")
	assert.NotEmpty(t, loc, "expected a redirect Location header")
	assert.Contains(t, loc, "error=access_denied")
	assert.Contains(t, loc, "state=mystate")
}

func TestAuthorize_Success(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("client_id", app.ClientId)
	form.Set("redirect_uri", "https://example.com/cb")
	form.Set("scope", "read")
	form.Set("state", "mystate")

	c, w := newFormContext(t, http.MethodPost, form)
	c.Set("authenticated", true)
	c.Set("account", createAccountInContext(t, env))

	Authorize(c)

	// gin's test ResponseRecorder may report status 200 for redirects on POST
	// requests; assert on the Location header instead.
	loc := w.Header().Get("Location")
	assert.NotEmpty(t, loc, "expected a redirect Location header")
	assert.Contains(t, loc, "code=")
	assert.Contains(t, loc, "state=mystate")
}

// ============== AuthorizePage Tests ==============

func TestAuthorizePage_MissingRequiredFields(t *testing.T) {
	setupTestDB(t)

	c, w := newGETContext(t, nil)
	AuthorizePage(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthorizePage_UnsupportedResponseType(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	q := url.Values{}
	q.Set("response_type", "token")
	q.Set("client_id", app.ClientId)
	q.Set("redirect_uri", "https://example.com/cb")

	c, w := newGETContext(t, q)
	AuthorizePage(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "unsupported response_type")
}

func TestAuthorizePage_InvalidClientId(t *testing.T) {
	setupTestDB(t)

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "nonexistent")
	q.Set("redirect_uri", "https://example.com/cb")

	c, w := newGETContext(t, q)
	AuthorizePage(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid client_id")
}

func TestAuthorizePage_InvalidRedirectUri(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", app.ClientId)
	q.Set("redirect_uri", "https://evil.com/cb")

	c, w := newGETContext(t, q)
	AuthorizePage(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid redirect_uri")
}

func TestAuthorizePage_UnauthenticatedRedirectsToLogin(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", app.ClientId)
	q.Set("redirect_uri", "https://example.com/cb")
	q.Set("scope", "read")
	q.Set("state", "abc")

	c, w := newGETContext(t, q)
	// The helper builds the request at "/". Production routes hit /oauth/authorize,
	// and the login return URL must echo that path so the SPA can bounce back.
	c.Request.URL.Path = "/oauth/authorize"
	// authenticated not set -> false
	AuthorizePage(c)

	// Assert on the Location header which is the authoritative redirect signal.
	loc := w.Header().Get("Location")
	assert.NotEmpty(t, loc, "expected a redirect to the login page")
	assert.Contains(t, loc, "/login")
	assert.Contains(t, loc, "redirect=")
	unescaped, err := url.QueryUnescape(loc)
	require.NoError(t, err)
	assert.Contains(t, unescaped, "client_id="+app.ClientId)
	assert.Contains(t, unescaped, "/oauth/authorize")
}

// ============== Token Tests ==============

func TestToken_BindingError(t *testing.T) {
	setupTestDB(t)

	c, w := newFormContext(t, http.MethodPost, nil)
	Token(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestToken_InvalidClient(t *testing.T) {
	setupTestDB(t)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", "nonexistent")
	form.Set("client_secret", "wrongsecret")

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_client")
}

func TestToken_UnsupportedGrantType(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("grant_type", "invalid_grant")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "unsupported_grant_type")
}

func TestToken_ClientCredentialsGrant_Success(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken, "access_token should not be empty")
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.Equal(t, "read", resp.Scope, "default scope should be read")
}

func TestToken_ClientCredentialsGrant_WithScope(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	form.Set("scope", "read write")

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken)
	assert.Equal(t, "read write", resp.Scope)
}

// ============== Revoke Tests ==============

func TestRevoke_BindingError(t *testing.T) {
	setupTestDB(t)

	c, w := newFormContext(t, http.MethodPost, nil)
	Revoke(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRevoke_InvalidToken(t *testing.T) {
	setupTestDB(t)

	form := url.Values{}
	form.Set("token", "invalid-token-string")

	c, w := newFormContext(t, http.MethodPost, form)
	Revoke(c)

	// Invalid tokens should still return 200 (per OAuth spec)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRevoke_ValidToken(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	// Create a token using client credentials grant
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)
	require.Equal(t, http.StatusOK, w.Code)

	var tokenResp TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tokenResp))
	require.NotEmpty(t, tokenResp.AccessToken)

	// Now revoke it
	revokeForm := url.Values{}
	revokeForm.Set("token", tokenResp.AccessToken)

	c2, w2 := newFormContext(t, http.MethodPost, revokeForm)
	Revoke(c2)

	assert.Equal(t, http.StatusOK, w2.Code)

	// Verify the token is now revoked by trying to use it for refresh
	refreshForm := url.Values{}
	refreshForm.Set("grant_type", "refresh_token")
	refreshForm.Set("client_id", app.ClientId)
	refreshForm.Set("client_secret", app.ClientSecret)
	refreshForm.Set("refresh_token", tokenResp.AccessToken)

	c3, w3 := newFormContext(t, http.MethodPost, refreshForm)
	Token(c3)
	// Should return 400 because the token is revoked
	assert.Equal(t, http.StatusBadRequest, w3.Code)
}

// ============== VerifyCredentials Tests ==============

func TestVerifyCredentials_Unauthenticated(t *testing.T) {
	setupTestDB(t)

	c, w := newGETContext(t, nil)
	VerifyCredentials(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestVerifyCredentials_NoApplication(t *testing.T) {
	setupTestDB(t)

	c, w := newGETContext(t, nil)
	c.Set("authenticated", true)
	VerifyCredentials(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// Many Mastodon clients register apps with a form-encoded body.
func TestCreateApp_FormEncoded(t *testing.T) {
	newOAuthTestEnv(t)

	form := "client_name=FormApp&redirect_uris=urn%3Aietf%3Awg%3Aoauth%3A2.0%3Aoob&scopes=read+write"
	c, w := newJSONContext(t, http.MethodPost, nil)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form)) //nolint:noctx
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	CreateApp(c)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp CreateAppResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "FormApp", resp.Name)
	assert.NotEmpty(t, resp.ClientId)
}
