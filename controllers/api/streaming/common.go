package streaming

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

var upgrade = websocket.Upgrader{
	CheckOrigin: checkOrigin,
}

func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser client (e.g., native app, curl)
		return true
	}

	allowedOrigins := parseAllowedOrigins(config.C.WebSocketAllowedOrigins)
	if len(allowedOrigins) == 0 {
		// No allowed origins configured, fall back to r.Host
		return r.Host != ""
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	for _, allowed := range allowedOrigins {
		if u.Host == allowed {
			return true
		}
	}

	return false
}

func parseAllowedOrigins(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func serializerStatusEvent(account *models.Account, payload string) (eventType string, payloadStr string, err error) {
	type statusEvent struct {
		Type    string         `json:"type"`
		Payload *models.Status `json:"payload"`
	}

	event := &statusEvent{}
	err = json.Unmarshal([]byte(payload), event)
	if err != nil {
		return
	}

	// 查看扩展字段是否加载全, 如果没有加载全, 则加载全
	err = event.Payload.CheckAndLoadAllExtendFields(db.GetDB())
	if err != nil {
		return
	}

	if event.Type == "delete" {
		// 删除事件, 不需要序列化，只需要返回被删除的status id
		eventType = event.Type
		payloadStr = strconv.FormatUint(event.Payload.Id, 10)
		return
	}

	// 序列化
	sStatus, err := serializer.ToStatus(event.Payload, account)

	if err != nil {
		return
	}

	_payloadStr, _ := json.Marshal(sStatus)
	eventType = event.Type
	payloadStr = string(_payloadStr)

	return
}

func serializerNotificationEvent(account *models.Account, payload string) (eventType string, payloadStr string, err error) {
	type notificationEvent struct {
		Payload *serializer.Notification `json:"payload"`
	}

	event := &notificationEvent{}
	err = json.Unmarshal([]byte(payload), event)
	if err != nil {
		return
	}

	_payloadStr, err := json.Marshal(event.Payload)
	if err != nil {
		return
	}

	eventType = "notification"
	payloadStr = string(_payloadStr)

	return
}

func startSseService(c *gin.Context, procDataHandle func(data any) (event, payload string, err error), timelines ...feed.Timeline) {
	if len(timelines) == 0 {
		panic("streaming: has not any timelines")
	}

	//订阅相关redis频道
	channelName := make([]string, len(timelines))

	for i, timeline := range timelines {
		channelName[i] = feed.GetRedisChannelName(timeline)
	}

	ctx := c.Request.Context()
	topicChan := db.GetRedis().Subscribe(ctx, channelName...)

	defer func() {
		topicChan.Close()
	}()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	flusher, _ := c.Writer.(http.Flusher)
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-topicChan.Channel():
			// 处理数据
			event, payload, err := procDataHandle(msg.Payload)
			if err != nil {
				log.ErrorV(errors.Wrap(err, "streaming: procDataHandle error"))
				continue
			}

			// 清理 payload 中的 \r 和 \n，防止 SSE 帧被拆分
			safeEvent := strings.ReplaceAll(strings.ReplaceAll(event, "\r", " "), "\n", " ")
			safePayload := strings.ReplaceAll(strings.ReplaceAll(payload, "\r", " "), "\n", " ")
			_, _ = c.Writer.Write([]byte("event: " + safeEvent + "\n" + "data: " + safePayload + "\n\n"))
			flusher.Flush()
		}
	}
}

type RedisChannelEvent struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// serializerAnnouncementEvent 序列化公告事件
func serializerAnnouncementEvent(payload json.RawMessage) (eventType string, payloadStr string, err error) {
	// 公告事件直接传递原始 payload
	return "announcement", string(payload), nil
}

// serializerAnnouncementDeleteEvent 序列化公告删除事件
func serializerAnnouncementDeleteEvent(payload json.RawMessage) (eventType string, payloadStr string, err error) {
	// 公告删除事件 payload 应该是公告 ID
	return "announcement.delete", string(payload), nil
}

// serializerAnnouncementReactionEvent 序列化公告反应事件
func serializerAnnouncementReactionEvent(payload json.RawMessage) (eventType string, payloadStr string, err error) {
	return "announcement.reaction", string(payload), nil
}
