package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

func GetHistory(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)

	dbConn := db.GetDB()
	currentAccount := controllers.GetAccount(c)

	canView, err := status.CanViewEx(dbConn, currentAccount)

	if err != nil {
		err = errors.Wrap(err, "can view status")
		_ = c.Error(err)
		return
	}

	if !canView {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}

	history := []*models.StatusHistory{}
	err = dbConn.Model(&models.StatusHistory{}).Preload("Account").Where("status_id = ?", status.Id).Order("id desc").Find(&history).Error

	if err != nil {
		err = errors.Wrap(err, "get status history failed")
		_ = c.Error(err)
		return
	}

	historyStatus := make([]*models.StatusHistory, 0, len(history)+1)
	historyStatus = append(historyStatus, &models.StatusHistory{
		StatusId:            status.Id,
		AccountId:           status.AccountId,
		Content:             status.Content.Content,
		SpoilerText:         status.Content.SpoilerText,
		Sensitive:           status.Sensitive,
		MediaAttachmentsIds: status.Attachments.GetIds(),
		EmojisIds:           status.Emojis.GetIds(),
		CreatedAt:           status.CreatedAt,
	})

	historyStatus = append(historyStatus, history...)

	ret, err := serializer.ToHistoryStatuses(dbConn, historyStatus)

	if err != nil {
		err = errors.Wrap(err, "serialize status history failed")
		_ = c.Error(err)
		return
	}

	c.JSON(200, ret)
}
