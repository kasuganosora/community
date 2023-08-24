package streaming

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/feed/notification"
)

func NotificationStreaming(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	timeline := &notification.NotificationTimeline{account}

	procHandle := func(data any) (event, payload string, err error) {
		return serializerNotificationEvent(account, data.(string))
	}

	startSseService(c, procHandle, timeline)
}
