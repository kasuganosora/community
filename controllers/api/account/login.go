package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"net/http"
	"strconv"
)

type AuthRequest struct {
	Username string `form:"username"`
	Email    string `form:"email"`
	Password string `form:"password" binding:"required"`
}

func Login(c *gin.Context) {
	req := &AuthRequest{}

	if err := c.ShouldBind(req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user name or password error"})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	usersd := &models.UserSecretData{}
	err = db.GetDB().Where("user_id = ?", user.Id).First(usersd).Error

	if err != nil {
		err = errors.WithMessagef(err, "UserSecretData NotFound user_id: %d", user.Id)
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid user record"})
		return
	}

	if models.CompareHashAndPassword(usersd.Password, req.Password, usersd.Salt) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid password"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 生成登录态 Token
	token, rawTokenStr := models.NewAuthToken(user.Id, models.AuthTokenTypeWeb, 0, permissions, nil, sessionExpire, config.C.EncryptedKey)

	// 保存 Token
	err = db.GetDB().Create(token).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Debug("User id %d, token is %s", user.Id, rawTokenStr)

	// 设置 Cookie
	c.SetCookie("app-token", rawTokenStr, sessionExpire, "/", "", false, true)
	// 返回首页
	c.JSON(http.StatusOK, gin.H{"message": "login success", "token": rawTokenStr})
}
