package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type VerifyEmailRequest struct {
	Token string `binding:"required" form:"token"`
}

func VerifyEmail(c *gin.Context) {
	req := &VerifyEmailRequest{}

	if err := c.ShouldBindQuery(req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	if req.Token == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "token is required"})

		return
	}

	var user *models.User

	// 解析出token里的用户id
	tokenParts := strings.Split(req.Token, "-")
	if len(tokenParts) < 3 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid token"}) //nolint:goconst

		return
	}

	userId, err := strconv.ParseUint(tokenParts[1], 10, 64)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid token"})

		return
	}

	token := &VerifyEmailToken{}

	err = models.GetUserMeta(db.GetDB(), userId, "verify_email_token", token)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invalid token"})

		return
	}

	if token.Token != req.Token {
		c.JSON(http.StatusNotFound, gin.H{"error": "invalid token"})

		return
	}

	if token.ExpireAt < time.Now().Unix() {
		c.JSON(http.StatusNotFound, gin.H{"error": "invalid token"})

		return
	}

	user = &models.User{}

	err = db.GetDB().Preload("MailVerifyCode").Where("id = ?", userId).First(user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "invalid code"})

			return
		}

		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	user.IsMailVerified = true

	err = db.GetDB().Save(user).Error
	if err != nil {
		log.ErrorV(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	// 跳转到首页
	c.Redirect(http.StatusFound, "/")
}
