package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

type ChangePasswordReq struct {
	OldPassword string `form:"old_password" binding:"required"`
	NewPassword string `form:"new_password" binding:"required"`
}

func ChangePassword(c *gin.Context) {
	var err error
	req := &ChangePasswordReq{}

	if err = c.ShouldBind(req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	user := c.MustGet("user").(*models.User)

	userSecret := &models.UserSecretData{}

	err = db.GetDB().Model(user).Association("UserSecret").Find(&userSecret)

	if err != nil {
		err = errors.Wrap(err, "get user secret failed")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}

	if err = models.CompareHashAndPassword(userSecret.Password, req.OldPassword, userSecret.Salt); err != nil {
		err = nil
		c.JSON(400, gin.H{"error": "old password is wrong"})
		return
	}

	newPassword, err := models.EncryptPassword(req.NewPassword, userSecret.Salt)

	if err != nil {
		err = errors.Wrap(err, "encrypt password failed")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "encrypt password failed"})
		return
	}

	err = db.GetDB().Model(userSecret).Where("user_id =?", userSecret.UserId).Update("password", newPassword).Error

	if err != nil {
		err = errors.Wrap(err, "update password failed")
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "update password failed"})
		return
	}

	c.JSON(200, gin.H{"message": "ok"})
}
