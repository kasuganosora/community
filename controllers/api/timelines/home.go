package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
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
	statuses, err := home.GetTimeLineContent(account, req.ToFeedListParams())
	controllers.AbortIfError(c, err)

	ret, err := serializer.ToStatuses(statuses, account)
	controllers.AbortIfError(c, err)

	c.JSON(http.StatusOK, ret)
}
