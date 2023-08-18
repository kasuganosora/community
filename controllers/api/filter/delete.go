package filter

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

func Delete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)

	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "id must be a number"})
		return
	}

	currentAccount := c.MustGet("account").(*models.Account)

	filter := &models.Filter{}
	err = db.GetDB().Where("id = ? and account_id = ?", id, currentAccount.Id).First(filter).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "filter not found"})
			return
		}
		_ = c.Error(err)
		return
	}

	err = db.GetDB().Delete(filter).Error

	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(200, gin.H{})
}
