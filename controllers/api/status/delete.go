package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

func Delete(c *gin.Context) {
	currentAccount := c.MustGet("account").(*models.Account)
	statusIdStr := c.Param("id")

	if statusIdStr == "" {
		c.JSON(400, gin.H{"error": "status id is required"})
		return
	}

	statusId, err := strconv.ParseUint(statusIdStr, 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "status id must be numeric"})
		return
	}

	status := &models.Status{}

	err = db.GetDB().First(status, statusId).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "status not found"})
			return
		} else {
			_ = c.Error(err)
			return
		}
	}

	// check permission
	if status.AccountId != currentAccount.Id {
		c.JSON(403, gin.H{"error": "has no permission"})
		return
	}

	if status.ReblogId != nil {
		c.JSON(400, gin.H{"error": "can't delete reblog status"})
		return
	}

	err = db.GetDB().Delete(status).Error
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.Data(204, "application/json", nil)
}
