package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/utils"
	"net/http"
)

type PublicTimeline struct {
	TimelineRequest
	Local     string `form:"local"`
	Remote    string `form:"remote"`
	OnlyMedia string `form:"only_media"`
}

func (req *PublicTimeline) ToFeedListParams() *feed.ListParam {
	ret := req.TimelineRequest.ToFeedListParams()
	ret.Other["local"] = utils.ToBool(req.Local)
	ret.Other["remote"] = utils.ToBool(req.Remote)
	ret.Other["only_media"] = utils.ToBool(req.OnlyMedia)
	return ret
}

func Public(c *gin.Context) {
	var req PublicTimeline
	if err := c.BindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var statuses models.Statuses
	var err error

	if utils.ToBool(req.OnlyMedia) {
		statuses, err = public.GetTimelineContentMedia(req.ToFeedListParams())
	} else {
		statuses, err = public.GetTimelineContentAll(req.ToFeedListParams())
	}

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
