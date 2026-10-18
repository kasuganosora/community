package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"net/http"
	"strconv"
)

type AuthRequest struct {
	Username string `form:"username"            json:"username"`
	Email    string `form:"email"               json:"email"`
	Password string `binding:"required"         form:"password"            json:"password"`
	Captcha  string `form:"grecaptcha[response]" json:"captcha_response"`
	// OTPAttempt optionally carries the TOTP or backup code in the same request.
	OTPAttempt string `form:"otp_attempt" json:"otp_attempt"`
}

func Login(c *gin.Context) {
	req := &AuthRequest{}

	if err := c.ShouldBind(req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	if req.Username == "" && req.Email == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "username or email is required"})

		return
	}

	token := req.Captcha
	if token == "" {
		token = c.PostForm("grecaptcha[response]")
	}
	if !requireCaptcha(c, token) {
		return
	}

	identifier := req.Username
	if identifier == "" {
		identifier = req.Email
	}

	user, err := models.FindUserForLogin(db.GetDB(), identifier)
	if err != nil && req.Email != "" && req.Email != identifier {
		user, err = models.FindUserForLogin(db.GetDB(), req.Email)
	}

	if err != nil {
		// 不泄露用户是否存在：无论用户不存在还是密码错误，都返回相同的错误信息和状态码
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})

		return
	}

	usersd := &models.UserSecretData{}

	err = db.GetDB().Where("user_id = ?", user.Id).First(usersd).Error
	if err != nil {
		// 不泄露用户敏感数据是否存在，返回与认证失败相同的错误
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})

		return
	}

	if usersd.CheckPassword(req.Password) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})

		return
	}

	if systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false) && !user.IsMailVerified {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "email not verified"})

		return
	}

	if user.IsBan || user.Status != models.UserStatusNormal {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user status abnormal"})

		return
	}

	if !PassSecondFactor(c, user.Id, req.OTPAttempt, true) {
		return
	}

	finishLogin(c, user)
}

func finishLogin(c *gin.Context, user *models.User) {
	rawTokenStr, err := IssueWebSession(c, user)
	if err != nil {
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	log.Debug("User id %d logged in successfully", user.Id)
	c.JSON(http.StatusOK, gin.H{"message": "login success", "token": rawTokenStr}) //nolint:goconst
}

// IssueWebSession creates the same web session cookie and bearer token as password login.
func IssueWebSession(c *gin.Context, user *models.User) (string, error) {
	sessionExpire, err := strconv.Atoi(config.C.SessionExpire)
	if err != nil || sessionExpire <= 0 {
		sessionExpire = 1382400 // 默认 16 天
	}

	permissions, err := models.GetUserPermissions(db.GetDB(), user.Id)
	if err != nil {
		return "", err
	}

	token, rawTokenStr := models.NewAuthToken(user.Id, models.AuthTokenTypeWeb, 0, permissions, nil, sessionExpire, config.C.EncryptedKey)
	if err = db.GetDB().Create(token).Error; err != nil {
		return "", err
	}

	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("app-token", rawTokenStr, sessionExpire, "/", "", secure, true)

	return rawTokenStr, nil
}
