package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
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
	emailHash := models.GetEmailHash(req.Email)
	err := db.GetDB().Where("username = ? or email_hash = ?", req.Username, emailHash).First(user).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}

	usersd := &models.UserSecretData{}
	err = db.GetDB().Where("user_id = ?", user.Id).First(usersd).Error

	if err != nil {
		err = errors.WithMessagef(err, "UserSecretData NotFound user_id: %d", user.Id)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid user record"})
		return
	}

	passwordEncrypt, err := models.EncryptPassword(req.Password, usersd.Salt)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if passwordEncrypt != usersd.Password {
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

	sessionExpire, _ := strconv.Atoi(config.C.SessionExpire)

	// 取出当前用户所拥有的权限位
	permissions, err := models.GetUserPermissions(db.GetDB(), user.Id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 生成登录态 Token
	token := models.NewAuthToken(user.Id, models.AuthTokenTypeWeb, permissions, nil, sessionExpire, config.C.EncryptedKey)

	// 保存 Token
	err = db.GetDB().Create(token).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 设置 Cookie
	c.SetCookie("token", token.Token, sessionExpire, "/", "", false, true)
	// 返回首页
	c.Redirect(http.StatusFound, "/")
}
