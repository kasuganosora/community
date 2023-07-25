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

func GetHistory(c *gin.Context) {
	statusIdStr, ok := c.Params.Get("id")
	if !ok {
		c.JSON(400, gin.H{"error": "invalid id"})
		return
	}

	statusId, err := strconv.ParseUint(statusIdStr, 10, 64)
	if err != nil && statusId <= 0 {
		c.JSON(400, gin.H{"error": "invalid id"})
		return
	}

	status := &models.Status{}
	dbConn := db.GetDB()
	query := dbConn.Model(status).Where("id = ?", statusId)
	query = statusPreload(query)
	err = query.First(status).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "status not found"})
			return
		}
		err = errors.Wrap(err, "get status failed")
		_ = c.Error(err)
	}

	var currentAccount *models.Account
	_currentAccount, exists := c.Get("account")

	if exists {
		currentAccount = _currentAccount.(*models.Account)
	}

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
	err = dbConn.Model(&models.StatusHistory{}).Preload("Account").Where("status_id = ?", statusId).Order("id desc").Find(&history).Error

	if err != nil {
		err = errors.Wrap(err, "get status history failed")
		_ = c.Error(err)
		return
	}

	historyStatus := make([]*models.StatusHistory, 0, len(history)+1)
	historyStatus = append(historyStatus, &models.StatusHistory{
		StatusId:            statusId,
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
