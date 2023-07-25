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

func getRealStatus(c *gin.Context) (status *models.Status, abort bool) {
	status, abort = getStatusByIdPram(c)
	if abort {
		return
	}

	// 判断当前的 status 是否是转发的 status, 如果是则替换成原始的 status
	if status.ReblogId != nil && *status.ReblogId > 0 {
		if status.Reblog == nil {
			c.JSON(404, gin.H{"error": "status not found"})
			abort = true
			return
		}
		status = status.Reblog
	}

	return
}

func getStatusByIdPram(c *gin.Context) (status *models.Status, abort bool) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid id"})
		abort = true
		return
	}

	dbConn := db.GetDB()

	status = &models.Status{}
	err = dbConn.Model(status).Preload("Reblog").First(status, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "status not found"})
			abort = true
			return
		}
		err = errors.Wrap(err, "get status")
		_ = c.Error(err)
		abort = true
		return
	}

	return
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
		Preload("Tags").
		Preload("Reblog").
		Preload("Reblog.Account").
		Preload("Reblog.Content").
		Preload("Reblog.Application").
		Preload("Reblog.Attachments").
		Preload("Reblog.ReplyAccount").
		Preload("Reblog.Emojis").
		Preload("Reblog.ToAccounts").
		Preload("Reblog.Poll").
		Preload("Reblog.Poll.Options").
		Preload("Reblog.Poll.Options").
		Preload("Reblog.Tags")
}

func returnFullSingleStatus(c *gin.Context, status *models.Status, currentAccount *models.Account) (abort bool) {
	query := statusPreload(db.GetDB().Model(status))
	err := query.First(status, status.Id).Error

	if err != nil {
		_ = c.Error(err)
		abort = true
		return
	}

	ret, err := serializer.ToStatus(status, currentAccount)
	if err != nil {
		_ = c.Error(err)
		abort = true
		return
	}

	c.JSON(200, ret)
	return
}
