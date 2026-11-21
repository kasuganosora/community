package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func GetHistory(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)

	dbConn := db.GetDB()
	currentAccount := controllers.GetAccount(c)

	abortUnlessVisible(c, dbConn, status, currentAccount)

	history := []*models.StatusHistory{}
	err := dbConn.Model(&models.StatusHistory{}).Preload("Account").Where("status_id = ?", status.Id).Order("id ASC").Find(&history).Error

	controllers.AbortIfError(c, err)

	current := &models.StatusHistory{
		StatusId:            status.Id,
		AccountId:           status.AccountId,
		Account:             status.Account,
		Content:             status.Content.Content,
		SpoilerText:         status.Content.SpoilerText,
		Sensitive:           status.Sensitive,
		MediaAttachmentsIds: status.Attachments.GetIds(),
		EmojisIds:           status.Emojis.GetIds(),
		CreatedAt:           status.CreatedAt,
	}
	if status.Poll != nil {
		current.PollOptions = status.Poll.Options.GetNames()
	}
	if len(history) > 0 && status.UpdatedAt != nil {
		current.CreatedAt = status.UpdatedAt
	}

	ret, err := serializer.ToHistoryStatuses(dbConn, append(history, current))

	controllers.AbortIfError(c, err)

	c.JSON(200, ret)
}
