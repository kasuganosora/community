package streaming

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
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

	return slices.Contains(allowedOrigins, u.Host)
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

	err = json.Unmarshal([]byte(payload), event) //nolint:musttag // struct already has json tags
	if err != nil {
		return eventType, payloadStr, err
	}

	if event.Payload == nil {
		return eventType, payloadStr, errors.New("streaming: status event has no payload")
	}

	// 查看扩展字段是否加载全, 如果没有加载全, 则加载全
	err = event.Payload.CheckAndLoadAllExtendFields(db.GetDB())
	if err != nil {
		return eventType, payloadStr, err
	}

	if event.Type == "delete" { //nolint:goconst
		// 删除事件, 不需要序列化，只需要返回被删除的status id
		eventType = event.Type
		payloadStr = strconv.FormatUint(event.Payload.Id, 10)

		return eventType, payloadStr, err
	}

	// The author is mandatory for clients; skip the event (logged by the
	// caller) instead of serializing a status without an account.
	if event.Payload.Account == nil || event.Payload.Account.Id == 0 {
		return eventType, payloadStr, errors.Wrapf(errEventAccountMissing, "status %d", event.Payload.Id)
	}

	if event.Payload.Reblog != nil && event.Payload.Reblog.Account == nil {
		return eventType, payloadStr, errors.Wrapf(errEventAccountMissing, "reblogged status %d", event.Payload.Reblog.Id)
	}

	// 序列化
	sStatus, err := serializer.ToStatus(event.Payload, account)
	if err != nil {
		return eventType, payloadStr, err
	}

	_payloadStr, err := json.Marshal(sStatus)
	if err != nil {
		return eventType, payloadStr, err
	}
	eventType = event.Type
	payloadStr = string(_payloadStr)

	return eventType, payloadStr, err
}

func serializerNotificationEvent(account *models.Account, payload string) (eventType string, payloadStr string, err error) { //nolint:unparam // param reserved for future use
	type notificationEvent struct {
		Payload *serializer.Notification `json:"payload"`
	}

	event := &notificationEvent{}

	err = json.Unmarshal([]byte(payload), event)
	if err != nil {
		return
	}

	// Mastodon clients require notification.account; the sender may have
	// been deleted since the event was queued.
	if event.Payload == nil || event.Payload.Account == nil {
		return eventType, payloadStr, errors.Wrap(errEventAccountMissing, "notification")
	}

	_payloadStr, err := json.Marshal(event.Payload)
	if err != nil {
		return
	}

	eventType = "notification" //nolint:goconst
	payloadStr = string(_payloadStr)

	return
}

func startSseService(c *gin.Context, procDataHandle func(data any) (event, payload string, err error), timelines ...feed.Timeline) {
	if len(timelines) == 0 {
		panic("streaming: has not any timelines")
	}

	// 订阅相关redis频道
	channelName := make([]string, len(timelines))

	for i, timeline := range timelines {
		channelName[i] = feed.GetRedisChannelName(timeline)
	}

	ctx := c.Request.Context()
	topicChan := db.GetRedis().Subscribe(ctx, channelName...)

	defer func() {
		_ = topicChan.Close()
	}()

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.AbortWithStatusJSON(500, gin.H{"error": "streaming not supported"})

		return
	}

	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-topicChan.Channel():
			// 处理数据
			event, payload, err := procDataHandle(msg.Payload)
			if err != nil {
				logEventError(err)

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

// serializerAnnouncementEvent 序列化公告事件.
func serializerAnnouncementEvent(payload json.RawMessage) (eventType string, payloadStr string, err error) {
	// 公告事件直接传递原始 payload
	return "announcement", string(payload), nil
}

// serializerAnnouncementDeleteEvent 序列化公告删除事件.
func serializerAnnouncementDeleteEvent(payload json.RawMessage) (eventType string, payloadStr string, err error) {
	// 公告删除事件 payload 应该是公告 ID
	return "announcement.delete", string(payload), nil
}

// serializerAnnouncementReactionEvent 序列化公告反应事件.
func serializerAnnouncementReactionEvent(payload json.RawMessage) (eventType string, payloadStr string, err error) {
	return "announcement.reaction", string(payload), nil
}

var errEventAccountMissing = errors.New("streaming: status event account missing")

// logEventError logs a failed event serialization. A status whose author (or
// reblogged author) row no longer exists is expected: accounts are
// soft-deleted (models.Account.DeletedAt) or purged while their statuses are
// still queued in redis feeds, so such events are skipped at debug level
// (the message carries the status id) instead of flooding the error log.
func logEventError(err error) {
	if errors.Is(err, errEventAccountMissing) {
		log.Debug("streaming: skipping event: %v", err)

		return
	}

	log.ErrorV(errors.Wrap(err, "streaming: event error"))
}
