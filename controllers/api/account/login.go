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
	Username string `form:"username"`
	Email    string `form:"email"`
	Password string `binding:"required" form:"password"`
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

	user := &models.User{}

	var emailHash string
	if req.Email != "" {
		emailHash = models.GetEmailHash(req.Email)
	}

	err := db.GetDB().Where("username = ? or email_hash = ?", req.Username, emailHash).First(user).Error
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

	if user.Status != models.UserStatusNormal {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user status abnormal"})

		return
	}

	sessionExpire, err := strconv.Atoi(config.C.SessionExpire)
	if err != nil || sessionExpire <= 0 {
		sessionExpire = 1382400 // 默认 16 天
	}

	// 取出当前用户所拥有的权限位
	permissions, err := models.GetUserPermissions(db.GetDB(), user.Id)
	if err != nil {
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	// 生成登录态 Token
	token, rawTokenStr := models.NewAuthToken(user.Id, models.AuthTokenTypeWeb, 0, permissions, nil, sessionExpire, config.C.EncryptedKey)

	// 保存 Token
	err = db.GetDB().Create(token).Error
	if err != nil {
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	log.Debug("User id %d logged in successfully", user.Id)

	// 设置 Cookie
	// Secure 标志根据请求是否为 HTTPS 动态判断
	// SameSite=Lax 防止跨站请求伪造（CSRF）
	secure := c.Request.TLS != nil
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("app-token", rawTokenStr, sessionExpire, "/", "", secure, true)
	// 返回首页
	c.JSON(http.StatusOK, gin.H{"message": "login success", "token": rawTokenStr}) //nolint:goconst
}
