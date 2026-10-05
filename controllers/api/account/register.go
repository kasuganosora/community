package account

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/captcha"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/email"
	"github.com/kasuganosora/community/i18nz"
	"github.com/kasuganosora/community/invitecode"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type RegisterRequest struct {
	DisplayName       string `binding:"required"          form:"user[account_attributes][display_name]"`
	UserName          string `binding:"required"          form:"user[account_attributes][username]"`
	Email             string `binding:"required"          form:"user[email]"`
	Password          string `binding:"required"          form:"user[password]"`
	PasswordConfirm   string `binding:"required"          form:"user[password_confirmation]"`
	Website           string `form:"user[website]"`
	InviteCode        string `form:"user[invite_code]"`
	RecaptchaResponse string `form:"grecaptcha[response]"`
}

func Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	if msg := validateRegisterRequest(&req); msg != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": msg})

		return
	}

	// 检查当前是否开放注册。缺省与 /api/v1/instance 一致：没写配置就视为关闭。
	if !systemsettings.GetBool(systemsettings.KeyIsEnableRegistration, false) {
		c.JSON(http.StatusForbidden, gin.H{"error": "本站暂不开放注册"})

		return
	}

	// 开放注册之后先过人机验证，避免用「用户名已存在」探号。
	if !requireCaptcha(c, captchaToken(c, req.RecaptchaResponse)) {
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
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	if existsFieldName != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": existsFieldName + " has been registered",
			"field": existsFieldName,
		})

		return
	}

	// 检查两次密码是否一致
	if req.Password != req.PasswordConfirm {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "passwords do not match"})

		return
	}

	// 邀请码
	invCode, err := checkAndGetInviteCode(req.InviteCode)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})

		return
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
	if acc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	payload := gin.H{"message": "register success", "account": acc} //nolint:goconst
	if user.Status == models.UserStatusUnapproved {
		payload["approval_required"] = true
	}

	c.JSON(http.StatusOK, payload)
}

func sendWelcomeEmail(c *gin.Context, req RegisterRequest, user *models.User) (err error) {
	acceptLanguage := c.Request.Header.Get("Accept-Language")
	mailTemplate := i18nz.GetMailTemplateName(acceptLanguage, "welcome_mail")

	mailData := map[string]any{
		"UserName": user.UserName,                                                  //nolint:goconst
		"SiteName": systemsettings.GetString(systemsettings.KeyTitle, "Community"), //nolint:goconst
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

	if systemsettings.GetBool(systemsettings.KeyRegistrationApprovalRequired, false) {
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

		return user, account, err
	}

	account = &models.Account{
		DisplayName: req.DisplayName,
		Website:     req.Website,
		Username:    req.UserName,
	}

	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		// 消费邀请码: 与用户创建处于同一事务, 保证 use_count 递增与用户创建一起成功/回滚.
		// 这样即使后续创建用户/账号失败, use_count 也会随事务回滚, 不会白白消耗名额.
		// 注意: 仅有提供了邀请码时才消费; 未提供邀请码 (code==nil) 时跳过.
		if code != nil {
			consumed, consumeErr := invitecode.ConsumeInviteCodeTx(tx, code.Code)
			if consumeErr != nil && !errors.Is(consumeErr, invitecode.ErrNoInviteCode) {
				return consumeErr
			}

			// 消费成功 (consumed != nil), 用最新状态覆盖入参, 供后续 ExecAfterRegisterAction 使用.
			if consumed != nil {
				*code = *consumed
			}
		}

		err := tx.Create(user).Error
		if err != nil {
			return err
		}

		userSecret.UserId = user.Id
		err = tx.Create(userSecret).Error
		if err != nil {
			return err
		}

		account.UserId = user.Id
		err = tx.Create(account).Error
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		// 邀请码相关错误 (不存在/不可用/已用尽) 使用 422 返回, 与原先 checkAndGetInviteCode 的语义一致.
		if isInviteCodeConsumeError(err) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			log.ErrorV(err)

			return user, account, err
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": "create user error"})

		err = errors.WithMessage(err, "create user error")
		log.ErrorV(err)
	}

	return user, account, err
}

// requireCaptcha consumes a captcha token when the site has captcha enabled.
// It writes the response and returns false when the caller should stop.
func requireCaptcha(c *gin.Context, token string) bool {
	if !captcha.IsCaptchaEnabled() {
		return true
	}
	if err := checkCaptcha(c, token); err != nil {
		log.ErrorV(err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "验证码没有通过，请再试一次"})
		return false
	}
	return true
}

// captchaToken reads whichever field the widget posted. The bound form name is
// Mastodon's grecaptcha[response]; hCaptcha and Turnstile use their own.
func captchaToken(c *gin.Context, bound string) string {
	if token := strings.TrimSpace(bound); token != "" {
		return token
	}

	for _, key := range []string{"h-captcha-response", "cf-turnstile-response", "captcha_response"} {
		if token := strings.TrimSpace(c.PostForm(key)); token != "" {
			return token
		}
	}

	return ""
}

// checkCaptcha 检查验证码.
func checkCaptcha(c *gin.Context, response string) (err error) {
	remoteIP := c.ClientIP()

	result, err := captcha.GetService().Verify(c.Request.Context(), response, remoteIP)
	if err != nil {
		return errors.WithMessage(err, "captcha verify error")
	}

	if !result.Success {
		return errors.New("captcha verify failed")
	}

	return
}

// isInviteCodeConsumeError 判断错误是否来自邀请码消费 (不存在/不可用/已用尽).
// 这类错误应作为客户端错误 (422) 返回, 而非 500.
func isInviteCodeConsumeError(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()

	for _, s := range []string{
		"invite code not found",
		"invite code is not available",
		"query invite code failed",
		"increment use_count failed",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}

	return false
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

		// 其它数据库错误 (连接错误等) 必须返回, 不能继续往下走.
		// 之前这里吞掉了错误, 导致 inv 为零值结构体, 而 IsAvailable() 对零值返回 true,
		// 会让一个不存在的/出错的邀请码被当作有效邀请码放行 (安全漏洞).
		err = errors.WithMessage(err, "query invite code failed")

		return
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

		return "", err
	}

	if exists > 0 {
		existsFieldName = "username"

		return existsFieldName, nil
	}

	exists = 0
	emailHash := models.GetEmailHash(email)

	err = db.GetDB().Model(&models.User{}).Where("email_hash = ?", emailHash).Count(&exists).Error
	if err != nil {
		err = errors.WithMessage(err, "check email exists error")

		return "", err
	}

	if exists > 0 {
		existsFieldName = "email"

		return existsFieldName, nil
	}

	return "", nil
}

// usernameRegexp allows letters (including CJK), digits and underscores.
// Usernames end up in URLs (/users/<name>, /@<name>) and in
// acct:<name>@<domain> WebFinger handles, so characters such as '/', '.',
// '@', '?', '#' or whitespace must never be accepted. Before this check a
// username like "../../etc" could be registered.
var usernameRegexp = regexp.MustCompile(`^[\p{L}\p{N}_]+$`)

const (
	maxUsernameLength = 20
	minPasswordLength = 8
	maxDisplayNameLen = 64
)

// validateRegisterRequest validates the user supplied registration fields
// and returns an error message, or "" when they are acceptable.
func validateRegisterRequest(req *RegisterRequest) string {
	if n := utf8.RuneCountInString(req.UserName); n == 0 || n > maxUsernameLength || !usernameRegexp.MatchString(req.UserName) {
		return "username may only contain letters, numbers and underscores (max 20 characters)"
	}

	if utf8.RuneCountInString(req.DisplayName) > maxDisplayNameLen {
		return "display name is too long (max 64 characters)"
	}

	addr, err := mail.ParseAddress(req.Email)
	if err != nil || addr.Address != req.Email || !strings.Contains(req.Email, "@") {
		return "email is invalid"
	}

	if len(req.Password) < minPasswordLength {
		return "password is too short (minimum is 8 characters)"
	}

	return ""
}
