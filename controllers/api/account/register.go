package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/recaptcha"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"net/http"
)

type RegisterRequest struct {
	DisplayName       string `form:"user[account_attributes][display_name]" binding:"required"`
	Username          string `form:"user[account_attributes][username]" binding:"required"`
	Email             string `form:"user[email]" binding:"required"`
	Password          string `form:"user[password]" binding:"required"`
	PasswordConfirm   string `form:"user[password_confirmation]" binding:"required"`
	ConfirmPassword   string `form:"user[confirm_password]"`
	Website           string `form:"user[website]"`
	InviteCode        string `form:"user[invite_code]"`
	RecaptchaResponse string `form:"grecaptcha[response]"`
}

func Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	// 检查当前是否开放注册
	if !systemsettings.GetBool(systemsettings.KeyIsEnableRegistration, false) {
		c.JSON(http.StatusForbidden, gin.H{"error": "registration is not open"})
		return
	}

	// 检查是否需要邀请码才能注册, 而当前请求没有邀请码
	if systemsettings.GetBool(systemsettings.KeyRegistrationInviteRequired, false) && req.InviteCode == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "invite code is required"})
		return
	}

	// 检查当前用户名和邮箱有没被注册
	existsFieldName, err := checkExists(req.Username, req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if existsFieldName != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": existsFieldName + " has been registered",
			"field": existsFieldName,
		})
		return
	}

	// 邀请码
	inviteCode, err := checkAndGetInviteCode(req.InviteCode)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	// 如果开启了 Recaptcha, 那么就验证 Recaptcha
	// 没有开启 Recaptcha
	if systemsettings.GetBool(systemsettings.KeyRecaptchaEnabled, false) {
		err = checkRecaptcha(c, req.RecaptchaResponse)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
	}

	// 创建用户和账号
	// TODO...
	// 创建完后如果有用到邀请码,则将邀请码设置为已使用,并且把当前用户的邀请人设置为邀请码的拥有者,
	// 如果邀请码设置了使用回调,则调用邀请码的回调函数
	// TODO...

}

func checkRecaptcha(c *gin.Context, response string) (err error) {
	// 没有配置 Recaptcha 密钥
	if systemsettings.GetString(systemsettings.KeyRecaptchaSecretKey, "") == "" {
		err = errors.New("recaptcha secret is not configured")
		return
	}

	if response == "" {
		err = errors.New("recaptcha response is empty")
		return
	}

	remoteIP := c.ClientIP()

	success, err := recaptcha.SiteVerify(response, remoteIP)
	if err != nil {
		err = errors.WithMessage(err, "recaptcha site verify error")
		return
	}

	if !success {
		err = errors.New("recaptcha verify failed")
		return
	}

	return
}

func checkAndGetInviteCode(code string) (inviteCode *models.InviteCode, err error) {
	inv := &models.InviteCode{}
	err = db.GetDB().Model(&models.InviteCode{}).Where("code = ?", code).First(&inv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = errors.New("invite code not found")
			return
		}
	}

	if !inv.IsAvailable() {
		err = errors.New("invite code is not available")
		return
	}

	inviteCode = inv
	return
}

func checkExists(username string, email string) (existsFieldName string, err error) {
	var exists int64
	err = db.GetDB().Model(&models.User{}).Where("username = ?", username).Count(&exists).Error
	if err != nil {
		err = errors.WithMessage(err, "check username exists error")
		return
	}

	if exists > 0 {
		existsFieldName = "username"
		return
	}

	exists = 0
	emailHash := models.GetEmailHash(email)
	err = db.GetDB().Model(&models.User{}).Where("email_hash = ?", emailHash).Count(&exists).Error

	if err != nil {
		err = errors.WithMessage(err, "check email exists error")
		return
	}

	if exists > 0 {
		existsFieldName = "email"
		return
	}

	return
}
