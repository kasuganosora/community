package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/serializer"
	"net/http"
)

func Home(c *gin.Context) {
	var req TimelineRequest
	if err := c.Bind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	account := controllers.GetAccount(c)
	params := req.ToFeedListParams()
	params.Context = c.Request.Context()
	statuses, err := home.GetTimeLineContent(account, params)
	controllers.AbortIfError(c, err)

	ret, err := serializer.ToStatusesContext(c.Request.Context(), statuses, account)
	controllers.AbortIfError(c, err)

	common.SetListLinks(c, ret, func(s *serializer.Status) string { return s.Id })
	c.JSON(http.StatusOK, ret)
}
