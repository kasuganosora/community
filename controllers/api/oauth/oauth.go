package oauth

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
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
	Id           string   `json:"id"`
	Name         string   `json:"name"`
	Website      *string  `json:"website"`
	RedirectUri  string   `json:"redirect_uri"`
	RedirectUris []string `json:"redirect_uris"`
	Scopes       []string `json:"scopes"`
	ClientId     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	// ClientSecretExpiresAt is always 0 (never expires), as in Mastodon.
	ClientSecretExpiresAt int    `json:"client_secret_expires_at"`
	VapidKey              string `json:"vapid_key"`
}

// AuthorizeRequest represents the request for OAuth authorization.
type AuthorizeRequest struct {
	ResponseType        string `binding:"required" form:"response_type"`
	ClientId            string `binding:"required" form:"client_id"`
	RedirectUri         string `binding:"required" form:"redirect_uri"`
	Scope               string `form:"scope"`
	State               string `form:"state"`
	ForceLogin          bool   `form:"force_login"`
	Lang                string `form:"lang"`
	CodeChallenge       string `form:"code_challenge"`
	CodeChallengeMethod string `form:"code_challenge_method"`
}

// TokenRequest represents the request for obtaining a token.
type TokenRequest struct {
	GrantType    string `form:"grant_type"    json:"grant_type"`
	ClientId     string `form:"client_id"     json:"client_id"`
	ClientSecret string `form:"client_secret" json:"client_secret"`
	RedirectUri  string `form:"redirect_uri" json:"redirect_uri"`
	Code         string `form:"code"         json:"code"`
	Scope        string `form:"scope"        json:"scope"`
	// Password grant
	Username string `form:"username" json:"username"`
	Password string `form:"password" json:"password"`
	// Refresh token
	RefreshToken string `form:"refresh_token" json:"refresh_token"`
	// PKCE (RFC 7636). Required for Misskey OAuth / IndieAuth clients.
	CodeVerifier string `form:"code_verifier" json:"code_verifier"`
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
	Token        string `form:"token"         json:"token"`
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
	Id                  uint64     `gorm:"primary_key;autoIncrement"`
	Code                string     `gorm:"type:varchar(128);uniqueIndex;not null"`
	ApplicationId       uint64     `gorm:"not null;index"`
	AccountId           uint64     `gorm:"not null;index"`
	RedirectUri         string     `gorm:"type:varchar(2048);not null"`
	Scopes              string     `gorm:"type:varchar(1024);not null"`
	CodeChallenge       string     `gorm:"type:varchar(128)"`
	CodeChallengeMethod string     `gorm:"type:varchar(16)"`
	ExternalClientID    string     `gorm:"type:varchar(512);index"`
	AccessTokenHash     string     `gorm:"type:varchar(64)"`
	RefreshTokenHash    string     `gorm:"type:varchar(64)"`
	ExpiresAt           *time.Time `gorm:"not null"`
	CreatedAt           *time.Time `gorm:"autoCreateTime"`
	Used                bool       `gorm:"default:false"`
}

func (OAuthAuthorizationCode) TableName() string {
	return "oauth_authorization_codes"
}

// CreateApp handles POST /api/v1/apps.
func CreateApp(c *gin.Context) {
	req, err := bindCreateApp(c)
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
		Website:      websitePtr(app.Website),
		RedirectUri:  app.RedirectUri,
		RedirectUris: splitRedirects(app.RedirectUri),
		Scopes:       responseScopes(req.Scopes),
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

	app, info, errMsg := resolveAuthorizeClient(c, req.ClientId, req.RedirectUri)
	if errMsg != "" {
		c.AbortWithStatusJSON(400, gin.H{"error": errMsg})

		return
	}

	pkceRequired := app == nil
	if msg := validatePKCERequest(req.CodeChallenge, req.CodeChallengeMethod, pkceRequired); msg != "" {
		c.AbortWithStatusJSON(400, gin.H{"error": msg})

		return
	}

	scope, ok := normalizeScope(app, req.Scope)
	if !ok {
		c.Redirect(http.StatusFound, buildDenyRedirect(req.RedirectUri, "invalid_scope", "Requested scope exceeds the registered scopes", req.State))

		return
	}

	req.Scope = scope

	// Check if user is authenticated
	if !c.GetBool("authenticated") {
		c.Redirect(http.StatusFound, loginReturnURL(c))

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert // guaranteed by auth middleware

	if app != nil && !req.ForceLogin && priorGrantCovers(app.Id, account.UserId, req.Scope) {
		finishAuthorize(c, app, info, account, req)

		return
	}

	showConsent(c, consentFromOAuth(app, info, account, req))
}

// Authorize handles POST /oauth/authorize.
func Authorize(c *gin.Context) {
	var req struct {
		ClientId            string `binding:"required" form:"client_id"`
		RedirectUri         string `binding:"required" form:"redirect_uri"`
		Scope               string `form:"scope"`
		State               string `form:"state"`
		Deny                string `form:"deny"`
		CodeChallenge       string `form:"code_challenge"`
		CodeChallengeMethod string `form:"code_challenge_method"`
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
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert // guaranteed by auth middleware

	app, info, errMsg := resolveAuthorizeClient(c, req.ClientId, req.RedirectUri)
	if errMsg != "" {
		c.AbortWithStatusJSON(400, gin.H{"error": errMsg})

		return
	}

	if msg := validatePKCERequest(req.CodeChallenge, req.CodeChallengeMethod, app == nil); msg != "" {
		c.AbortWithStatusJSON(400, gin.H{"error": msg})

		return
	}

	scope, ok := normalizeScope(app, req.Scope)
	if !ok {
		c.Redirect(http.StatusFound, buildDenyRedirect(req.RedirectUri, "invalid_scope", "Requested scope exceeds the registered scopes", req.State))

		return
	}

	// If user denied authorization
	if req.Deny != "" {
		c.Redirect(http.StatusFound, buildDenyRedirectUrl(req.RedirectUri, req.State))

		return
	}

	if app != nil {
		if err = rememberGrant(app.Id, account.UserId, scope); err != nil {
			_ = c.Error(errors.Wrap(err, "create user auth error"))
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

			return
		}
	}

	authReq := AuthorizeRequest{
		ClientId:            req.ClientId,
		RedirectUri:         req.RedirectUri,
		Scope:               scope,
		State:               req.State,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
	}
	finishAuthorize(c, app, info, account, authReq)
}

// Revoke handles POST /oauth/revoke (RFC 7009, Mastodon-compatible).
//
// The token comes from the form/JSON body; when the body omits it, the
// caller's own bearer token is revoked (the web account switcher signs out a
// background account this way). Client credentials are optional: possession
// of the token is enough to revoke it. Unknown or already revoked tokens still
// answer 200 {}.
func Revoke(c *gin.Context) {
	var req RevokeRequest
	_ = c.ShouldBind(&req)

	tokenStr := strings.TrimSpace(req.Token)
	if tokenStr == "" {
		if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
			tokenStr = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}

	if tokenStr == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid_request", "error_description": "token is required"})

		return
	}

	// Purge the auth cache first and unconditionally: even when the row lookup
	// fails, a cached "valid" entry must not keep the token alive.
	middleware.InvalidateTokenCache(tokenStr)

	pairHash, err := models.RevokeTokenByTokenStr(db.GetDB(), tokenStr)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "revoke token error"))
	}

	middleware.InvalidateTokenCacheHash(pairHash)

	// Purge again in case a request that read the row before the revoke
	// re-populated the cache in between.
	middleware.InvalidateTokenCache(tokenStr)

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

// responseScopes returns the registered scopes as an array; Mastodon
// defaults to ["read"] when the client sent none.
func responseScopes(raw string) []string {
	out := strings.Fields(raw)
	if len(out) == 0 {
		return []string{"read"}
	}

	return out
}

func websitePtr(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}
