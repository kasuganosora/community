package streaming

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

func UserStreaming(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)
	timeline := &home.HomeTimeline{account}

	procHandle := func(data any) (event, payload string, err error) {
		type RedisChannelEvent struct {
			Type string `json:"type"`
		}

		ev := &RedisChannelEvent{}
		err = json.Unmarshal([]byte(data.(string)), ev)
		if err != nil {
			return
		}

		switch ev.Type {
		case "update", "delete", "status.update":
			return serializerStatusEvent(account, data.(string))
		case "notification":
			return serializerNotificationEvent(account, data.(string))
			// TODO filters_changed, announcement, announcement.delete,announcement.reaction
		default:
			err = errors.New("unknown event type")
			return
		}
	}

	startSseService(c, procHandle, timeline)
}
