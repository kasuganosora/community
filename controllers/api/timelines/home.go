package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"net/http"
)

func Home(c *gin.Context) {
	var req TimeLineRequest
	if err := c.Bind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	account := c.MustGet("account").(*models.Account)
	statuses, err := home.GetTimeLineContent(account, &feed.ListParam{
		MaxId:   req.MaxId,
		MinId:   req.MinId,
		SinceId: req.SinceId,
		Limit:   req.Limit,
	})

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
