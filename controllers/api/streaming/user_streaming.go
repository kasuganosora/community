package streaming

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/log"
)

func UserStreaming(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	timeline := &home.HomeTimeline{Account: account}

	procHandle := func(data any) (event, payload string, err error) {
		type RedisChannelEvent struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}

		ev := &RedisChannelEvent{}

		err = json.Unmarshal([]byte(data.(string)), ev) //nolint:forcetypeassert
		if err != nil {
			return event, payload, err
		}

		switch ev.Type {
		case "update", "delete", "status.update": //nolint:goconst
			return serializerStatusEvent(account, data.(string)) //nolint:forcetypeassert
		case "notification": //nolint:goconst
			return serializerNotificationEvent(account, data.(string)) //nolint:forcetypeassert
		case "filters_changed": //nolint:goconst
			// 过滤器变更事件 - 通知客户端重新获取过滤器
			return "filters_changed", "{}", nil
		case "announcement":
			// 公告创建事件
			return serializerAnnouncementEvent(ev.Payload)
		case "announcement.delete":
			// 公告删除事件
			return serializerAnnouncementDeleteEvent(ev.Payload)
		case "announcement.reaction":
			// 公告表情反应事件
			return serializerAnnouncementReactionEvent(ev.Payload)
		default:
			// 对于未知的事件类型，记录日志但不返回错误
			log.Debug("Unknown streaming event type: %s", ev.Type)

			return ev.Type, string(ev.Payload), nil
		}
	}

	startSseService(c, procHandle, timeline)
}
