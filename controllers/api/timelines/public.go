package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
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

	err := c.BindQuery(&req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": err}) //nolint:goconst

	var statuses models.Statuses

	if utils.ToBool(req.OnlyMedia) {
		statuses, err = public.GetTimelineContentMedia(req.ToFeedListParams())
	} else {
		statuses, err = public.GetTimelineContentAll(req.ToFeedListParams())
	}

	controllers.AbortIfError(c, err)

	var account *models.Account

	if _account, ok := c.Get("account"); ok {
		account = _account.(*models.Account) //nolint:forcetypeassert
	}

	ret, err := serializer.ToStatuses(statuses, account)
	controllers.AbortIfError(c, err)

	c.JSON(http.StatusOK, ret)
}
