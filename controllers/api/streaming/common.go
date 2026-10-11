package streaming

import (
	"context"
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
	"gorm.io/gorm"
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

func serializerStatusEvent(account *models.Account, payload string, channel ...string) (eventType string, payloadStr string, err error) {
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

	if event.Payload.Id == 0 {
		return "", "", errEventNotVisible
	}
	if event.Type == "delete" {
		// Tombstones carry only an ID; the deleted SQL row need not still exist.
		return "delete", strconv.FormatUint(event.Payload.Id, 10), nil
	}
	if event.Type != "update" && event.Type != "status.update" {
		return "", "", errors.New("streaming: unsupported status event")
	}
	account, err = currentStreamingAccount(account)
	if err != nil {
		return "", "", err
	}
	var current models.Status
	statuses := models.Statuses{}
	contextName := ""
	if len(channel) > 0 {
		statuses, contextName, err = currentTimelineStatus(account, event.Payload.Id, channel[0])
		if err != nil {
			return "", "", err
		}
	} else {
		// Non-timeline callers still reload current SQL and authorize embedded objects.
		if err = models.StatusLoadAllQuery(db.GetDB()).First(&current, event.Payload.Id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", "", errors.Wrap(errEventNotVisible, "status no longer exists")
			}
			return "", "", err
		}
		if current.Account == nil {
			return "", "", errors.Wrap(errEventAccountMissing, "status author")
		}
		statuses = models.Statuses{&current}
	}
	rows, err := serializer.ToStatusesContext(models.WithFilterContext(context.Background(), contextName), statuses, account)
	if err != nil {
		return "", "", err
	}
	if len(rows) != 1 {
		return "", "", errEventNotVisible
	}
	sStatus := rows[0]

	_payloadStr, err := json.Marshal(sStatus)
	if err != nil {
		return eventType, payloadStr, err
	}
	eventType = event.Type
	payloadStr = string(_payloadStr)

	return eventType, payloadStr, err
}

// currentStreamingAccount invalidates long-lived streams after account removal
// or suspension instead of trusting the account snapshot captured at handshake.
func currentStreamingAccount(account *models.Account) (*models.Account, error) {
	if account == nil {
		return nil, nil
	}
	var current models.Account
	if err := db.GetDB().First(&current, account.Id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errEventNotVisible
		}
		return nil, err
	}
	if current.IsBan {
		return nil, errEventNotVisible
	}
	return &current, nil
}

func serializerNotificationEvent(account *models.Account, payload string) (string, string, error) {
	var event struct {
		Payload *serializer.Notification `json:"payload"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return "", "", err
	}
	if event.Payload == nil {
		return "", "", errEventAccountMissing
	}
	account, err := currentStreamingAccount(account)
	if err != nil {
		return "", "", err
	}
	if account == nil {
		return "", "", errEventNotVisible
	}
	id, err := strconv.ParseUint(event.Payload.Id, 10, 64)
	if err != nil || id == 0 {
		return "", "", errEventNotVisible
	}
	var current models.Notification
	if err := db.GetDB().Where("id = ? AND account_id = ?", id, account.Id).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", "", errEventNotVisible
		}
		return "", "", err
	}
	// The batch serializer also filters embedded reblogs/quotes and incomplete
	// notifications, using current SQL rather than queued serialized text.
	rows, err := serializer.ToNotifications([]*models.Notification{&current}, account)
	if err != nil {
		return "", "", err
	}
	if len(rows) != 1 {
		return "", "", errEventNotVisible
	}
	encoded, err := json.Marshal(rows[0])
	return "notification", string(encoded), err
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
	value, _ := c.Get("account")
	viewer, _ := value.(*models.Account)
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
		case msg, open := <-topicChan.Channel():
			if !open || msg == nil {
				return
			}
			// List ownership/following can be revoked while this SSE connection is open.
			if _, _, err := streamForChannel(viewer, msg.Channel); err != nil {
				logEventError(err)
				continue
			}
			var event, payload string
			var err error
			if isStatusStreamEvent(msg.Payload) {
				event, payload, err = serializerStatusEvent(viewer, msg.Payload, msg.Channel)
			} else {
				event, payload, err = procDataHandle(msg.Payload)
			}
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

var errEventNotVisible = errors.New("streaming: event is no longer visible")

var errEventAccountMissing = errors.New("streaming: status event account missing")

// logEventError logs a failed event serialization. A status whose author (or
// reblogged author) row no longer exists is expected: accounts are
// soft-deleted (models.Account.DeletedAt) or purged while their statuses are
// still queued in redis feeds, so such events are skipped at debug level
// (the message carries the status id) instead of flooding the error log.
func logEventError(err error) {
	if errors.Is(err, errEventAccountMissing) || errors.Is(err, errEventNotVisible) {
		log.Debug("streaming: skipping event: %v", err)

		return
	}

	log.ErrorV(errors.Wrap(err, "streaming: event error"))
}
