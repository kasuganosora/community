package streaming

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed/notification"
	"github.com/kasuganosora/community/models"
)

func NotificationStreaming(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)
	timeline := &notification.NotificationTimeline{account}

	procHandle := func(data any) (event, payload string, err error) {
		return serializerNotificationEvent(account, data.(string))
	}

	startSseService(c, procHandle, timeline)
}
