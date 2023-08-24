package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	listTimeline "github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"net/http"
)

func List(c *gin.Context) {
	var req TimelineRequest
	err := c.BindQuery(&req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": err})

	listObj := controllers.GetIdFromContext(&models.List{}, c, db.GetDB())

	var statuses models.Statuses

	statuses, err = listTimeline.GetTimelineContent(listObj, req.ToFeedListParams())
	if err != nil {
		_ = c.Error(err)
		return
	}

	account := controllers.GetAccount(c)

	ret, err := serializer.ToStatuses(statuses, account)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusOK, ret)
}
