package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	mentionTimeline "github.com/kasuganosora/community/feed/mention"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"net/http"
)

func Mention(c *gin.Context) {
	var req TimelineRequest
	err := c.BindQuery(&req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": err})

	account := controllers.MustGetAccount(c)

	var statuses models.Statuses

	statuses, err = mentionTimeline.GetTimelineContent(account, req.ToFeedListParams())
	controllers.AbortIfError(c, err)

	ret, err := serializer.ToStatuses(statuses, account)
	controllers.AbortIfError(c, err)

	c.JSON(http.StatusOK, ret)
}
