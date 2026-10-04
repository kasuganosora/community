package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

// MiAuthPage handles GET /miauth/:session.
// After approval the browser is sent to callback?session=<session>.
func MiAuthPage(c *gin.Context) {
	session := c.Param("session")
	if !sessionIDPattern.MatchString(session) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid session"})

		return
	}

	name := c.Query("name")
	icon := c.Query("icon")
	callback := c.Query("callback")
	permission := c.Query("permission")

	if callback != "" && !isAllowedRedirect(callback) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid callback"})

		return
	}

	if icon != "" && !isAllowedRedirect(icon) {
		icon = ""
	}

	if !c.GetBool("authenticated") {
		c.Redirect(http.StatusFound, loginReturnURL(c))

		return
	}

	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	renderConsent(c, consentView{
		Title:       "Misskey MiAuth",
		Domain:      systemsettings.GetDomain(),
		AppName:     fallbackName(name, "Misskey app"),
		Website:     "",
		Icon:        icon,
		DisplayName: account.DisplayName,
		Username:    account.Username,
		Labels:      scopeLabels(strings.ReplaceAll(permission, ",", " ")),
		Action:      "/miauth/" + session,
		Hidden: hiddenPairs(map[string]string{
			"name":       name,
			"icon":       icon,
			"callback":   callback,
			"permission": permission,
		}, "name", "icon", "callback", "permission"),
	})
}

// MiAuthDecision handles POST /miauth/:session.
func MiAuthDecision(c *gin.Context) {
	session := c.Param("session")
	if !sessionIDPattern.MatchString(session) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid session"})

		return
	}

	if !c.GetBool("authenticated") {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})

		return
	}

	name := c.PostForm("name")
	callback := c.PostForm("callback")
	permission := c.PostForm("permission")
	icon := c.PostForm("icon")

	if callback != "" && !isAllowedRedirect(callback) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid callback"})

		return
	}

	if c.PostForm("deny") != "" {
		c.Redirect(http.StatusFound, "/")

		return
	}

	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	scope := misskeyPermissionToScope(permission)
	access, _, _, _, err := persistTokenPair(db.GetDB(), account.UserId, 0, scope, "miauth:"+session)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "miauth token"))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	row := &models.MisskeyAuthSession{
		Session:     session,
		Kind:        "miauth",
		AppName:     fallbackName(name, "Misskey app"),
		AppIcon:     icon,
		CallbackURL: callback,
		Permission:  permission,
		UserId:      account.UserId,
		AccountId:   account.Id,
		AccessToken: access,
		Approved:    true,
		ExpiresAt:   ptrTime(time.Now().Add(time.Hour)),
	}
	if err = db.GetDB().Where("session = ?", session).Assign(row).FirstOrCreate(row).Error; err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	// FirstOrCreate does not overwrite an existing row's token. Force the approval.
	if err = db.GetDB().Model(&models.MisskeyAuthSession{}).Where("session = ?", session).Updates(map[string]any{
		"kind": "miauth", "app_name": row.AppName, "callback_url": callback, "permission": permission,
		"user_id": account.UserId, "account_id": account.Id, "access_token": access, "approved": true,
		"expires_at": row.ExpiresAt,
	}).Error; err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	if callback == "" {
		renderOOB(c, access, row.AppName, false)

		return
	}

	loc, err := appendQuery(callback, map[string]string{"session": session})
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid callback"})

		return
	}

	c.Redirect(http.StatusFound, loc)
}

// MiAuthCheck handles POST /api/miauth/:session/check.
func MiAuthCheck(c *gin.Context) {
	session := c.Param("session")
	var row models.MisskeyAuthSession
	err := db.GetDB().Where("session = ? AND kind = ?", session, "miauth").First(&row).Error
	if err != nil || !row.Approved || row.AccessToken == "" || sessionExpired(row.ExpiresAt) {
		c.JSON(http.StatusOK, gin.H{"ok": false})

		return
	}

	account := &models.Account{}
	if err = db.GetDB().First(account, row.AccountId).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"token": row.AccessToken,
		"user":  misskeyUser(account),
	})
}

// MisskeyCreateApp handles POST /api/app/create (legacy Misskey app registration).
func MisskeyCreateApp(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid request"})

		return
	}

	var raw map[string]json.RawMessage
	if err = json.Unmarshal(body, &raw); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid json"})

		return
	}

	var name, description, callback string
	_ = json.Unmarshal(raw["name"], &name)
	_ = json.Unmarshal(raw["description"], &description)
	_ = json.Unmarshal(raw["callbackUrl"], &callback)
	permission := decodePermission(raw["permission"])

	name = clipRunes(name, 128)
	description = clipRunes(description, 255)
	if name == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "name is required"})

		return
	}

	if callback == "" {
		callback = oobURI
	}

	if !isAllowedRedirect(callback) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid callbackUrl"})

		return
	}

	scope := misskeyPermissionToScope(permission)
	app := &OAuthApplication{
		Application: models.Application{
			Name:        name,
			Website:     description,
			RedirectUri: callback,
			Scopes:      parseScopes(scope),
			IsEnabled:   true,
		},
		ClientId:     utils.GetRandomHex(32),
		ClientSecret: utils.GetRandomHex(64),
	}
	if err = db.GetDB().Create(app).Error; err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          strconv.FormatUint(app.Id, 10),
		"name":        app.Name,
		"callbackUrl": callbackURLOrNil(callback),
		"permission":  splitPermission(permission),
		"secret":      app.ClientSecret,
	})
}

// MisskeyGenerateSession handles POST /api/auth/session/generate.
func MisskeyGenerateSession(c *gin.Context) {
	var req struct {
		AppSecret string `json:"appSecret"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.AppSecret == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "appSecret is required"})

		return
	}

	app := &OAuthApplication{}
	if err := db.GetDB().Where("client_secret = ?", req.AppSecret).First(app).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid appSecret"})

		return
	}

	token := utils.GetRandomHex(32)
	row := &models.MisskeyAuthSession{
		Session:       token,
		Kind:          "legacy",
		AppName:       app.Name,
		CallbackURL:   app.RedirectUri,
		Permission:    app.Scopes.OAuthScopeString(),
		ApplicationId: app.Id,
		ExpiresAt:     ptrTime(time.Now().Add(time.Hour)),
	}
	if err := db.GetDB().Create(row).Error; err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"url":   issuer() + "/auth/" + token,
	})
}

// LegacyAuthPage handles GET /auth/:token.
func LegacyAuthPage(c *gin.Context) {
	token := c.Param("token")
	row, app := loadLegacySession(token)
	if row == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "session not found"})

		return
	}

	if !c.GetBool("authenticated") {
		c.Redirect(http.StatusFound, loginReturnURL(c))

		return
	}

	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	name := row.AppName
	if app != nil && app.Name != "" {
		name = app.Name
	}

	renderConsent(c, consentView{
		Title:       "授权应用",
		Domain:      systemsettings.GetDomain(),
		AppName:     name,
		DisplayName: account.DisplayName,
		Username:    account.Username,
		Labels:      scopeLabels(row.Permission),
		Action:      "/auth/" + token,
	})
}

// LegacyAuthDecision handles POST /auth/:token.
// The callback is callbackUrl?token=<session token>, matching Misskey.
// The access token itself is fetched from /api/auth/session/userkey.
func LegacyAuthDecision(c *gin.Context) {
	token := c.Param("token")
	row, app := loadLegacySession(token)
	if row == nil || app == nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "session not found"})

		return
	}

	if !c.GetBool("authenticated") {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})

		return
	}

	if c.PostForm("deny") != "" {
		c.Redirect(http.StatusFound, "/")

		return
	}

	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	scope := row.Permission
	if scope == "" {
		scope = app.Scopes.OAuthScopeString()
	}

	access, _, _, _, err := persistTokenPair(db.GetDB(), account.UserId, app.Id, scope, "")
	if err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	row.Approved = true
	row.UserId = account.UserId
	row.AccountId = account.Id
	row.AccessToken = access
	if err = db.GetDB().Save(row).Error; err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	callback := strings.TrimSpace(row.CallbackURL)
	if callback == "" || isOOB(callback) {
		renderOOB(c, token, app.Name, false)

		return
	}

	loc, err := appendQuery(callback, map[string]string{"token": token})
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid callback"})

		return
	}

	c.Redirect(http.StatusFound, loc)
}

// MisskeyUserKey handles POST /api/auth/session/userkey.
func MisskeyUserKey(c *gin.Context) {
	var req struct {
		AppSecret string `json:"appSecret"`
		Token     string `json:"token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.AppSecret == "" || req.Token == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "appSecret and token are required"})

		return
	}

	app := &OAuthApplication{}
	if err := db.GetDB().Where("client_secret = ?", req.AppSecret).First(app).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid appSecret"})

		return
	}

	row := &models.MisskeyAuthSession{}
	if err := db.GetDB().Where("session = ? AND kind = ? AND application_id = ?", req.Token, "legacy", app.Id).First(row).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid token"})

		return
	}

	if !row.Approved || row.Consumed || row.AccessToken == "" || sessionExpired(row.ExpiresAt) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid token"})

		return
	}

	account := &models.Account{}
	if err := db.GetDB().First(account, row.AccountId).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid token"})

		return
	}

	row.Consumed = true
	if err := db.GetDB().Model(row).Update("consumed", true).Error; err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	sum := sha256.Sum256([]byte(row.AccessToken + req.AppSecret))
	c.JSON(http.StatusOK, gin.H{
		"accessToken": row.AccessToken,
		"user":        misskeyUser(account),
		// Legacy secure-token clients derive i as sha256(accessToken + appSecret).
		"i": hex.EncodeToString(sum[:]),
	})
}

func loadLegacySession(token string) (*models.MisskeyAuthSession, *OAuthApplication) {
	if !sessionIDPattern.MatchString(token) {
		return nil, nil
	}

	row := &models.MisskeyAuthSession{}
	if err := db.GetDB().Where("session = ? AND kind = ?", token, "legacy").First(row).Error; err != nil {
		return nil, nil
	}

	if sessionExpired(row.ExpiresAt) {
		return nil, nil
	}

	app := &OAuthApplication{}
	if err := db.GetDB().First(app, row.ApplicationId).Error; err != nil {
		return row, nil
	}

	return row, app
}

func misskeyUser(account *models.Account) gin.H {
	name := account.DisplayName
	if name == "" {
		name = account.Username
	}

	return gin.H{
		"id":          strconv.FormatUint(account.Id, 10),
		"username":    account.Username,
		"name":        name,
		"host":        nil,
		"avatarUrl":   account.Avatar,
		"isBot":       account.Bot,
		"isLocked":    account.Locked,
		"isCat":       false,
		"isAdmin":     false,
		"isModerator": false,
	}
}

func misskeyPermissionToScope(permission string) string {
	read, write, follow, push := false, false, false, false
	for _, part := range splitPermission(permission) {
		switch {
		case part == "read" || strings.HasPrefix(part, "read:"):
			read = true
		case part == "write" || strings.HasPrefix(part, "write:"):
			write = true
		case part == "follow" || strings.Contains(part, "following"):
			follow = true
		case part == "push":
			push = true
		}
	}

	if !read && !write && !follow && !push {
		read = true
	}

	var parts []string
	if read {
		parts = append(parts, "read")
	}

	if write {
		parts = append(parts, "write")
	}

	if follow {
		parts = append(parts, "follow")
	}

	if push {
		parts = append(parts, "push")
	}

	return strings.Join(parts, " ")
}

func splitPermission(permission string) []string {
	permission = strings.ReplaceAll(permission, ",", " ")
	return strings.Fields(permission)
}

func decodePermission(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return one
	}

	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return strings.Join(many, ",")
	}

	return ""
}

func callbackURLOrNil(callback string) any {
	if callback == "" || isOOB(callback) {
		return nil
	}

	return callback
}

func fallbackName(name, fallback string) string {
	name = clipRunes(name, 128)
	if name == "" {
		return fallback
	}

	return name
}

func clipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n])
	}

	return string(r)
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func sessionExpired(t *time.Time) bool {
	return t != nil && t.Before(time.Now())
}
