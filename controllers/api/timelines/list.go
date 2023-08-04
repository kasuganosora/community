package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"net/http"
	"strconv"

	listTimeline "github.com/kasuganosora/community/feed/list"
)

func List(c *gin.Context) {
	var req TimelineRequest
	if err := c.BindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	listIdStr := c.Param("list_id")
	listId, err := strconv.ParseUint(listIdStr, 10, 64)

	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "list_id must be a number"})
		return
	}

	listObj := &models.List{}

	err = db.GetDB().First(listObj, listId).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "list not found"})
			return
		}

		_ = c.Error(err)
		return
	}

	var statuses models.Statuses

	statuses, err = listTimeline.GetTimelineContent(listObj, req.ToFeedListParams())
	if err != nil {
		_ = c.Error(err)
		return
	}

	var account *models.Account

	if _account, ok := c.Get("account"); ok {
		account = _account.(*models.Account)
	}

	ret, err := serializer.ToStatuses(statuses, account)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, ret)
}
