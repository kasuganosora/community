package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"time"
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

	clientIP := c.ClientIP()
	if blocked, retry := middleware.LoginBlocked(identifier, clientIP); blocked {
		c.Header("Retry-After", strconv.Itoa(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many failed login attempts", "retry_after": retry})

		return
	}

	user, err := models.FindUserForLogin(db.GetDB(), identifier)
	if err != nil && req.Email != "" && req.Email != identifier {
		user, err = models.FindUserForLogin(db.GetDB(), req.Email)
	}

	if err != nil {
		// 不泄露用户是否存在：无论用户不存在还是密码错误，都返回相同的错误信息和状态码
		middleware.RecordLoginFailure(identifier, clientIP)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})

		return
	}

	usersd := &models.UserSecretData{}

	err = db.GetDB().Where("user_id = ?", user.Id).First(usersd).Error
	if err != nil {
		// 不泄露用户敏感数据是否存在，返回与认证失败相同的错误
		middleware.RecordLoginFailure(identifier, clientIP)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})

		return
	}

	if usersd.CheckPassword(req.Password) != nil {
		middleware.RecordLoginFailure(identifier, clientIP)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})

		return
	}

	if systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false) && !user.IsMailVerified {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "email not verified"})

		return
	}

	// Suspended accounts may still sign in. AuthMiddleware only lets that
	// session read and file an appeal; pending, rejected, and unverified
	// accounts stay blocked.
	if user.Status != models.UserStatusNormal {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user status abnormal"})

		return
	}

	if !PassSecondFactor(c, user.Id, req.OTPAttempt, true) {
		return
	}

	finishLogin(c, user)
}

func finishLogin(c *gin.Context, user *models.User) {
	rawTokenStr, persistentToken, err := IssueWebSessionPair(c, user, "")
	if err != nil {
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	log.Debug("User id %d logged in successfully", user.Id)
	c.JSON(http.StatusOK, gin.H{
		"message":          "login success", //nolint:goconst
		"token":            rawTokenStr,
		"persistent_token": persistentToken,
	})
}

// SessionExpireSeconds returns the configured web session cookie lifetime in seconds.
func SessionExpireSeconds() int {
	sessionExpire, err := strconv.Atoi(config.C.SessionExpire)
	if err != nil || sessionExpire <= 0 {
		return 1382400 // 默认 16 天
	}

	return sessionExpire
}

// IssueWebSession creates the same web session cookie and bearer token as password login.
func IssueWebSession(c *gin.Context, user *models.User) (string, error) {
	rawTokenStr, _, err := IssueWebSessionPair(c, user, "")

	return rawTokenStr, err
}

// IssueWebSessionPair creates a paired web session cookie token and non-expiring
// encrypted persistent credential for a device. Any existing app-token cookie on
// the request has only its single web session row revoked so multi-account
// persistent credentials for other signed-in accounts stay valid.
func IssueWebSessionPair(c *gin.Context, user *models.User, deviceID string) (string, string, error) {
	sessionExpire := SessionExpireSeconds()
	conn := db.GetDB().WithContext(c.Request.Context())

	permissions, err := models.GetUserPermissions(conn, user.Id)
	if err != nil {
		return "", "", err
	}

	sessionRow, rawTokenStr := models.NewAuthToken(user.Id, models.AuthTokenTypeWeb, 0, permissions, nil, sessionExpire, config.C.EncryptedKey)
	if rawTokenStr == "" {
		return "", "", models.ErrInvalidTokenGeneration
	}

	persistentRow, persistentToken, err := models.NewPersistentToken(user.Id, deviceID, sessionRow.Token, config.C.EncryptedKey)
	if err != nil {
		return "", "", err
	}

	sessionRow.PairHash = persistentRow.Token
	sessionRow.ExternalClient = persistentRow.ExternalClient

	prevCookie, _ := c.Cookie(middleware.AppTokenCookie)

	err = conn.Transaction(func(tx *gorm.DB) error {
		if prevCookie != "" {
			if err := models.RevokeSingleTokenByTokenStr(tx, prevCookie); err != nil {
				return err
			}
		}
		if err := tx.Create(sessionRow).Error; err != nil {
			return err
		}

		return tx.Create(persistentRow).Error
	})
	if err != nil {
		return "", "", err
	}

	if prevCookie != "" {
		middleware.InvalidateTokenCache(prevCookie)
	}

	if err = models.RecordSignIn(conn, user.Id, c.ClientIP(), time.Now()); err != nil {
		return "", "", err
	}

	middleware.SetAppTokenCookie(c, rawTokenStr, sessionExpire)

	return rawTokenStr, persistentToken, nil
}
