package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"net/http"
)

type VerifyEmailRequest struct {
	Code string `form:"code" binding:"required"`
}

func VerifyEmail(c *gin.Context) {
	req := &VerifyEmailRequest{}

	if err := c.ShouldBindQuery(req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	if req.Code == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "code is required"})
		return
	}

	user := &models.User{}
	err := db.GetDB().Where("mail_verify_code = ?", req.Code).First(user).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "invalid code"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	user.MailVerifyCode = nil
	user.IsMailVerified = true

	err = db.GetDB().Save(user).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 跳转到首页
	c.Redirect(http.StatusFound, "/")
}
