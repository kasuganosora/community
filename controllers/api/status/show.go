package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
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
	followCheckMap := make(map[uint64]*models.Follow)
	if status.Visibility == models.StatusVisibilityPrivate || status.Visibility == models.StatusVisibilityDirect {
		if account == nil {
			c.JSON(403, gin.H{"error": "has no permission"})
			return
		}

		currentAccount := account.(*models.Account)

		if status.AccountId != currentAccount.Id {
			// check follow
			follow := &models.Follow{}
			err = db.GetDB().Where("account_id = ? AND follow_id = ?", currentAccount.Id, status.AccountId).First(follow).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					err = nil
				} else {
					_ = c.Error(err)
					return
				}

				if follow.Id > 0 {
					followCheckMap[follow.FollowId] = follow
				}
			}

			if status.CanView(currentAccount, followCheckMap) == false {
				c.JSON(403, gin.H{"error": "has no permission"})
				return
			}
		}
	}

	var acc *models.Account
	if account != nil {
		acc = account.(*models.Account)
	}

	ret, err := serializer.ToStatus(status, acc)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(200, ret)
}

func statusPreload(query *gorm.DB) *gorm.DB {
	return query.Preload("Account").
		Preload("Content").
		Preload("Application").
		Preload("Attachments").
		Preload("ReplyAccount").
		Preload("Emojis").
		Preload("ToAccounts").
		Preload("Poll").
		Preload("Poll.Options").
		Preload("Poll.Options").
		Preload("Tags")
}
