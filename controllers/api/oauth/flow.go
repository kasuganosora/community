package oauth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
)

func resolveAuthorizeClient(c *gin.Context, clientID, redirectURI string) (*OAuthApplication, *ClientInformation, string) {
	if strings.TrimSpace(clientID) == "" {
		return nil, nil, "invalid client_id"
	}

	app := &OAuthApplication{}
	err := db.GetDB().Where("client_id = ?", clientID).First(app).Error
	if err == nil {
		if !app.IsEnabled {
			return nil, nil, "invalid client_id"
		}

		if !isValidRedirectUri(app.RedirectUri, redirectURI) {
			return nil, nil, "invalid redirect_uri"
		}

		return app, nil, ""
	}

	if !isIndieAuthClientID(clientID) {
		return nil, nil, "invalid client_id"
	}

	info, ferr := lookupClientInfo(c.Request.Context(), clientID)
	if ferr != nil {
		return nil, nil, "invalid client_id"
	}

	if !isValidRedirectUri(strings.Join(info.RedirectURIs, "\n"), redirectURI) {
		return nil, nil, "invalid redirect_uri"
	}

	return nil, &info, ""
}

func priorGrantCovers(appID, userID uint64, requested string) bool {
	var row models.ApplicationUserAuth
	err := db.GetDB().Where("application_id = ? AND user_id = ?", appID, userID).First(&row).Error
	if err != nil {
		return false
	}

	stored := strings.TrimSpace(row.Scopes)
	if stored == "" {
		return false
	}

	return scopeStringSubset(stored, requested)
}

func rememberGrant(appID, userID uint64, scope string) error {
	var row models.ApplicationUserAuth
	err := db.GetDB().Where("application_id = ? AND user_id = ?", appID, userID).First(&row).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		return db.GetDB().Create(&models.ApplicationUserAuth{
			ApplicationId: appID,
			UserId:        int64(userID), //nolint:gosec // G115: reviewed and safe
			Scopes:        mergeScopeStrings(scope),
		}).Error
	}

	return db.GetDB().Model(&row).Update("scopes", mergeScopeStrings(row.Scopes, scope)).Error
}

func mergeScopeStrings(parts ...string) string {
	scopes := parseScopes(strings.Join(parts, " "))

	return scopes.OAuthScopeString()
}

func showConsent(c *gin.Context, view consentView) {
	renderConsent(c, view)
}

func consentFromOAuth(app *OAuthApplication, info *ClientInformation, account *models.Account, req AuthorizeRequest) consentView {
	name := ""
	website := ""

	if app != nil {
		name = app.Name
		website = app.Website
	}

	if info != nil {
		name = info.Name
		website = info.ID
	}

	if !isAllowedRedirect(website) || isOOB(website) {
		website = ""
	}

	return consentView{
		Title:       "授权应用",
		Domain:      systemsettings.GetDomain(),
		AppName:     name,
		Website:     website,
		DisplayName: account.DisplayName,
		Username:    account.Username,
		Labels:      scopeLabels(req.Scope),
		Action:      "/oauth/authorize",
		Callback:    displayCallback(req.RedirectUri),
		Hidden: hiddenPairs(map[string]string{
			"client_id":             req.ClientId,
			"redirect_uri":          req.RedirectUri,
			"scope":                 req.Scope,
			"state":                 req.State,
			"code_challenge":        req.CodeChallenge,
			"code_challenge_method": req.CodeChallengeMethod,
		}, "client_id", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method"),
	}
}

func finishAuthorize(c *gin.Context, app *OAuthApplication, info *ClientInformation, account *models.Account, req AuthorizeRequest) {
	var appID uint64
	external := ""
	appName := req.ClientId

	if app != nil {
		appID = app.Id
		appName = app.Name
	}

	if info != nil {
		external = info.ID
		appName = info.Name
	}

	code := generateAuthorizationCode(appID, account.Id, req.RedirectUri, req.Scope, req.CodeChallenge, req.CodeChallengeMethod, external)
	if code == "" {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to generate authorization code"})

		return
	}

	if isOOB(req.RedirectUri) {
		renderOOB(c, code, appName, req.RedirectUri == oobAutoURI)

		return
	}

	c.Redirect(http.StatusFound, buildRedirectUrl(req.RedirectUri, code, req.State))
}

func generateAuthorizationCode(appId, accountId uint64, redirectUri, scopes, challenge, method, external string) string {
	code := utils.GetRandomHex(64)
	expiresAt := time.Now().Add(10 * time.Minute)

	authCode := &OAuthAuthorizationCode{
		Code:                code,
		ApplicationId:       appId,
		AccountId:           accountId,
		RedirectUri:         redirectUri,
		Scopes:              scopes,
		CodeChallenge:       challenge,
		CodeChallengeMethod: method,
		ExternalClientID:    external,
		ExpiresAt:           &expiresAt,
		Used:                false,
	}

	if err := db.GetDB().Create(authCode).Error; err != nil {
		log.Error("Failed to create auth code: %v", err)

		return ""
	}

	return code
}
