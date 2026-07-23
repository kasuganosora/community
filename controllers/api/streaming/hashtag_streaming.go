package streaming

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/feed/tag"
)

type HashtagStreamingRequest struct {
	OnlyMedia bool   `form:"only_media"`
	Tag       string `binding:"required" form:"tag"`
}

func HashtagStreamingBase(c *gin.Context, onlyLocal bool, onlyRemote bool) {
	var (
		req HashtagStreamingRequest
		err error
	)

	if err = c.ShouldBindQuery(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	account := controllers.GetAccount(c)

	timeline := &tag.TagTimeline{
		Tag:        req.Tag,
		OnlyMedia:  req.OnlyMedia,
		OnlyLocal:  onlyLocal,
		OnlyRemote: onlyRemote,
	}

	procHandle := func(data any) (event, payload string, err error) {
		return serializerStatusEvent(account, data.(string)) //nolint:forcetypeassert
	}

	startSseService(c, procHandle, timeline)
}

func HashtagStreamingAll(c *gin.Context) {
	HashtagStreamingBase(c, false, false)
}

func HashtagStreamingLocal(c *gin.Context) {
	HashtagStreamingBase(c, true, false)
}

func HashtagStreamingRemote(c *gin.Context) {
	HashtagStreamingBase(c, false, true)
}
