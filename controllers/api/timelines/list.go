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
	var req PublicTimeline

	err := c.BindQuery(&req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": err}) //nolint:goconst

	listObj := controllers.GetIdFromContext(&models.List{}, c, db.GetDB())
	account := controllers.GetAccount(c)

	canView, err := models.ListCanView(listObj, account)
	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, !canView, http.StatusForbidden, gin.H{"error": "forbidden"})

	params := req.ToFeedListParams()
	params.Context = c.Request.Context()
	params.Viewer = account
	statuses, err := listTimeline.GetTimelineContent(listObj, params)
	controllers.AbortIfError(c, err)

	ret, err := serializer.ToStatusesContext(c.Request.Context(), statuses, account)
	controllers.AbortIfError(c, err)

	common.SetListLinks(c, ret, func(s *serializer.Status) string { return s.Id })
	c.JSON(http.StatusOK, ret)
}
