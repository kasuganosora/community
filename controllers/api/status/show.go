package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

func Show(c *gin.Context) {
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

	if statusId == 0 {
		c.JSON(400, gin.H{"error": "status id is required"})
		return
	}

	account, _ := c.Get("account")

	status := &models.Status{}
	query := db.GetDB().Model(&models.Status{})
	query = statusPreload(query)
	err = query.First(status, statusId).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "status not found"})
			return
		} else {
			_ = c.Error(err)
			return
		}
	}

	if status.ReblogId != nil {
		reblog := &models.Status{}
		query := db.GetDB().Model(&models.Status{})
		query = statusPreload(query)
		err = query.First(reblog, *status.ReblogId).Error

		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(404, gin.H{"error": "status not found"})
				return
			} else {
				_ = c.Error(err)
				return
			}
		}

		status.Reblog = reblog
	}

	// check permission
	// 如果是私有的，只有自己和关注者/提及的人可以看到
	if status.Visibility == models.StatusVisibilityPrivate || status.Visibility == models.StatusVisibilityDirect {
		if account == nil {
			c.JSON(403, gin.H{"error": "has no permission"})
			return
		} else {
			canview, err := status.CanViewEx(db.GetDB(), account.(*models.Account))
			if err != nil {
				_ = c.Error(err)
				return
			}

			if !canview {
				c.JSON(403, gin.H{"error": "has no permission"})
				return
			}
		}

	}

	var currentAccount *models.Account
	if account != nil {
		currentAccount = account.(*models.Account)
	}

	returnFullSingleStatus(c, status, currentAccount)
}
