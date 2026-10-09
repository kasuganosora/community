package oauth

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Token handles POST /oauth/token.
func Token(c *gin.Context) {
	var req TokenRequest
	if err := c.ShouldBind(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	applyBasicAuth(c, &req)
	if strings.TrimSpace(req.GrantType) == "" || strings.TrimSpace(req.ClientId) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})

		return
	}

	app, external, secretSkipped, status, code := resolveTokenClient(&req)
	if code != "" {
		c.AbortWithStatusJSON(status, gin.H{"error": code})

		return
	}

	if secretSkipped && req.GrantType != "authorization_code" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_client"})

		return
	}

	switch req.GrantType {
	case "authorization_code":
		handleAuthorizationCodeGrant(c, app, external, secretSkipped, &req)
	case "password":
		if app == nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unauthorized_client"})

			return
		}

		handlePasswordGrant(c, app, &req)
	case "refresh_token":
		handleRefreshTokenGrant(c, app, external, &req)
	case "client_credentials":
		if app == nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unauthorized_client"})

			return
		}

		handleClientCredentialsGrant(c, app, &req)
	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unsupported_grant_type"})
	}
}

func applyBasicAuth(c *gin.Context, req *TokenRequest) {
	id, secret, ok := c.Request.BasicAuth()
	if !ok {
		return
	}

	if req.ClientId == "" {
		req.ClientId = id
	}

	if req.ClientSecret == "" {
		req.ClientSecret = secret
	}
}

func resolveTokenClient(req *TokenRequest) (app *OAuthApplication, external string, secretSkipped bool, status int, code string) {
	found := &OAuthApplication{}
	err := db.GetDB().Where("client_id = ?", req.ClientId).First(found).Error
	if err == nil {
		if req.ClientSecret == "" {
			return found, "", true, 0, ""
		}

		if subtle.ConstantTimeCompare([]byte(req.ClientSecret), []byte(found.ClientSecret)) != 1 {
			return nil, "", false, http.StatusUnauthorized, "invalid_client"
		}

		return found, "", false, 0, ""
	}

	if isIndieAuthClientID(req.ClientId) && req.ClientSecret == "" {
		return nil, req.ClientId, false, 0, ""
	}

	return nil, "", false, http.StatusUnauthorized, "invalid_client"
}

func handleAuthorizationCodeGrant(c *gin.Context, app *OAuthApplication, external string, secretSkipped bool, req *TokenRequest) {
	var (
		codeRow      *OAuthAuthorizationCode
		account      *models.Account
		reuseHashes  [2]string
		reused       bool
		accessToken  string
		refreshToken string
		scopeStr     string
	)

	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		codeRow = &OAuthAuthorizationCode{}

		query := tx
		// SQLite and the sqlexec test engine have no row locks.
		if dialect.IsMySQL(tx) || dialect.IsPostgres(tx) {
			query = tx.Clauses(clause.Locking{Strength: "UPDATE"})
		}

		query = query.Where("code = ?", req.Code)
		if app != nil {
			query = query.Where("application_id = ?", app.Id)
		} else {
			query = query.Where("external_client_id = ?", external)
		}

		if err := query.First(codeRow).Error; err != nil {
			return errors.New("invalid_grant")
		}

		if codeRow.Used {
			reused = true
			reuseHashes = [2]string{codeRow.AccessTokenHash, codeRow.RefreshTokenHash}

			return nil
		}

		if codeRow.ExpiresAt != nil && codeRow.ExpiresAt.Before(time.Now()) {
			return errors.New("invalid_grant")
		}

		redirectURI := req.RedirectUri
		if redirectURI == "" {
			redirectURI = codeRow.RedirectUri
		}

		if codeRow.RedirectUri != redirectURI {
			return errors.New("invalid_grant")
		}

		if codeRow.CodeChallengeMethod != "" {
			if !verifyPKCE(req.CodeVerifier, codeRow.CodeChallenge, codeRow.CodeChallengeMethod) {
				return errors.New("invalid_grant")
			}
		} else if secretSkipped || external != "" {
			return errors.New("invalid_client")
		}

		account = &models.Account{}
		if err := tx.Preload("User").Preload("Extra").First(account, codeRow.AccountId).Error; err != nil {
			return errors.New("invalid_grant")
		}

		var appID uint64
		if app != nil {
			appID = app.Id
		}

		scopeStr = codeRow.Scopes
		access, refresh, aHash, rHash, err := persistTokenPair(tx, account.UserId, appID, scopeStr, codeRow.ExternalClientID)
		if err != nil {
			return err
		}

		result := tx.Model(&OAuthAuthorizationCode{}).
			Where("code = ? AND used = ?", codeRow.Code, false).
			Updates(map[string]any{
				"used":               true,
				"access_token_hash":  aHash,
				"refresh_token_hash": rHash,
			})
		if result.Error != nil {
			return result.Error
		}

		if result.RowsAffected == 0 {
			return errors.New("code_reused")
		}

		accessToken = access
		refreshToken = refresh

		return nil
	})
	if err != nil {
		if err.Error() == "code_reused" {
			var current OAuthAuthorizationCode
			_ = db.GetDB().Where("code = ?", req.Code).First(&current).Error
			revokeByHashes(current.AccessTokenHash, current.RefreshTokenHash)
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

			return
		}

		status := http.StatusBadRequest
		errCode := err.Error()
		if errCode == "invalid_client" {
			status = http.StatusUnauthorized
		}

		c.AbortWithStatusJSON(status, gin.H{"error": errCode})

		return
	}

	if reused {
		revokeByHashes(reuseHashes[0], reuseHashes[1])
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

		return
	}

	c.JSON(http.StatusOK, TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		Scope:        scopeStr,
		CreatedAt:    time.Now().Unix(),
		RefreshToken: refreshToken,
	})
}

func persistTokenPair(tx *gorm.DB, userID, appID uint64, scopeStr, external string) (access, refresh, accessHash, refreshHash string, err error) {
	scopes := parseScopes(scopeStr)
	accessRow, access := models.NewAuthToken(userID, models.AuthTokenTypeOauth, appID, 0, &scopes, 0, config.C.EncryptedKey)
	refreshRow, refresh := models.NewAuthToken(userID, models.AuthTokenTypeOauth, appID, 0, &scopes, 0, config.C.EncryptedKey)
	accessHash = tokenHash(access)
	refreshHash = tokenHash(refresh)
	accessRow.ExternalClient = external
	refreshRow.ExternalClient = external
	accessRow.PairHash = refreshHash
	refreshRow.PairHash = accessHash

	if err = tx.Create(accessRow).Error; err != nil {
		return "", "", "", "", err
	}

	if err = tx.Create(refreshRow).Error; err != nil {
		return "", "", "", "", err
	}

	return access, refresh, accessHash, refreshHash, nil
}

func revokeByHashes(hashes ...string) {
	now := time.Now()
	for _, hash := range hashes {
		if hash == "" {
			continue
		}

		middleware.InvalidateTokenCacheHash(hash)

		var row models.AuthToken
		if err := db.GetDB().Where("token = ?", hash).First(&row).Error; err != nil {
			continue
		}

		row.Revoke = &now
		if err := db.GetDB().Save(&row).Error; err != nil {
			log.Error("revoke reused authorization code token: %v", err)
		}
	}
}

func handlePasswordGrant(c *gin.Context, app *OAuthApplication, req *TokenRequest) {
	if req.Username == "" || req.Password == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

		return
	}

	user, err := models.FindUserForLogin(db.GetDB(), req.Username)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

		return
	}

	userSecret := &models.UserSecretData{}
	if err = db.GetDB().Where("user_id = ?", user.Id).First(userSecret).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

		return
	}

	if err = userSecret.CheckPassword(req.Password); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

		return
	}

	if systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false) && !user.IsMailVerified {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "email not verified"})

		return
	}

	if user.Status != models.UserStatusNormal {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "user status abnormal"})

		return
	}

	scope, ok := normalizeScope(app, req.Scope)
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_scope"})

		return
	}

	access, refresh, _, _, err := persistTokenPair(db.GetDB(), user.Id, app.Id, scope, "")
	if err != nil {
		_ = c.Error(errors.Wrap(err, "create token error"))

		return
	}

	c.JSON(http.StatusOK, TokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		Scope:        scope,
		CreatedAt:    time.Now().Unix(),
		RefreshToken: refresh,
	})
}

func handleRefreshTokenGrant(c *gin.Context, app *OAuthApplication, external string, req *TokenRequest) {
	if req.RefreshToken == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

		return
	}

	oldToken, err := models.GetTokenByTokenStr(req.RefreshToken)
	if err != nil || oldToken.Revoke != nil || oldToken.Type != models.AuthTokenTypeOauth {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

		return
	}

	if app != nil && oldToken.ApplicationId != app.Id {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

		return
	}

	if app == nil && (external == "" || oldToken.ExternalClient != external) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

		return
	}

	stored := ""
	if data, derr := models.GetAuthTokenData(req.RefreshToken, config.C.EncryptedKey); derr == nil {
		stored = data.Scope
	}

	scope := strings.TrimSpace(req.Scope)
	switch {
	case scope == "" && stored != "":
		scope = stored
	case scope == "":
		scope = "read"
	case stored != "" && !scopeStringSubset(stored, scope):
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_scope"})

		return
	}

	now := time.Now()
	res := db.GetDB().Model(&models.AuthToken{}).Where("token = ? AND revoke IS NULL", oldToken.Token).Update("revoke", now)
	if res.Error != nil {
		_ = c.Error(errors.Wrap(res.Error, "save revoked token error"))

		return
	}

	if res.RowsAffected == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

		return
	}

	middleware.InvalidateTokenCache(req.RefreshToken)

	var appID uint64
	clientRef := external
	if app != nil {
		appID = app.Id
		clientRef = ""
	}

	access, refresh, _, _, err := persistTokenPair(db.GetDB(), oldToken.UserId, appID, scope, clientRef)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "create token error"))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	if oldToken.PairHash != "" {
		revokeByHashes(oldToken.PairHash)
	}

	c.JSON(http.StatusOK, TokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		Scope:        scope,
		CreatedAt:    time.Now().Unix(),
		RefreshToken: refresh,
	})
}

func handleClientCredentialsGrant(c *gin.Context, app *OAuthApplication, req *TokenRequest) {
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = "read"
	}

	if !scopeAllowed(app.Scopes, scope) && !(app.Scopes.ReadScope == 0 && app.Scopes.WriteScope == 0 && app.Scopes.AdminScope == 0 && app.Scopes.OtherScope == 0) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_scope"})

		return
	}

	scopes := parseScopes(scope)
	token, tokenStr := models.NewAuthToken(0, models.AuthTokenTypeOauth, app.Id, 0, &scopes, 0, config.C.EncryptedKey)
	if err := db.GetDB().Create(token).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create token error"))

		return
	}

	c.JSON(http.StatusOK, TokenResponse{
		AccessToken: tokenStr,
		TokenType:   "Bearer",
		Scope:       scope,
		CreatedAt:   time.Now().Unix(),
	})
}
