package account

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/email"
	"github.com/kasuganosora/community/i18nz"
	"github.com/kasuganosora/community/invitecode"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/recaptcha"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"time"
)

type RegisterRequest struct {
	DisplayName       string `form:"user[account_attributes][display_name]" binding:"required"`
	UserName          string `form:"user[account_attributes][username]" binding:"required"`
	Email             string `form:"user[email]" binding:"required"`
	Password          string `form:"user[password]" binding:"required"`
	PasswordConfirm   string `form:"user[password_confirmation]" binding:"required"`
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
	existsFieldName, err := checkExists(req.UserName, req.Email)
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
	invCode, err := checkAndGetInviteCode(req.InviteCode)
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
	user, account, err := createUserAndAccount(c, req, invCode)
	if err != nil {
		log.ErrorV(err)
		return
	}

	// 创建完后如果有用到邀请码,则将邀请码设置为已使用,并且把当前用户的邀请人设置为邀请码的拥有者,
	// 如果邀请码设置了使用回调,则调用邀请码的回调函数(如果要验证邮箱,则在邮箱验证通过后调用回调函数)
	if invCode != nil {
		invitecode.ExecAfterRegisterAction(db.GetDB(), invCode, user, account)
	}

	// 发送验证邮件/欢迎邮件
	if systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false) {
		// 发送验证邮件
		err = sendVerifyEmail(c, req, user)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "send verify email failed"})
			log.ErrorV(err)
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "verify email has been sent"})
		return
	}

	// 不用验证邮箱, 发送欢迎邮件
	err = sendWelcomeEmail(c, req, user)
	if err != nil {
		log.ErrorV(err)
		return
	}

	acc := serializer.ToAccount(account)
	if err != nil {
		err = errors.Wrap(err, "serializer.ToAccount failed")
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "register success", "account": acc})

}

func sendWelcomeEmail(c *gin.Context, req RegisterRequest, user *models.User) (err error) {
	acceptLanguage := c.Request.Header.Get("Accept-Language")
	mailTemplate := i18nz.GetMailTemplateName(acceptLanguage, "welcome_mail")

	mailData := map[string]any{
		"UserName": user.UserName,
		"SiteName": systemsettings.GetString(systemsettings.KeyTitle, "Community"),
	}

	subject := i18nz.GetLocalizer("", acceptLanguage).T("welcome_mail_subject", map[string]any{
		"UserName": user.UserName,
		"SiteName": systemsettings.GetString(systemsettings.KeyTitle, "Community"),
	})

	err = email.SendHtmlTemplate(req.Email, "", "", subject, "", mailTemplate, mailData)
	if err != nil {
		return errors.Wrap(err, "send welcome email failed")
	}

	return nil
}

type VerifyEmailToken struct {
	Token    string `json:"token"`
	ExpireAt int64  `json:"expire_at"`
}

func sendVerifyEmail(c *gin.Context, req RegisterRequest, user *models.User) (err error) {
	token := VerifyEmailToken{
		Token:    utils.GetRandomString(32) + "-" + strconv.FormatUint(user.Id, 10) + "-" + uuid.New().String(),
		ExpireAt: time.Now().Add(time.Minute * 5).Unix(),
	}

	err = models.SaveUserMeta(db.GetDB(), user.Id, "verify_email_token", token)

	if err != nil {
		return errors.Wrap(err, "save verify email token failed")
	}

	acceptLanguage := c.Request.Header.Get("Accept-Language")
	mailTemplate := i18nz.GetMailTemplateName(acceptLanguage, "verify_email")

	mailData := map[string]any{
		"UserName":   user.UserName,
		"VerifyCode": token.Token,
		"SiteName":   systemsettings.GetString(systemsettings.KeyTitle, "Community"),
	}

	subject := i18nz.GetLocalizer("", acceptLanguage).T("verify_email_subject", map[string]any{
		"UserName": user.UserName,
		"SiteName": systemsettings.GetString(systemsettings.KeyTitle, "Community"),
	})

	err = email.SendHtmlTemplate(req.Email, "", "", subject, "", mailTemplate, mailData)

	return
}

func createUserAndAccount(c *gin.Context, req RegisterRequest, code *models.InviteCode) (
	user *models.User, account *models.Account, err error) {
	// 创建用户
	user = &models.User{
		UserName:  req.UserName,
		EmailHash: models.GetEmailHash(req.Email),
	}

	if systemsettings.GetBool(systemsettings.KeyRegistrationApprovalRequired, true) {
		user.Status = models.UserStatusUnapproved
	} else {
		user.Status = models.UserStatusNormal
	}

	if code != nil {
		user.InviteBy = code.UserId
	}

	userSecret := &models.UserSecretData{}
	userSecret.Email = req.Email
	userSecret.Salt = utils.GetRandomString(64)
	userSecret.Password, err = models.EncryptPassword(req.Password, userSecret.Salt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encrypt password failed"})
		log.ErrorV(err)
		return
	}

	account = &models.Account{
		DisplayName: req.DisplayName,
		Website:     req.Website,
		Username:    req.UserName,
	}

	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return err
		}

		userSecret.UserId = user.Id
		if err := tx.Create(userSecret).Error; err != nil {
			return err
		}

		account.UserId = user.Id
		if err := tx.Create(account).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create user error"})
		err = errors.WithMessage(err, "create user error")
		log.ErrorV(err)
	}

	return
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
	if code == "" {
		return
	}

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
