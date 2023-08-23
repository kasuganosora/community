package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func Show(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)
	account := controllers.GetAccount(c)

	var err error
	if status.ReblogId != nil {
		reblog := &models.Status{}
		query := db.GetDB().Model(&models.Status{})
		query = models.StatusLoadAllQuery(query)
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
			canview, err := status.CanViewEx(db.GetDB(), account)
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

	returnFullSingleStatus(c, status, account)
}
