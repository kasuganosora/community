package oauth

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// CreateAppRequest represents the request for creating an OAuth application.
type CreateAppRequest struct {
	ClientName   string `binding:"required,min=1,max=128" form:"client_name"   json:"client_name"`
	RedirectUris string `binding:"required"               form:"redirect_uris" json:"redirect_uris"`
	Scopes       string `form:"scopes"                    json:"scopes"`
	Website      string `form:"website"                   json:"website"`
}

// CreateAppResponse represents the response for creating an OAuth application.
type CreateAppResponse struct {
	Id           string `json:"id"`
	Name         string `json:"name"`
	Website      string `json:"website,omitempty"`
	RedirectUri  string `json:"redirect_uri"`
	ClientId     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	VapidKey     string `json:"vapid_key"`
}

// AuthorizeRequest represents the request for OAuth authorization.
type AuthorizeRequest struct {
	ResponseType string `binding:"required" form:"response_type"`
	ClientId     string `binding:"required" form:"client_id"`
	RedirectUri  string `binding:"required" form:"redirect_uri"`
	Scope        string `form:"scope"`
	State        string `form:"state"`
	ForceLogin   bool   `form:"force_login"`
	Lang         string `form:"lang"`
}

// TokenRequest represents the request for obtaining a token.
type TokenRequest struct {
	GrantType    string `binding:"required"  form:"grant_type"    json:"grant_type"`
	ClientId     string `binding:"required"  form:"client_id"     json:"client_id"`
	ClientSecret string `binding:"required"  form:"client_secret" json:"client_secret"`
	RedirectUri  string `form:"redirect_uri" json:"redirect_uri"`
	Code         string `form:"code"         json:"code"`
	Scope        string `form:"scope"        json:"scope"`
	// Password grant
	Username string `form:"username" json:"username"`
	Password string `form:"password" json:"password"`
	// Refresh token
	RefreshToken string `form:"refresh_token" json:"refresh_token"`
}

// TokenResponse represents the response for token endpoint.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	CreatedAt    int64  `json:"created_at"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

// RevokeRequest represents the request for revoking a token.
type RevokeRequest struct {
	ClientId     string `form:"client_id"     json:"client_id"`
	ClientSecret string `form:"client_secret" json:"client_secret"`
	Token        string `binding:"required"   form:"token"         json:"token"`
}

// OAuthApplication extends Application with OAuth-specific fields.
type OAuthApplication struct {
	models.Application

	ClientId     string `gorm:"type:varchar(64);uniqueIndex;not null"`
	ClientSecret string `gorm:"type:varchar(128);not null"`
}

func (OAuthApplication) TableName() string {
	return "applications"
}

// OAuthAuthorizationCode stores temporary authorization codes.
type OAuthAuthorizationCode struct {
	Id            uint64     `gorm:"primary_key;autoIncrement"`
	Code          string     `gorm:"type:varchar(128);uniqueIndex;not null"`
	ApplicationId uint64     `gorm:"not null;index"`
	AccountId     uint64     `gorm:"not null;index"`
	RedirectUri   string     `gorm:"type:varchar(255);not null"`
	Scopes        string     `gorm:"type:varchar(255);not null"`
	ExpiresAt     *time.Time `gorm:"not null"`
	CreatedAt     *time.Time `gorm:"autoCreateTime"`
	Used          bool       `gorm:"default:false"`
}

func (OAuthAuthorizationCode) TableName() string {
	return "oauth_authorization_codes"
}

// CreateApp handles POST /api/v1/apps.
func CreateApp(c *gin.Context) {
	var req CreateAppRequest
	// Mastodon clients send either JSON or form-encoded bodies.
	err := c.ShouldBind(&req)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	// Generate client_id and client_secret
	clientId := utils.GetRandomHex(32)
	clientSecret := utils.GetRandomHex(64)

	// Parse scopes
	scopes := parseScopes(req.Scopes)

	// Create application
	app := &OAuthApplication{
		Application: models.Application{
			Name:        req.ClientName,
			Website:     req.Website,
			RedirectUri: req.RedirectUris,
			Scopes:      scopes,
			IsEnabled:   true,
		},
		ClientId:     clientId,
		ClientSecret: clientSecret,
	}

	err = db.GetDB().Create(app).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "create application error"))

		return
	}

	c.JSON(200, CreateAppResponse{
		Id:           strconv.FormatUint(app.Id, 10),
		Name:         app.Name,
		Website:      app.Website,
		RedirectUri:  app.RedirectUri,
		ClientId:     app.ClientId,
		ClientSecret: app.ClientSecret,
		VapidKey:     "", // VAPID key for push notifications (optional)
	})
}

// AuthorizePage handles GET /oauth/authorize.
func AuthorizePage(c *gin.Context) {
	var req AuthorizeRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})

		return
	}

	// Validate response_type
	if req.ResponseType != "code" {
		c.AbortWithStatusJSON(400, gin.H{"error": "unsupported response_type"})

		return
	}

	// Find application by client_id
	app := &OAuthApplication{}
	if err := db.GetDB().Where("client_id = ?", req.ClientId).First(app).Error; err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid client_id"})

		return
	}

	// Validate redirect_uri
	if !isValidRedirectUri(app.RedirectUri, req.RedirectUri) {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid redirect_uri"})

		return
	}

	// Check if user is authenticated
	if !c.GetBool("authenticated") {
		// Redirect to login page
		// 对参数进行 URL 编码，防止参数注入
		loginUrl := fmt.Sprintf("/login?redirect_uri=%s&client_id=%s&scope=%s&state=%s&response_type=code",
			url.QueryEscape(req.RedirectUri), url.QueryEscape(req.ClientId), url.QueryEscape(req.Scope), url.QueryEscape(req.State))
		c.Redirect(http.StatusTemporaryRedirect, loginUrl)

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert // guaranteed by auth middleware //nolint:forcetypeassert

	// Check if application is already authorized
	var userAuth models.ApplicationUserAuth

	err := db.GetDB().Where("application_id = ? AND user_id = ?", app.Id, account.UserId).First(&userAuth).Error
	if err == nil && !req.ForceLogin {
		// Already authorized, generate code and redirect
		code := generateAuthorizationCode(app.Id, account.Id, req.RedirectUri, req.Scope)
		if code == "" {
			c.AbortWithStatusJSON(500, gin.H{"error": "failed to generate authorization code"})

			return
		}

		redirectUrl := buildRedirectUrl(req.RedirectUri, code, req.State)
		c.Redirect(http.StatusTemporaryRedirect, redirectUrl)

		return
	}

	// Show authorization page
	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	c.HTML(http.StatusOK, "oauth_authorize.html", gin.H{
		"app":          serializer.ToApplication(&app.Application),
		"account":      serializer.ToAccount(account),
		"domain":       domain,
		"scope":        req.Scope,
		"state":        req.State,
		"redirect_uri": req.RedirectUri,
		"client_id":    req.ClientId,
	})
}

// Authorize handles POST /oauth/authorize.
func Authorize(c *gin.Context) {
	var req struct {
		ClientId    string `binding:"required" form:"client_id"`
		RedirectUri string `binding:"required" form:"redirect_uri"`
		Scope       string `form:"scope"`
		State       string `form:"state"`
		Deny        string `form:"deny"`
	}

	err := c.ShouldBind(&req)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})

		return
	}

	// Check if user is authenticated
	if !c.GetBool("authenticated") {
		c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert // guaranteed by auth middleware //nolint:forcetypeassert

	// Find application
	app := &OAuthApplication{}
	err = db.GetDB().Where("client_id = ?", req.ClientId).First(app).Error
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid client_id"})

		return
	}

	// Validate redirect_uri matches registered application
	// Use isValidRedirectUri for consistency with the GET authorize page,
	// which correctly handles multiple registered redirect URIs (newline-separated).
	if !isValidRedirectUri(app.RedirectUri, req.RedirectUri) {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid redirect_uri"})

		return
	}

	// If user denied authorization
	if req.Deny != "" {
		redirectUrl := buildDenyRedirectUrl(req.RedirectUri, req.State)
		c.Redirect(http.StatusTemporaryRedirect, redirectUrl)

		return
	}

	// Create or update user authorization
	userAuth := &models.ApplicationUserAuth{
		ApplicationId: app.Id,
		UserId:        int64(account.UserId), //nolint:gosec // G115: reviewed and safe
	}
	err = db.GetDB().Where("application_id = ? AND user_id = ?", app.Id, account.UserId).FirstOrCreate(userAuth).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "create user auth error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Generate authorization code
	code := generateAuthorizationCode(app.Id, account.Id, req.RedirectUri, req.Scope)
	if code == "" {
		c.AbortWithStatusJSON(500, gin.H{"error": "failed to generate authorization code"})

		return
	}

	// Redirect back to application
	redirectUrl := buildRedirectUrl(req.RedirectUri, code, req.State)
	c.Redirect(http.StatusTemporaryRedirect, redirectUrl)
}

// Token handles POST /oauth/token.
func Token(c *gin.Context) {
	var req TokenRequest
	err := c.ShouldBind(&req)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})

		return
	}

	// Find application
	app := &OAuthApplication{}
	err = db.GetDB().Where("client_id = ? AND client_secret = ?", req.ClientId, req.ClientSecret).First(app).Error
	if err != nil {
		c.AbortWithStatusJSON(401, gin.H{"error": "invalid_client"})

		return
	}

	switch req.GrantType {
	case "authorization_code":
		handleAuthorizationCodeGrant(c, app, &req)
	case "password":
		handlePasswordGrant(c, app, &req)
	case "refresh_token":
		handleRefreshTokenGrant(c, app, &req)
	case "client_credentials":
		handleClientCredentialsGrant(c, app, &req)
	default:
		c.AbortWithStatusJSON(400, gin.H{"error": "unsupported_grant_type"})
	}
}

// handleAuthorizationCodeGrant handles the authorization code grant type.
func handleAuthorizationCodeGrant(c *gin.Context, app *OAuthApplication, req *TokenRequest) {
	// Use transaction with atomic conditional update to prevent race condition
	// when two requests try to exchange the same code concurrently.
	var (
		code    *OAuthAuthorizationCode
		account *models.Account
	)

	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		// Find authorization code
		code = &OAuthAuthorizationCode{}
		err := tx.Where("code = ? AND application_id = ?", req.Code, app.Id).First(code).Error
		if err != nil {
			return errors.New("invalid_grant")
		}

		// Check if code is expired
		if code.ExpiresAt != nil && code.ExpiresAt.Before(time.Now()) {
			return errors.New("invalid_grant")
		}

		// Validate redirect_uri
		if code.RedirectUri != req.RedirectUri {
			return errors.New("invalid_grant")
		}

		// Atomically mark code as used: only succeed if used is still false.
		// This prevents race condition when two concurrent requests try to
		// exchange the same code — only one UPDATE will affect a row.
		result := tx.Model(code).Where("used = ?", false).Update("used", true)
		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected == 0 {
			// Code was already used by a concurrent request
			return errors.New("invalid_grant")
		}

		// Get account
		account = &models.Account{}
		err = tx.Preload("User").Preload("Extra").First(account, code.AccountId).Error
		if err != nil {
			return errors.New("invalid_grant")
		}

		return nil
	})
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})

		return
	}

	// Create token
	scopes := parseScopes(code.Scopes)
	token, tokenStr := models.NewAuthToken(
		account.UserId,
		models.AuthTokenTypeOauth,
		app.Id,
		0,
		&scopes,
		0, // No expiration for now
		config.C.EncryptedKey,
	)

	// Save token to database
	if err := db.GetDB().Create(token).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create token error"))

		return
	}

	// Generate refresh token
	refreshToken, refreshTokenStr := models.NewAuthToken(
		account.UserId,
		models.AuthTokenTypeOauth,
		app.Id,
		0,
		&scopes,
		0,
		config.C.EncryptedKey,
	)
	if err := db.GetDB().Create(refreshToken).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create refresh token error"))
	}

	c.JSON(200, TokenResponse{
		AccessToken:  tokenStr,
		TokenType:    "Bearer", //nolint:goconst
		Scope:        code.Scopes,
		CreatedAt:    time.Now().Unix(),
		RefreshToken: refreshTokenStr,
	})
}

// handlePasswordGrant handles the password grant type.
func handlePasswordGrant(c *gin.Context, app *OAuthApplication, req *TokenRequest) {
	// Password grant requires username and password
	if req.Username == "" || req.Password == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid_grant"}) //nolint:goconst

		return
	}

	// Find user by username (try username or email hash)
	user := &models.User{}

	emailHash := models.GetEmailHash(req.Username)
	err := db.GetDB().Where("email_hash = ? OR username = ?", emailHash, req.Username).First(user).Error
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid_grant"})

		return
	}

	// Get user secret data for password verification
	userSecret := &models.UserSecretData{}
	if err := db.GetDB().Where("user_id = ?", user.Id).First(userSecret).Error; err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid_grant"})

		return
	}

	// Verify password
	if err := models.CompareHashAndPassword(userSecret.Password, req.Password, userSecret.Salt); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid_grant"})

		return
	}

	// Check user status (same checks as web login)
	if systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false) && !user.IsMailVerified {
		c.AbortWithStatusJSON(400, gin.H{"error": "email not verified"})

		return
	}

	if user.Status != models.UserStatusNormal {
		c.AbortWithStatusJSON(400, gin.H{"error": "user status abnormal"})

		return
	}

	// Create token
	scopes := parseScopes(req.Scope)
	token, tokenStr := models.NewAuthToken(
		user.Id,
		models.AuthTokenTypeOauth,
		app.Id,
		0,
		&scopes,
		0,
		config.C.EncryptedKey,
	)

	// Save token to database
	if err := db.GetDB().Create(token).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create token error"))

		return
	}

	// Generate refresh token
	refreshToken, refreshTokenStr := models.NewAuthToken(
		user.Id,
		models.AuthTokenTypeOauth,
		app.Id,
		0,
		&scopes,
		0,
		config.C.EncryptedKey,
	)
	if err := db.GetDB().Create(refreshToken).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create refresh token error"))
	}

	c.JSON(200, TokenResponse{
		AccessToken:  tokenStr,
		TokenType:    "Bearer",
		Scope:        req.Scope,
		CreatedAt:    time.Now().Unix(),
		RefreshToken: refreshTokenStr,
	})
}

// handleRefreshTokenGrant handles the refresh token grant type.
func handleRefreshTokenGrant(c *gin.Context, app *OAuthApplication, req *TokenRequest) {
	if req.RefreshToken == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid_grant"})

		return
	}

	// Find the refresh token
	oldToken, err := models.GetTokenByTokenStr(req.RefreshToken)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid_grant"})

		return
	}

	// Check if token is revoked
	if oldToken.Revoke != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid_grant"})

		return
	}

	// Check if token belongs to the same application
	if oldToken.ApplicationId != app.Id {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid_grant"})

		return
	}

	// Check if it's an OAuth token
	if oldToken.Type != models.AuthTokenTypeOauth {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid_grant"})

		return
	}

	// Get the user from the old token
	userId := oldToken.UserId

	// Get the user's primary account
	account := &models.Account{}
	if err := db.GetDB().Where("user_id = ?", userId).First(account).Error; err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid_grant"})

		return
	}

	// Revoke the old token
	now := time.Now()

	oldToken.Revoke = &now
	if err := db.GetDB().Save(oldToken).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "save revoked token error"))
	}

	// Parse scopes from request or use default
	scopes := parseScopes(req.Scope)
	if req.Scope == "" {
		// Default scopes
		scopes = models.ApplicationScope{}
		scopes.AddScope(models.AppScopeTypeRead, models.AppScopeRead)
		scopes.AddScope(models.AppScopeTypeWrite, models.AppScopeWrite)
	}

	// Create new token
	token, tokenStr := models.NewAuthToken(
		userId,
		models.AuthTokenTypeOauth,
		app.Id,
		0,
		&scopes,
		0,
		config.C.EncryptedKey,
	)

	// Save token to database
	if err := db.GetDB().Create(token).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create token error"))

		return
	}

	// Generate new refresh token
	refreshToken, refreshTokenStr := models.NewAuthToken(
		userId,
		models.AuthTokenTypeOauth,
		app.Id,
		0,
		&scopes,
		0,
		config.C.EncryptedKey,
	)

	// Save refresh token
	if err := db.GetDB().Create(refreshToken).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create refresh token error"))
	}

	// Build scope string
	scopeStr := req.Scope
	if scopeStr == "" {
		scopeStr = "read write"
	}

	c.JSON(200, TokenResponse{
		AccessToken:  tokenStr,
		TokenType:    "Bearer",
		Scope:        scopeStr,
		CreatedAt:    time.Now().Unix(),
		RefreshToken: refreshTokenStr,
		ExpiresIn:    0, // No expiration
	})
}

// handleClientCredentialsGrant handles the client credentials grant type.
func handleClientCredentialsGrant(c *gin.Context, app *OAuthApplication, req *TokenRequest) {
	// Client credentials grant is for application-level access
	// It creates a token associated with the application itself (not a specific user)

	// Create scopes - client_credentials typically has limited scope
	scopes := parseScopes(req.Scope)

	scopeStr := req.Scope
	if req.Scope == "" {
		// Default to read-only for client credentials
		scopes = models.ApplicationScope{}
		scopes.AddScope(models.AppScopeTypeRead, models.AppScopeRead)

		scopeStr = "read"
	}

	// Create a token for the application (no user account)
	token, tokenStr := models.NewAuthToken(
		0, // No user ID for client credentials
		models.AuthTokenTypeOauth,
		app.Id,
		0,
		&scopes,
		0,
		config.C.EncryptedKey,
	)

	// Save token to database
	err := db.GetDB().Create(token).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "create token error"))

		return
	}

	c.JSON(200, TokenResponse{
		AccessToken: tokenStr,
		TokenType:   "Bearer",
		Scope:       scopeStr,
		CreatedAt:   time.Now().Unix(),
	})
}

// Revoke handles POST /oauth/revoke.
func Revoke(c *gin.Context) {
	var req RevokeRequest
	if err := c.ShouldBind(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})

		return
	}

	// Find and revoke the token in database
	token, err := models.GetTokenByTokenStr(req.Token)
	if err != nil {
		// Token is invalid or already revoked, return success anyway
		c.JSON(200, gin.H{})

		return
	}

	// Set revoke time
	now := time.Now()

	token.Revoke = &now
	if err := db.GetDB().Save(token).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "save revoked token error"))
	}

	c.JSON(200, gin.H{})
}

// VerifyCredentials handles GET /api/v1/apps/verify_credentials.
func VerifyCredentials(c *gin.Context) {
	// Check if user is authenticated
	if !c.GetBool("authenticated") {
		c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})

		return
	}

	// Get application from context
	app, exists := c.Get("application")
	if !exists {
		c.AbortWithStatusJSON(404, gin.H{"error": "application not found"})

		return
	}

	application := app.(*models.Application) //nolint:forcetypeassert // type guaranteed by caller
	c.JSON(200, serializer.ToApplication(application))
}

// Helper functions

func parseScopes(scopesStr string) models.ApplicationScope {
	scopes := models.ApplicationScope{}
	if scopesStr == "" {
		// Default scopes
		return scopes
	}

	scopeList := strings.SplitSeq(scopesStr, " ")
	for scope := range scopeList {
		scope = strings.TrimSpace(scope)
		switch {
		case scope == "read" || strings.HasPrefix(scope, "read:"):
			scopes.AddScope(models.AppScopeTypeRead, models.AppScopeRead)
		case scope == "write" || strings.HasPrefix(scope, "write:"):
			scopes.AddScope(models.AppScopeTypeWrite, models.AppScopeWrite)
		case scope == "admin" || strings.HasPrefix(scope, "admin:"):
			scopes.AddScope(models.AppScopeTypeAdmin, models.AppScopeAdmin)
		case scope == "follow":
			scopes.AddScope(models.AppScopeTypeOther, models.AppScopeFollow)
		case scope == "push":
			scopes.AddScope(models.AppScopeTypeOther, models.AppScopePush)
		}
	}

	return scopes
}

func isValidRedirectUri(registeredUris, requestedUri string) bool {
	// Check for exact match
	uris := strings.SplitSeq(registeredUris, "\n")
	for uri := range uris {
		uri = strings.TrimSpace(uri)
		if uri == requestedUri {
			return true
		}
		// 支持 urn:ietf:wg:oauth:2.0:oob（原生应用）
		// 仅当注册了 OOB 且请求的 URI 也是 OOB 时才允许，否则会允许任意 redirect_uri
		if uri == "urn:ietf:wg:oauth:2.0:oob" && requestedUri == "urn:ietf:wg:oauth:2.0:oob" {
			return true
		}
	}

	return false
}

func generateAuthorizationCode(appId, accountId uint64, redirectUri, scopes string) string {
	code := utils.GetRandomHex(64)
	expiresAt := time.Now().Add(10 * time.Minute)

	authCode := &OAuthAuthorizationCode{
		Code:          code,
		ApplicationId: appId,
		AccountId:     accountId,
		RedirectUri:   redirectUri,
		Scopes:        scopes,
		ExpiresAt:     &expiresAt,
		Used:          false,
	}

	err := db.GetDB().Create(authCode).Error
	if err != nil {
		log.Error("Failed to create auth code: %v", err)

		return ""
	}

	return code
}

func buildRedirectUrl(baseUri, code, state string) string {
	separator := "?"
	if strings.Contains(baseUri, "?") {
		separator = "&"
	}

	// 对 code 和 state 进行 URL 编码，防止参数注入
	redirectUrl := fmt.Sprintf("%s%scode=%s", baseUri, separator, url.QueryEscape(code))
	if state != "" {
		redirectUrl += "&state=" + url.QueryEscape(state)
	}

	return redirectUrl
}

// buildDenyRedirectUrl builds the redirect URL for an authorization denial.
// It properly handles the query separator (to avoid malformed URLs when the
// redirect URI already has query parameters) and URL-encodes the state
// parameter to prevent parameter injection.
func buildDenyRedirectUrl(baseUri, state string) string {
	separator := "?"
	if strings.Contains(baseUri, "?") {
		separator = "&"
	}

	redirectUrl := baseUri + separator + "error=access_denied&error_description=" + url.QueryEscape("User denied access")
	if state != "" {
		redirectUrl += "&state=" + url.QueryEscape(state)
	}

	return redirectUrl
}
