package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func getRealStatus(c *gin.Context) (status *models.Status) {
	status = controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)

	// 判断当前的 status 是否是转发的 status, 如果是则替换成原始的 status
	if status.ReblogId != nil && *status.ReblogId > 0 {
		if status.Reblog == nil {
			c.AbortWithStatusJSON(404, gin.H{"error": "status not found"})
			panic("abort")
		}
		status = status.Reblog
	}

	return
}

func returnFullSingleStatus(c *gin.Context, status *models.Status, currentAccount *models.Account) {
	query := models.StatusLoadAllQuery(db.GetDB().Model(status))
	err := query.First(status, status.Id).Error

	if err != nil {
		_ = c.Error(err)
		panic("abort")
	}

	ret, err := serializer.ToStatus(status, currentAccount)
	if err != nil {
		_ = c.Error(err)
		panic("abort")
	}

	c.JSON(200, ret)
	return
}
