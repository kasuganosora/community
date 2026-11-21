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
	"github.com/kasuganosora/community/twofactor"
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
		if !found.IsEnabled {
			return nil, "", false, http.StatusUnauthorized, "invalid_client"
		}
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
		var registered models.ApplicationScope
		if app != nil {
			registered = app.Scopes
		}
		access, refresh, aHash, rHash, err := persistTokenPair(tx, account.UserId, appID, registered, scopeStr, codeRow.ExternalClientID)
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

func persistTokenPair(tx *gorm.DB, userID, appID uint64, registered models.ApplicationScope, scopeStr, external string) (access, refresh, accessHash, refreshHash string, err error) {
	scopes, ok := registered.OAuthGrant(scopeStr)
	if !ok {
		return "", "", "", "", errors.New("invalid_scope")
	}
	return saveTokenPair(tx, userID, appID, scopes, external)
}

func saveTokenPair(tx *gorm.DB, userID, appID uint64, scopes models.ApplicationScope, external string) (access, refresh, accessHash, refreshHash string, err error) {
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

	// Mastodon behaviour: accounts with 2FA cannot use the password grant
	// (clients fall back to the authorization-code flow, whose web login
	// asks for the code). Same invalid_grant so clients behave unchanged.
	if on, tfErr := twofactor.IsEnabled(db.GetDB(), user.Id); on || tfErr != nil {
		if tfErr != nil || req.OTPAttempt == "" || twofactor.Verify(db.GetDB(), user.Id, req.OTPAttempt) != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})

			return
		}
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

	var access, refresh string
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		var err error
		access, refresh, _, _, err = persistTokenPair(tx, user.Id, app.Id, app.Scopes, scope, "")
		return err
	})
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
	if err != nil || oldToken.Revoke != nil || oldToken.Type != models.AuthTokenTypeOauth || oldToken.UserId == 0 ||
		(oldToken.ExpiredAt != nil && !oldToken.ExpiredAt.After(time.Now())) {
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

	data, err := models.GetAuthTokenData(req.RefreshToken, config.C.EncryptedKey)
	if err != nil || data.Scopes == nil || *data.Scopes == (models.ApplicationScope{}) || (data.ExpiredAt > 0 && data.ExpiredAt <= time.Now().Unix()) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})
		return
	}
	granted := *data.Scopes
	scope := strings.TrimSpace(req.Scope)
	if scope == "" || scope == granted.OAuthScopeString() {
		// Preserve the encoded grant even on old tokens whose "follow" bit
		// did not include modern implied scopes.
		scope = granted.OAuthScopeString()
	} else {
		requested, valid := models.ParseOAuthScopes(scope)
		if !valid || !granted.Covers(requested) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_scope"})
			return
		}
		granted = requested
	}

	var access, refresh string
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Model(&models.AuthToken{}).Where("token = ? AND revoke IS NULL", oldToken.Token).Update("revoke", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if oldToken.PairHash != "" {
			if err := tx.Model(&models.AuthToken{}).Where("token = ? AND revoke IS NULL", oldToken.PairHash).Update("revoke", now).Error; err != nil {
				return err
			}
		}
		var err error
		access, refresh, _, _, err = saveTokenPair(tx, oldToken.UserId, oldToken.ApplicationId, granted, oldToken.ExternalClient)
		return err
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})
		return
	}
	if err != nil {
		_ = c.Error(errors.Wrap(err, "rotate token pair"))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
		return
	}
	middleware.InvalidateTokenCache(req.RefreshToken)
	middleware.InvalidateTokenCacheHash(oldToken.PairHash)

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

	scopes, ok := app.Scopes.OAuthGrant(scope)
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid_scope"})

		return
	}

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
