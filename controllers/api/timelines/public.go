package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/controllers/api/common"
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

	var account *models.Account

	if _account, ok := c.Get("account"); ok {
		account = _account.(*models.Account) //nolint:forcetypeassert
	}

	params := req.ToFeedListParams()
	params.Context = c.Request.Context()
	params.Viewer = account

	if utils.ToBool(req.OnlyMedia) {
		statuses, err = public.GetTimelineContentMedia(params)
	} else {
		statuses, err = public.GetTimelineContentAll(params)
	}

	controllers.AbortIfError(c, err)

	ret, err := serializer.ToStatusesContext(c.Request.Context(), statuses, account)
	controllers.AbortIfError(c, err)

	common.SetListLinks(c, ret, func(s *serializer.Status) string { return s.Id })
	c.JSON(http.StatusOK, ret)
}
