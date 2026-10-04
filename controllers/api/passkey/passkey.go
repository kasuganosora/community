package passkey

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/kasuganosora/community/controllers/api/account"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

const ceremonyCookie = "passkey_ceremony"

type storedCeremony struct {
	Session webauthn.SessionData `json:"session"`
	Name    string               `json:"name,omitempty"`
}

type waUser struct {
	user        *models.User
	account     *models.Account
	credentials []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte { return []byte(u.user.UUID) }
func (u *waUser) WebAuthnName() string {
	if u.account != nil && u.account.Username != "" {
		return u.account.Username
	}

	return u.user.UserName
}
func (u *waUser) WebAuthnDisplayName() string {
	if u.account != nil && u.account.DisplayName != "" {
		return u.account.DisplayName
	}

	return u.WebAuthnName()
}
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// LoginBegin starts a discoverable (usernameless) passkey assertion.
func LoginBegin(c *gin.Context) {
	wa, err := relyingParty(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "passkey is not configured"})

		return
	}

	assertion, session, err := wa.BeginDiscoverableLogin()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "could not start passkey login"})

		return
	}

	if err = saveCeremony(c, "login", 0, "", session); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "could not start passkey login"})

		return
	}

	c.JSON(http.StatusOK, assertion)
}

// LoginFinish completes passkey login and issues the normal web session.
func LoginFinish(c *gin.Context) {
	wa, ceremony, err := takeCeremony(c, "login")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "passkey ceremony expired"})

		return
	}

	user, credential, err := wa.FinishPasskeyLogin(userByCredential, ceremony.Session, c.Request)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "passkey verification failed"})

		return
	}

	concrete, ok := user.(*waUser)
	if !ok || concrete.user == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "passkey verification failed"})

		return
	}

	if err = saveUpdatedCredential(concrete.user.Id, credential); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "could not update passkey"})

		return
	}

	if systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false) && !concrete.user.IsMailVerified {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "email not verified"})

		return
	}

	if concrete.user.Status != models.UserStatusNormal {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user status abnormal"})

		return
	}

	token, err := account.IssueWebSession(c, concrete.user)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "login success", "token": token})
}

// RegisterBegin starts passkey registration for the signed-in user.
func RegisterBegin(c *gin.Context) {
	owner, err := loadContextUser(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})

		return
	}

	wa, err := relyingParty(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "passkey is not configured"})

		return
	}

	var body struct {
		Name string `json:"name"`
	}
	_ = c.ShouldBindJSON(&body)
	name := clipRunes(body.Name, 64)
	if name == "" {
		name = "Passkey"
	}

	creation, session, err := wa.BeginRegistration(owner, webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(owner.WebAuthnCredentials()).CredentialDescriptors()))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "could not start passkey registration"})

		return
	}

	if err = saveCeremony(c, "register", owner.user.Id, name, session); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "could not start passkey registration"})

		return
	}

	c.JSON(http.StatusOK, creation)
}

// RegisterFinish stores the new passkey.
func RegisterFinish(c *gin.Context) {
	owner, err := loadContextUser(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})

		return
	}

	wa, ceremony, err := takeCeremony(c, "register")
	if err != nil || ceremony.Session.UserID == nil || string(ceremony.Session.UserID) != owner.user.UUID {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "passkey ceremony expired"})

		return
	}

	credential, err := wa.FinishRegistration(owner, ceremony.Session, c.Request)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "passkey verification failed"})

		return
	}

	raw, err := json.Marshal(credential)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "could not store passkey"})

		return
	}

	row := &models.PasskeyCredential{
		UserId:         owner.user.Id,
		CredentialID:   base64.RawURLEncoding.EncodeToString(credential.ID),
		CredentialJSON: raw,
		Name:           ceremony.Name,
	}
	if row.Name == "" {
		row.Name = "Passkey"
	}

	if err = db.GetDB().Create(row).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "could not store passkey"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"id": strconv.FormatUint(row.Id, 10), "name": row.Name})
}

// List returns the signed-in user's passkeys, without key material.
func List(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	var rows []models.PasskeyCredential
	if err := db.GetDB().Where("user_id = ?", account.UserId).Order("id desc").Find(&rows).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		item := gin.H{"id": strconv.FormatUint(row.Id, 10), "name": row.Name}
		if row.CreatedAt != nil {
			item["created_at"] = row.CreatedAt.UTC().Format(time.RFC3339)
		}

		if row.LastUsedAt != nil {
			item["last_used_at"] = row.LastUsedAt.UTC().Format(time.RFC3339)
		}

		out = append(out, item)
	}

	c.JSON(http.StatusOK, out)
}

// Delete removes one of the signed-in user's passkeys.
func Delete(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid id"})

		return
	}

	res := db.GetDB().Where("id = ? AND user_id = ?", id, account.UserId).Delete(&models.PasskeyCredential{})
	if res.Error != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	if res.RowsAffected == 0 {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func relyingParty(c *gin.Context) (*webauthn.WebAuthn, error) {
	base := systemsettings.GetBaseURL()
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" {
		return nil, errors.New("invalid base url")
	}

	origins := []string{parsed.Scheme + "://" + parsed.Host}
	if origin := c.GetHeader("Origin"); origin != "" {
		ou, oerr := url.Parse(origin)
		if oerr == nil && (ou.Hostname() == parsed.Hostname() || bothLoopback(ou.Hostname(), parsed.Hostname())) {
			origins = append(origins, origin)
		}
	}

	if bothLoopback(parsed.Hostname(), parsed.Hostname()) && c.Request.Host != "" {
		origins = append(origins, "http://"+c.Request.Host, "https://"+c.Request.Host)
	}

	return webauthn.New(&webauthn.Config{
		RPDisplayName: "Community",
		RPID:          parsed.Hostname(),
		RPOrigins:     uniqueStrings(origins),
	})
}

func bothLoopback(a, b string) bool {
	return isLoopbackHost(a) && isLoopbackHost(b)
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")

	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, item := range in {
		if item == "" || item == "://" {
			continue
		}

		if _, ok := seen[item]; ok {
			continue
		}

		seen[item] = struct{}{}
		out = append(out, item)
	}

	return out
}

func saveCeremony(c *gin.Context, kind string, userID uint64, name string, session *webauthn.SessionData) error {
	raw, err := json.Marshal(storedCeremony{Session: *session, Name: name})
	if err != nil {
		return err
	}

	id := utils.GetRandomHex(32)
	row := &models.WebAuthnSession{
		ID:        id,
		UserId:    userID,
		Kind:      kind,
		Data:      raw,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	if err = db.GetDB().Create(row).Error; err != nil {
		return err
	}

	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(ceremonyCookie, id, 300, "/", "", secure, true)
	_ = db.GetDB().Where("expires_at < ?", time.Now()).Delete(&models.WebAuthnSession{})

	return nil
}

func takeCeremony(c *gin.Context, kind string) (*webauthn.WebAuthn, *storedCeremony, error) {
	wa, err := relyingParty(c)
	if err != nil {
		return nil, nil, err
	}

	id, err := c.Cookie(ceremonyCookie)
	if err != nil || id == "" {
		return nil, nil, errors.New("missing ceremony")
	}

	row := &models.WebAuthnSession{}
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND kind = ?", id, kind).First(row).Error; err != nil {
			return err
		}

		res := tx.Where("id = ? AND kind = ?", id, kind).Delete(&models.WebAuthnSession{})
		if res.Error != nil {
			return res.Error
		}

		if res.RowsAffected == 0 {
			return errors.New("ceremony used")
		}

		return nil
	})
	clearCeremony(c)
	if err != nil {
		return nil, nil, err
	}

	if row.ExpiresAt.Before(time.Now()) {
		return nil, nil, errors.New("expired")
	}

	var stored storedCeremony
	if err = json.Unmarshal(row.Data, &stored); err != nil {
		return nil, nil, err
	}

	return wa, &stored, nil
}

func clearCeremony(c *gin.Context) {
	if id, err := c.Cookie(ceremonyCookie); err == nil && id != "" {
		_ = db.GetDB().Delete(&models.WebAuthnSession{}, "id = ?", id)
	}

	c.SetCookie(ceremonyCookie, "", -1, "/", "", false, true)
}

func loadContextUser(c *gin.Context) (*waUser, error) {
	account, ok := c.Get("account")
	if !ok {
		return nil, errors.New("no account")
	}

	acc, ok := account.(*models.Account)
	if !ok || acc.UserId == 0 {
		return nil, errors.New("no account")
	}

	user := &models.User{}
	if err := db.GetDB().First(user, acc.UserId).Error; err != nil {
		return nil, err
	}

	creds, err := credentialsFor(user.Id)
	if err != nil {
		return nil, err
	}

	return &waUser{user: user, account: acc, credentials: creds}, nil
}

func credentialsFor(userID uint64) ([]webauthn.Credential, error) {
	var rows []models.PasskeyCredential
	if err := db.GetDB().Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]webauthn.Credential, 0, len(rows))
	for _, row := range rows {
		var cred webauthn.Credential
		if err := json.Unmarshal(row.CredentialJSON, &cred); err != nil {
			continue
		}

		out = append(out, cred)
	}

	return out, nil
}

func userByCredential(rawID, userHandle []byte) (webauthn.User, error) {
	encoded := base64.RawURLEncoding.EncodeToString(rawID)
	row := &models.PasskeyCredential{}
	if err := db.GetDB().Where("credential_id = ?", encoded).First(row).Error; err != nil {
		return nil, err
	}

	user := &models.User{}
	if err := db.GetDB().First(user, row.UserId).Error; err != nil {
		return nil, err
	}

	if len(userHandle) > 0 && string(userHandle) != user.UUID {
		return nil, errors.New("user handle mismatch")
	}

	account, _ := user.GetAccountBase(db.GetDB())
	creds, err := credentialsFor(user.Id)
	if err != nil {
		return nil, err
	}

	return &waUser{user: user, account: account, credentials: creds}, nil
}

func clipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n])
	}

	return string(r)
}

func saveUpdatedCredential(userID uint64, credential *webauthn.Credential) error {
	raw, err := json.Marshal(credential)
	if err != nil {
		return err
	}

	now := time.Now()

	return db.GetDB().Model(&models.PasskeyCredential{}).
		Where("user_id = ? AND credential_id = ?", userID, base64.RawURLEncoding.EncodeToString(credential.ID)).
		Updates(map[string]any{"credential_json": raw, "last_used_at": now}).Error
}
