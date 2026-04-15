package streaming

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/feed/public"
)

type PublicStreamingRequest struct {
	OnlyMedia bool `form:"only_media"`
}

func PublicStreamingBase(c *gin.Context, onlyLocal bool, onlyRemote bool) {
	var req PublicStreamingRequest
	err := c.ShouldBindQuery(&req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": err})

	account := controllers.GetAccount(c)

	timeline := &public.PublicTimeline{
		OnlyMedia:  req.OnlyMedia,
		OnlyLocal:  onlyLocal,
		OnlyRemote: onlyRemote,
	}

	procHandle := func(data any) (event, payload string, err error) {
		return serializerStatusEvent(account, data.(string))
	}

	startSseService(c, procHandle, timeline)
}

func PublicStreamingAll(c *gin.Context) {
	PublicStreamingBase(c, false, false)
}

func PublicStreamingLocal(c *gin.Context) {
	PublicStreamingBase(c, true, false)
}

func PublicStreamingRemote(c *gin.Context) {
	PublicStreamingBase(c, false, true)
}
