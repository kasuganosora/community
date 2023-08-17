package streaming

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/models"
)

type PublicStreamingRequest struct {
	OnlyMedia bool `form:"only_media"`
}

func PublicStreamingBase(c *gin.Context, onlyLocal bool, onlyRemote bool) {
	var req PublicStreamingRequest
	var err error

	if err = c.ShouldBindQuery(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var account *models.Account
	if _account, ok := c.Get("account"); ok {
		account = _account.(*models.Account)
	}

	timeline := &public.PublicTimeline{
		OnlyMedia:  req.OnlyMedia,
		OnlyLocal:  onlyLocal,
		OnlyRemote: onlyRemote,
	}

	procHandle := func(data any) (event, payload string, err error) {
		return serializerStatusEvent(account, data.(string))
	}

	startSseService(c, procHandle, timeline)

	return
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
