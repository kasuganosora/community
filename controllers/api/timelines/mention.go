package timelines

import (
	"github.com/gin-gonic/gin"
	mentionTimeline "github.com/kasuganosora/community/feed/mention"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"net/http"
)

func Mention(c *gin.Context) {
	var req TimelineRequest
	if err := c.BindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	account := c.MustGet("account").(*models.Account)

	var statuses models.Statuses

	statuses, err := mentionTimeline.GetTimelineContent(account, req.ToFeedListParams())
	if err != nil {
		_ = c.Error(err)
		return
	}

	ret, err := serializer.ToStatuses(statuses, account)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, ret)
}
