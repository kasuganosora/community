package account

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/twofactor"
)

type twoFactorPasswordReq struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

func twoFactorIssuer() string {
	if u, err := url.Parse(systemsettings.GetBaseURL()); err == nil && u.Host != "" {
		return u.Host
	}

	return "community"
}

// TwoFactorStatus GET /accounts/two_factor.
func TwoFactorStatus(c *gin.Context) {
	a, u, ok := lifecycleIdentity(c)
	if !ok {
		return
	}
	_ = a

	row, err := twofactor.Load(db.GetDB(), u.Id)
	if err != nil {
		log.Error("two-factor status for user %d: %v", u.Id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	resp := gin.H{"enabled": row.Enabled(), "enabled_at": nil, "backup_codes_remaining": 0}
	if row.Enabled() {
		n, _ := twofactor.RemainingBackupCodes(db.GetDB(), u.Id)
		resp["enabled_at"], resp["backup_codes_remaining"] = row.EnabledAt, n
	}

	c.JSON(http.StatusOK, resp)
}

func bindTwoFactorPassword(c *gin.Context, u *models.User) (*twoFactorPasswordReq, bool) {
	req := &twoFactorPasswordReq{}
	if c.ShouldBindJSON(req) != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid request"})

		return nil, false
	}

	return req, lifecyclePasswordOnly(c, u.Id, req.Password)
}

// lifecyclePasswordOnly verifies the password without the 2FA step.
func lifecyclePasswordOnly(c *gin.Context, userID uint64, password string) bool {
	if password == "" || len(password) > 1024 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid password"})

		return false
	}

	var secret models.UserSecretData
	if err := db.GetDB().WithContext(c.Request.Context()).First(&secret, "user_id = ?", userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "password verification unavailable"})

		return false
	}

	if secret.CheckPassword(password) != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid password"})

		return false
	}

	return true
}

// TwoFactorSetup POST /accounts/two_factor/setup {password}: returns a
// pending secret and otpauth URI. Nothing is enabled until confirm.
func TwoFactorSetup(c *gin.Context) {
	a, u, ok := lifecycleIdentity(c)
	if !ok {
		return
	}

	if _, ok = bindTwoFactorPassword(c, u); !ok {
		return
	}

	secret, err := twofactor.BeginSetup(db.GetDB(), u.Id)
	if errors.Is(err, twofactor.ErrAlreadyEnabled) {
		c.JSON(http.StatusConflict, gin.H{"error": "two_factor_already_enabled"})

		return
	}

	if err != nil {
		log.Error("two-factor setup for user %d: %v", u.Id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	issuer := twoFactorIssuer()
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"secret":      secret,
		"otpauth_uri": twofactor.ProvisioningURI(issuer, a.Username+"@"+issuer, secret),
	})
}

// TwoFactorConfirm POST /accounts/two_factor/confirm {code}: enables TOTP
// and returns the backup codes exactly once.
func TwoFactorConfirm(c *gin.Context) {
	_, u, ok := lifecycleIdentity(c)
	if !ok {
		return
	}

	req := &twoFactorPasswordReq{}
	if c.ShouldBindJSON(req) != nil || req.Code == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "code is required"})

		return
	}

	codes, err := twofactor.ConfirmSetup(db.GetDB(), u.Id, req.Code)
	switch {
	case err == nil:
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"enabled": true, "backup_codes": codes})
	case errors.Is(err, twofactor.ErrInvalidCode):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_two_factor_code"})
	case errors.Is(err, twofactor.ErrNoPending):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "two_factor_setup_expired"})
	case errors.Is(err, twofactor.ErrAlreadyEnabled):
		c.JSON(http.StatusConflict, gin.H{"error": "two_factor_already_enabled"})
	default:
		log.Error("two-factor confirm for user %d: %v", u.Id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// TwoFactorRegenerateBackupCodes POST /accounts/two_factor/backup_codes {password, code}.
func TwoFactorRegenerateBackupCodes(c *gin.Context) {
	_, u, ok := lifecycleIdentity(c)
	if !ok {
		return
	}

	req, ok := bindTwoFactorPassword(c, u)
	if !ok || !RequireSecondFactorForSensitive(c, u.Id, req.Code) {
		return
	}

	codes, err := twofactor.RegenerateBackupCodes(db.GetDB(), u.Id)
	if err != nil {
		log.Error("two-factor backup codes for user %d: %v", u.Id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"backup_codes": codes})
}

// TwoFactorDisable POST /accounts/two_factor/disable {password, code}.
func TwoFactorDisable(c *gin.Context) {
	_, u, ok := lifecycleIdentity(c)
	if !ok {
		return
	}

	req, ok := bindTwoFactorPassword(c, u)
	if !ok {
		return
	}

	if on, _ := twofactor.IsEnabled(db.GetDB(), u.Id); !on {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "two_factor_not_enabled"})

		return
	}

	if !RequireSecondFactorForSensitive(c, u.Id, req.Code) {
		return
	}

	if err := twofactor.Disable(db.GetDB(), u.Id); err != nil {
		log.Error("two-factor disable for user %d: %v", u.Id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"enabled": false})
}
