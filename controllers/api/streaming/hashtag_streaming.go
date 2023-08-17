package streaming

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed/tag"
	"github.com/kasuganosora/community/models"
)

type HashtagStreamingRequest struct {
	OnlyMedia bool   `form:"only_media"`
	Tag       string `form:"tag" binding:"required"`
}

func HashtagStreamingBase(c *gin.Context, onlyLocal bool, onlyRemote bool) {
	var req HashtagStreamingRequest
	var err error

	if err = c.ShouldBindQuery(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var account *models.Account
	if _account, ok := c.Get("account"); ok {
		account = _account.(*models.Account)
	}

	timeline := &tag.TagTimeline{
		Tag:        req.Tag,
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

func HashtagStreamingAll(c *gin.Context) {
	HashtagStreamingBase(c, false, false)
}

func HashtagStreamingLocal(c *gin.Context) {
	HashtagStreamingBase(c, true, false)
}

func HashtagStreamingRemote(c *gin.Context) {
	HashtagStreamingBase(c, false, true)
}
