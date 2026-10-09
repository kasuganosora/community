package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/db"
	listTimeline "github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"net/http"
)

func List(c *gin.Context) {
	var req TimelineRequest

	err := c.BindQuery(&req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": err}) //nolint:goconst

	listObj := controllers.GetIdFromContext(&models.List{}, c, db.GetDB())

	var statuses models.Statuses

	params := req.ToFeedListParams()
	params.Context = c.Request.Context()
	params.Viewer = controllers.GetAccount(c)
	statuses, err = listTimeline.GetTimelineContent(listObj, params)
	if err != nil {
		_ = c.Error(err)

		return
	}

	account := controllers.GetAccount(c)

	ret, err := serializer.ToStatusesContext(c.Request.Context(), statuses, account)
	if err != nil {
		_ = c.Error(err)

		return
	}

	common.SetListLinks(c, ret, func(s *serializer.Status) string { return s.Id })
	c.JSON(http.StatusOK, ret)
}
