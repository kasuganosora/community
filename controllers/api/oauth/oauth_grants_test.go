package oauth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============== Authorization Code Grant Tests ==============

func TestToken_AuthorizationCodeGrant_Success(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	account := createAccountInContext(t, env).(*models.Account)

	// Create an authorization code
	code := createAuthCode(t, env, app.Id, account.Id, "https://example.com/cb", "read", false)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	form.Set("code", code.Code)
	form.Set("redirect_uri", "https://example.com/cb")

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken, "access_token should not be empty")
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.Equal(t, "read", resp.Scope)
	assert.NotEmpty(t, resp.RefreshToken, "refresh_token should not be empty")
}

func TestToken_AuthorizationCodeGrant_InvalidCode(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	form.Set("code", "nonexistent-code")
	form.Set("redirect_uri", "https://example.com/cb")

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_grant")
}

func TestToken_AuthorizationCodeGrant_ExpiredCode(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	account := createAccountInContext(t, env).(*models.Account)

	// Create an expired authorization code
	code := createAuthCode(t, env, app.Id, account.Id, "https://example.com/cb", "read", true)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	form.Set("code", code.Code)
	form.Set("redirect_uri", "https://example.com/cb")

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_grant")
}

func TestToken_AuthorizationCodeGrant_RedirectUriMismatch(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	account := createAccountInContext(t, env).(*models.Account)

	code := createAuthCode(t, env, app.Id, account.Id, "https://example.com/cb", "read", false)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	form.Set("code", code.Code)
	form.Set("redirect_uri", "https://different.com/cb") // mismatch

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_grant")
}

func TestToken_AuthorizationCodeGrant_CodeAlreadyUsed(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	account := createAccountInContext(t, env).(*models.Account)

	code := createAuthCode(t, env, app.Id, account.Id, "https://example.com/cb", "read", false)

	// First use of the code should succeed
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	form.Set("code", code.Code)
	form.Set("redirect_uri", "https://example.com/cb")

	c1, w1 := newFormContext(t, http.MethodPost, form)
	Token(c1)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Second use of the same code should fail (code already used)
	c2, w2 := newFormContext(t, http.MethodPost, form)
	Token(c2)
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assert.Contains(t, w2.Body.String(), "invalid_grant")
}

// ============== Refresh Token Grant Tests ==============

func TestToken_RefreshTokenGrant_MissingRefreshToken(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	// refresh_token not set

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_grant")
}

func TestToken_RefreshTokenGrant_InvalidRefreshToken(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	form.Set("refresh_token", "invalid-refresh-token")

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_grant")
}

func TestToken_RefreshTokenGrant_RevokedToken(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	// Create a valid OAuth token via client_credentials, then revoke it and
	// attempt to use it as a refresh token.
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

	// Revoke the token
	revokeForm := url.Values{}
	revokeForm.Set("token", tokenResp.AccessToken)
	c2, w2 := newFormContext(t, http.MethodPost, revokeForm)
	Revoke(c2)
	require.Equal(t, http.StatusOK, w2.Code)

	// Try to refresh with the revoked token
	refreshForm := url.Values{}
	refreshForm.Set("grant_type", "refresh_token")
	refreshForm.Set("client_id", app.ClientId)
	refreshForm.Set("client_secret", app.ClientSecret)
	refreshForm.Set("refresh_token", tokenResp.AccessToken)

	c3, w3 := newFormContext(t, http.MethodPost, refreshForm)
	Token(c3)
	assert.Equal(t, http.StatusBadRequest, w3.Code)
	assert.Contains(t, w3.Body.String(), "invalid_grant")
}

func TestToken_RefreshTokenGrant_Success(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	account := createAccountInContext(t, env).(*models.Account)

	// Create a valid OAuth refresh token directly. We cannot rely on the
	// authorization_code grant to produce a token with a persisted
	// ApplicationId (NewAuthToken omits it), so we build the token and set
	// ApplicationId manually before saving.
	tokenRow, tokenStr := models.NewAuthToken(
		account.UserId, models.AuthTokenTypeOauth, app.Id, 0, nil, 0, "")
	tokenRow.ApplicationId = app.Id
	require.NoError(t, env.gdb.Create(tokenRow).Error)

	refreshForm := url.Values{}
	refreshForm.Set("grant_type", "refresh_token")
	refreshForm.Set("client_id", app.ClientId)
	refreshForm.Set("client_secret", app.ClientSecret)
	refreshForm.Set("refresh_token", tokenStr)

	c2, w2 := newFormContext(t, http.MethodPost, refreshForm)
	Token(c2)
	assert.Equal(t, http.StatusOK, w2.Code)

	var refreshResp TokenResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &refreshResp))
	assert.NotEmpty(t, refreshResp.AccessToken)
	assert.Equal(t, "Bearer", refreshResp.TokenType)
	assert.NotEmpty(t, refreshResp.RefreshToken)
}

// TestToken_RefreshTokenGrant_AppMismatch verifies the app-ownership check
// rejects refresh tokens that belong to a different application.
func TestToken_RefreshTokenGrant_AppMismatch(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	account := createAccountInContext(t, env).(*models.Account)

	// Create a token that belongs to a *different* application id (999).
	tokenRow, tokenStr := models.NewAuthToken(
		account.UserId, models.AuthTokenTypeOauth, 999, 0, nil, 0, "")
	tokenRow.ApplicationId = 999
	require.NoError(t, env.gdb.Create(tokenRow).Error)

	refreshForm := url.Values{}
	refreshForm.Set("grant_type", "refresh_token")
	refreshForm.Set("client_id", app.ClientId)
	refreshForm.Set("client_secret", app.ClientSecret)
	refreshForm.Set("refresh_token", tokenStr)

	c2, w2 := newFormContext(t, http.MethodPost, refreshForm)
	Token(c2)
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assert.Contains(t, w2.Body.String(), "invalid_grant")
}

// ============== Password Grant Tests ==============

func TestToken_PasswordGrant_MissingCredentials(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	// username and password not set

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_grant")
}

func TestToken_PasswordGrant_UserNotFound(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	form.Set("username", "nonexistent")
	form.Set("password", "somepassword")

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_grant")
}

func TestToken_PasswordGrant_NoSecretData(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	user := env.fix.CreateUser(func(u *models.User) {
		u.UserName = "pwduser"
	})

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	form.Set("username", user.UserName)
	form.Set("password", "somepassword")

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	// No UserSecretData row exists, so password verification fails
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_grant")
}

func TestToken_PasswordGrant_WrongPassword(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	user := env.fix.CreateUser(func(u *models.User) {
		u.UserName = "pwduser2"
	})

	// Create a secret with a known password hash
	salt := "mysalt"
	hash, err := models.EncryptPassword("correctpassword", salt)
	require.NoError(t, err)

	secret := &models.UserSecretData{
		UserId:   user.Id,
		Salt:     salt,
		Password: hash,
	}
	require.NoError(t, env.gdb.Create(secret).Error)

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	form.Set("username", user.UserName)
	form.Set("password", "wrongpassword")

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_grant")
}

func TestToken_PasswordGrant_Success(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	user := env.fix.CreateUser(func(u *models.User) {
		u.UserName = "pwduser3"
	})

	// Create a secret with a known password hash
	salt := "mysalt"
	hash, err := models.EncryptPassword("correctpassword", salt)
	require.NoError(t, err)

	secret := &models.UserSecretData{
		UserId:   user.Id,
		Salt:     salt,
		Password: hash,
	}
	require.NoError(t, env.gdb.Create(secret).Error)

	// Create account for the user (needed by some downstream code)
	account := &models.Account{
		UserId: user.Id,
		Acct:   "pwduser3",
	}
	require.NoError(t, env.gdb.Create(account).Error)

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", app.ClientId)
	form.Set("client_secret", app.ClientSecret)
	form.Set("username", user.UserName)
	form.Set("password", "correctpassword")
	form.Set("scope", "read")

	c, w := newFormContext(t, http.MethodPost, form)
	Token(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp TokenResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken)
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.NotEmpty(t, resp.RefreshToken)
	assert.Equal(t, "read", resp.Scope)
}

// ============== generateAuthorizationCode Tests ==============

func TestGenerateAuthorizationCode_Success(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	code := generateAuthorizationCode(app.Id, 123, "https://example.com/cb", "read")
	assert.NotEmpty(t, code, "should generate a non-empty code")

	// Verify the code was persisted
	var authCode OAuthAuthorizationCode
	require.NoError(t, env.gdb.Where("code = ?", code).First(&authCode).Error)
	assert.Equal(t, app.Id, authCode.ApplicationId)
	assert.Equal(t, uint64(123), authCode.AccountId)
	assert.Equal(t, "https://example.com/cb", authCode.RedirectUri)
	assert.False(t, authCode.Used)
}

func TestGenerateAuthorizationCode_Expiry(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")

	code := generateAuthorizationCode(app.Id, 456, "https://example.com/cb", "read")
	require.NotEmpty(t, code)

	var authCode OAuthAuthorizationCode
	require.NoError(t, env.gdb.Where("code = ?", code).First(&authCode).Error)
	require.NotNil(t, authCode.ExpiresAt)
	// Expiry should be ~10 minutes in the future
	assert.True(t, authCode.ExpiresAt.After(time.Now()))
	assert.True(t, authCode.ExpiresAt.Before(time.Now().Add(11*time.Minute)))
}

// ============== AuthorizePage additional Tests ==============

func TestAuthorizePage_AlreadyAuthorizedRedirects(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	account := createAccountInContext(t, env).(*models.Account)

	// Pre-create the user authorization so the "already authorized" path is taken
	userAuth := &models.ApplicationUserAuth{
		ApplicationId: app.Id,
		UserId:        int64(account.UserId), //nolint:gosec // G115: reviewed and safe
	}
	require.NoError(t, env.gdb.Create(userAuth).Error)

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", app.ClientId)
	q.Set("redirect_uri", "https://example.com/cb")
	q.Set("scope", "read")
	q.Set("state", "xyz")

	c, w := newGETContext(t, q)
	c.Set("authenticated", true)
	c.Set("account", account)
	AuthorizePage(c)

	// Already authorized -> should redirect with a code
	loc := w.Header().Get("Location")
	assert.NotEmpty(t, loc, "expected a redirect with an authorization code")
	assert.Contains(t, loc, "code=")
	assert.Contains(t, loc, "state=xyz")
}

func TestAuthorizePage_ForceLoginSkipsAlreadyAuthorized(t *testing.T) {
	env := newOAuthTestEnv(t)
	app := createTestApp(t, env, "TestApp", "https://example.com/cb")
	account := createAccountInContext(t, env).(*models.Account)

	userAuth := &models.ApplicationUserAuth{
		ApplicationId: app.Id,
		UserId:        int64(account.UserId), //nolint:gosec // G115: reviewed and safe
	}
	require.NoError(t, env.gdb.Create(userAuth).Error)

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", app.ClientId)
	q.Set("redirect_uri", "https://example.com/cb")
	q.Set("scope", "read")
	q.Set("state", "xyz")
	q.Set("force_login", "true")

	c, w := newGETContext(t, q)
	c.Set("authenticated", true)
	c.Set("account", account)

	// With force_login=true, the handler proceeds past the "already authorized"
	// fast-path to render the authorization page (c.HTML). Without a template
	// engine configured this panics with a nil pointer dereference. The
	// behavior we want to verify is that force_login does NOT auto-redirect
	// with a code, so we recover from the expected HTML-render panic and
	// assert that no redirect-with-code Location header was set.
	func() {
		defer func() { _ = recover() }()
		AuthorizePage(c)
	}()

	loc := w.Header().Get("Location")
	assert.NotContains(t, loc, "code=", "force_login should not auto-redirect with a code")
}

// ============== VerifyCredentials Success ==============

func TestVerifyCredentials_Success(t *testing.T) {
	env := newOAuthTestEnv(t)

	// Use createTestApp which correctly sets up an OAuthApplication (with
	// client_id/client_secret), then extract the embedded Application.
	oauthApp := createTestApp(t, env, "VerifiedApp", "https://example.com/cb")
	app := &oauthApp.Application

	c, w := newGETContext(t, nil)
	c.Set("authenticated", true)
	c.Set("application", app)
	VerifyCredentials(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// Response should contain the application name
	assert.Contains(t, w.Body.String(), "VerifiedApp")
}
