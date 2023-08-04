package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed"
	tagtimeline "github.com/kasuganosora/community/feed/tag"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/utils"
	"net/http"
)

type TagTimelineRequest struct {
	TimelineRequest
	OnlyMedia bool   `form:"only_media"`
	Local     string `form:"local"`
	Remote    string `form:"remote"`
}

func (req *TagTimelineRequest) ToFeedListParams() *feed.ListParam {
	ret := req.TimelineRequest.ToFeedListParams()
	ret.Other["only_media"] = utils.ToBool(req.OnlyMedia)
	ret.Other["local"] = utils.ToBool(req.Local)
	ret.Other["remote"] = utils.ToBool(req.Remote)
	return ret
}

func Tag(c *gin.Context) {
	var req TagTimelineRequest
	if err := c.BindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tag := c.Param("tag")

	var account *models.Account

	if _account, ok := c.Get("account"); ok {
		account = _account.(*models.Account)
	}

	var statuses models.Statuses

	statuses, err := tagtimeline.GetTimelineContent(tag, account, req.ToFeedListParams())
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
