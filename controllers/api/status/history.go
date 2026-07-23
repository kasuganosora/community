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

	canView, err := status.CanViewEx(dbConn, currentAccount)

	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, !canView, 403, gin.H{"error": "forbidden"}) //nolint:goconst

	history := []*models.StatusHistory{}
	err = dbConn.Model(&models.StatusHistory{}).Preload("Account").Where("status_id = ?", status.Id).Order("id desc").Find(&history).Error

	controllers.AbortIfError(c, err)

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

	controllers.AbortIfError(c, err)

	c.JSON(200, ret)
}
