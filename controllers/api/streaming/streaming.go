package streaming

import (
	"context"
	"crypto/md5" //nolint:gosec // G501: reviewed and safe
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/direct"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/feed/notification"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/feed/tag"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/secretredact"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 用户设定要订阅什么主题
// {"type":"subscribe","stream":"user"}

// 收到用户订阅的主题后，返回主题的内容
// {"stream":["user"],"event":"update","payload":"{\"id\":\"110868746257279010\",\"created_at\":\"2023-08-11T03:00:10.192Z\",\"in_reply_to_id\":null,\"in_reply_to_account_id\":null,\"sensitive\":false,\"spoiler_text\":\"\",\"visibility\":\"public\",\"language\":null,\"uri\":\"https://misskey.io/notes/9i9ke7v4ws\",\"url\":\"https://misskey.io/notes/9i9ke7v4ws\",\"replies_count\":0,\"reblogs_count\":0,\"favourites_count\":0,\"edited_at\":null,\"content\":\"<p>​:ohayoo:​</p>\",\"reblog\":null,\"account\":{\"id\":\"110145337597585017\",\"username\":\"neoki_ohaE\",\"acct\":\"neoki_ohaE@misskey.io\",\"display_name\":\"neoki\",\"locked\":false,\"bot\":false,\"discoverable\":true,\"group\":false,\"created_at\":\"2023-04-05T00:00:00.000Z\",\"note\":\"<p><span>自創作キャラデザ/昼寝が好きです<br>Twitter⇨ </span><a href=\\\"https://twitter.com/neoki_ohae/\\\" rel=\\\"nofollow noopener noreferrer\\\" target=\\\"_blank\\\">https://twitter.com/neoki_ohae/</a><span><br>Instagram⇨ </span><a href=\\\"https://instagram.com/neoki_ohae\\\" rel=\\\"nofollow noopener noreferrer\\\" target=\\\"_blank\\\">https://instagram.com/neoki_ohae</a><span><br>ご依頼⇨ </span><a href=\\\"https://coconala.com/users/3836159\\\" rel=\\\"nofollow noopener noreferrer\\\" target=\\\"_blank\\\">https://coconala.com/users/3836159</a><span><br><br></span>📎<span>  </span><a href=\\\"https://misskey.io/clips/9fnq2pi55d\\\" rel=\\\"nofollow noopener noreferrer\\\" target=\\\"_blank\\\">https://misskey.io/clips/9fnq2pi55d</a><span><br>　　</span><a href=\\\"https://misskey.io/clips/9fnq9r0q5k\\\" rel=\\\"nofollow noopener noreferrer\\\" target=\\\"_blank\\\">https://misskey.io/clips/9fnq9r0q5k</a></p>\",\"url\":\"https://misskey.io/@neoki_ohaE\",\"avatar\":\"https://files.meow.cloud/cache/accounts/avatars/110/145/337/597/585/017/original/fb3c2010a8dbd6e4.gif\",\"avatar_static\":\"https://files.meow.cloud/cache/accounts/avatars/110/145/337/597/585/017/static/fb3c2010a8dbd6e4.png\",\"header\":\"https://files.meow.cloud/cache/accounts/headers/110/145/337/597/585/017/original/0819085a5e4a2ced.png\",\"header_static\":\"https://files.meow.cloud/cache/accounts/headers/110/145/337/597/585/017/original/0819085a5e4a2ced.png\",\"followers_count\":1671,\"following_count\":90,\"statuses_count\":4302,\"last_status_at\":\"2023-08-11\",\"emojis\":[],\"fields\":[]},\"media_attachments\":[],\"mentions\":[],\"tags\":[],\"emojis\":[{\"shortcode\":\"ohayoo\",\"url\":\"https://files.meow.cloud/cache/custom_emojis/images/000/011/476/original/ef1d553b2e9752de.png\",\"static_url\":\"https://files.meow.cloud/cache/custom_emojis/images/000/011/476/static/ef1d553b2e9752de.png\",\"visible_in_picker\":true}],\"card\":null,\"poll\":null,\"favourited\":false,\"reblogged\":false,\"muted\":false,\"bookmarked\":false,\"filtered\":[]}"}

// streamingPingInterval keeps idle WebSocket connections open behind proxies.
var streamingPingInterval = 15 * time.Second

type StreamingRequest struct {
	Type   string `json:"type"`
	Stream string `json:"stream"`
	List   string `json:"list"`
	Tag    string `json:"tag"`
}

func (sr *StreamingRequest) Hash() string {
	raw := fmt.Sprintf("%s:%s:%s", sr.Type, sr.Stream, sr.List)

	return utils.Hash(md5.New(), raw) //nolint:gosec // G401: reviewed and safe
}

type StreamingResponse struct {
	Stream  []string `json:"stream"`
	Event   string   `json:"event"`
	Payload string   `json:"payload"`
}

type StreamingErrorResponse struct { //nolint:errname // intentional naming for API response
	Err string `json:"error"`
}

func (err *StreamingErrorResponse) Error() string {
	return err.Err
}

func Streaming(c *gin.Context) {
	var account *models.Account

	if acc, ok := c.Get("account"); ok {
		account = acc.(*models.Account) //nolint:forcetypeassert
	}

	// Mastodon clients choose the stream with ?stream= (plus tag/list) and
	// expect events without sending a subscribe message. Resolve it before the
	// upgrade so an unauthorized or unknown stream is an HTTP error, not a
	// silent socket.
	var initial *StreamingRequest
	if stream := strings.TrimSpace(c.Query("stream")); stream != "" {
		initial = &StreamingRequest{Type: "subscribe", Stream: stream, Tag: c.Query("tag"), List: c.Query("list")}
		if _, err := getChannelName(account, initial); err != nil {
			status := http.StatusBadRequest
			var streamErr *StreamingErrorResponse
			if errors.As(err, &streamErr) {
				switch streamErr.Err {
				case "need login":
					status = http.StatusUnauthorized
				case visitorRestrictedError, "forbidden":
					status = http.StatusForbidden
				case "list not found", "stream not found":
					status = http.StatusNotFound
				}
			} else {
				status = http.StatusInternalServerError
				err = &StreamingErrorResponse{Err: "subscribe failed"}
			}
			c.AbortWithStatusJSON(status, gin.H{"error": err.Error()})
			return
		}
	}

	ws, err := upgrade.Upgrade(c.Writer, c.Request, subprotocolResponseHeader(c.Request))
	if err != nil {
		return
	}
	defer func() { _ = ws.Close() }()
	ws.SetReadLimit(4096)

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	shutdown := currentStreamShutdown()

	closeChan := make(chan bool, 1)
	readChan := make(chan []byte)

	go func() {
		defer close(readChan)
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				select {
				case closeChan <- true:
				case <-ctx.Done():
				}

				return
			}

			select {
			case readChan <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()

	var redisPubSub *redis.PubSub
	activeStreams := map[string]bool{}

	redisMsgChan := make(<-chan *redis.Message)

	defer func() {
		if redisPubSub != nil {
			_ = redisPubSub.Close()
		}
	}()

	writeWSJSON := func(v any) error {
		_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return ws.WriteJSON(v)
	}

	if initial != nil {
		redisPubSub, err = subscribe(nil, account, initial)
		if err != nil {
			if streamErr, ok := err.(*StreamingErrorResponse); ok { //nolint:errorlint
				_ = writeWSJSON(streamErr)
			} else {
				_ = writeWSJSON(&StreamingErrorResponse{Err: "subscribe failed"})
			}
			return
		}
		activeStreams[initial.Stream] = true
		redisMsgChan = redisPubSub.Channel()
	}

	// Reverse proxies drop idle upgraded connections; keep them alive.
	ping := time.NewTicker(streamingPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-shutdown:
			return
		case <-ping.C:
			if account != nil {
				current, err := revalidateStreamConnection(c, account)
				if err != nil {
					return
				}
				account = current
			}
			if ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
				return
			}

		case <-closeChan:
			return

		case msg, ok := <-readChan:
			if !ok {
				return
			}
			if account != nil {
				current, err := revalidateStreamConnection(c, account)
				if err != nil {
					return
				}
				account = current
			}

			var parsed StreamingRequest
			parsedOK := json.Unmarshal(msg, &parsed) == nil
			redisPubSub, err = processUserMsg(ws, msg, account, redisPubSub)
			if err != nil {
				continue
			}
			if parsedOK {
				switch parsed.Type {
				case "subscribe":
					activeStreams[parsed.Stream] = true
				case "unsubscribe":
					delete(activeStreams, parsed.Stream)
				}
			}

			if redisPubSub != nil {
				redisMsgChan = redisPubSub.Channel()
			}

		case msg, open := <-redisMsgChan:
			if !open || msg == nil {
				return
			}
			if account != nil {
				current, err := revalidateStreamConnection(c, account)
				if err != nil {
					return
				}
				account = current
			}
			if err := processRedisMsgWithStreams(ws, account, msg, activeStreams); err != nil {
				logEventError(err)
			}
		}
	}
}

func safeStreamingError(err error, fallback string) *StreamingErrorResponse {
	if streamErr, ok := err.(*StreamingErrorResponse); ok { //nolint:errorlint
		return streamErr
	}
	return &StreamingErrorResponse{Err: fallback}
}

func processUserMsg(ws *websocket.Conn, msg []byte, account *models.Account, redisPub *redis.PubSub) (ret *redis.PubSub, err error) {
	// Always preserve the existing PubSub by default so malformed JSON or
	// rejected stream requests never leak the active Redis subscription.
	ret = redisPub
	req := &StreamingRequest{}
	if err = json.Unmarshal(msg, req); err != nil {
		return ret, err
	}

	if req.Type == "subscribe" { //nolint:goconst
		sub, subErr := subscribe(redisPub, account, req)
		if subErr != nil {
			_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = ws.WriteJSON(safeStreamingError(subErr, "subscribe failed"))
			return redisPub, subErr
		}

		return sub, nil
	}

	if req.Type == "unsubscribe" { //nolint:goconst
		sub, unsubErr := unsubscribe(redisPub, account, req)
		if unsubErr != nil {
			_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = ws.WriteJSON(safeStreamingError(unsubErr, "unsubscribe failed"))
			return redisPub, unsubErr
		}

		return sub, nil
	}

	_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = ws.WriteJSON(&StreamingErrorResponse{Err: "unknown type"})

	return redisPub, nil
}

func getChannelNames(account *models.Account, s *StreamingRequest) ([]string, error) {
	primary, err := getChannelName(account, s)
	if err != nil {
		return nil, err
	}
	if s.Stream == "user" && account != nil {
		return []string{
			primary,
			feed.GetRedisChannelName(&notification.NotificationTimeline{Account: account}),
			feed.GetRedisChannelName(feed.AnnouncementsChannel{}),
		}, nil
	}
	return []string{primary}, nil
}

func subscribe(pub *redis.PubSub, account *models.Account, s *StreamingRequest) (ret *redis.PubSub, err error) {
	channels, err := getChannelNames(account, s)
	if err != nil {
		return
	}

	if pub != nil {
		err = pub.Subscribe(context.Background(), channels...)
		if err != nil {
			return
		}

		ret = pub

		return
	}

	ret = db.GetUniversalRedis().Subscribe(context.Background(), channels...)

	return
}

func unsubscribe(pub *redis.PubSub, account *models.Account, s *StreamingRequest) (ret *redis.PubSub, err error) {
	channels, err := getChannelNames(account, s)
	if err != nil {
		return
	}

	if pub == nil {
		err = errors.New("[unsubscribe]pub is nil")
		log.ErrorV(err)
		err = &StreamingErrorResponse{Err: "unsubscribe failed"}

		return
	}

	err = pub.Unsubscribe(context.Background(), channels...)
	if err != nil {
		err = errors.Wrap(err, "[unsubscribe] unsubscribe failed")
		log.ErrorV(err)
		err = &StreamingErrorResponse{Err: "unsubscribe failed"}

		return
	}

	ret = pub

	return
}

func getChannelName(account *models.Account, s *StreamingRequest) (channelName string, err error) {
	if !visitorStreamAllowed(account, s.Stream) {
		return "", &StreamingErrorResponse{Err: visitorRestrictedError}
	}
	switch {
	case s.Stream == "user": //nolint:goconst
		if account == nil {
			err = &StreamingErrorResponse{Err: "need login"} //nolint:goconst

			return channelName, err
		}

		channelName = feed.GetRedisChannelName(&home.HomeTimeline{Account: account})
	case strings.HasPrefix(s.Stream, "public"): // 公开时间线
		isLocal := strings.Contains(s.Stream, ":local")
		isRemote := strings.Contains(s.Stream, ":remote")
		isMedia := strings.Contains(s.Stream, ":media")
		timeline := &public.PublicTimeline{OnlyLocal: isLocal, OnlyRemote: isRemote, OnlyMedia: isMedia}
		channelName = feed.GetRedisChannelName(timeline)

	case strings.Contains(s.Stream, "hashtag"):
		if s.Tag == "" {
			err = &StreamingErrorResponse{Err: "tag name is empty"} //nolint:goconst

			return channelName, err
		}

		isLocal := strings.Contains(s.Stream, ":local")
		isRemote := strings.Contains(s.Stream, ":remote")
		isMedia := strings.Contains(s.Stream, ":media")
		timeline := &tag.TagTimeline{Tag: s.Tag, OnlyLocal: isLocal, OnlyRemote: isRemote, OnlyMedia: isMedia}
		channelName = feed.GetRedisChannelName(timeline)
	case s.Stream == "user:notification": //nolint:goconst
		if account == nil {
			err = &StreamingErrorResponse{Err: "need login"}

			return channelName, err
		}

		channelName = feed.GetRedisChannelName(&notification.NotificationTimeline{Account: account})
	case s.Stream == "list": //nolint:goconst
		if s.List == "" {
			err = &StreamingErrorResponse{Err: "list name is empty"}

			return channelName, err
		}

		ls := &models.List{}

		err = db.GetDB().Where("id = ?", s.List).First(ls).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				err = &StreamingErrorResponse{Err: "list not found"} //nolint:goconst

				return channelName, err
			} else {
				err = errors.Wrap(err, "[Streaming] get list failed")
				log.ErrorV(err)
				err = &StreamingErrorResponse{Err: "get list failed"}

				return channelName, err
			}
		}

		allowed, permissionErr := models.ListCanView(ls, account)
		if permissionErr != nil {
			return "", permissionErr
		}
		if !allowed {
			if account == nil {
				return "", &StreamingErrorResponse{Err: "need login"}
			}
			return "", &StreamingErrorResponse{Err: "forbidden"}
		}

		channelName = feed.GetRedisChannelName(&list.ListTimeline{List: ls})

	case s.Stream == "direct": //nolint:goconst
		if account == nil {
			err = &StreamingErrorResponse{Err: "need login"}

			return channelName, err
		}

		channelName = feed.GetRedisChannelName(&direct.DirectTimeline{Account: account})
	default:
		err = &StreamingErrorResponse{Err: "stream not found"} //nolint:goconst
	}

	return channelName, err
}

// streamForChannel converts internal (including versioned) Redis keys back to
// Mastodon's public stream tuple and revalidates list subscriptions on delivery.
func streamForChannel(account *models.Account, raw string) ([]string, string, error) {
	channel := strings.TrimPrefix(raw, "subscribed:")
	parts := strings.Split(channel, ":")
	if len(parts) < 2 {
		return nil, "", &StreamingErrorResponse{Err: "unknown channel"}
	}
	if parts[0] == "announcements" {
		if account == nil {
			return nil, "", errEventNotVisible
		}
		return []string{"user"}, "announcements", nil
	}
	if parts[0] == "direct" {
		parts = append([]string{"feed"}, parts...)
	}
	if parts[0] != "feed" || len(parts) < 3 {
		return nil, "", &StreamingErrorResponse{Err: "unknown channel"}
	}
	kind := parts[1]
	last := parts[len(parts)-1]
	switch kind {
	case "home", "notification", "direct":
		id, err := strconv.ParseUint(last, 10, 64)
		if err != nil || account == nil || account.Id != id {
			return nil, "", errEventNotVisible
		}
		name := map[string]string{"home": "user", "notification": "user:notification", "direct": "direct"}[kind]
		return []string{name}, kind, nil
	case "public":
		name := "public"
		for _, scope := range []string{"local", "remote", "media"} {
			for _, value := range parts[2:] {
				if value == scope {
					name += ":" + scope
				}
			}
		}
		return []string{name}, kind, nil
	case "hashtag":
		name := "hashtag"
		if len(parts) > 3 {
			name += ":" + strings.Join(parts[2:len(parts)-1], ":")
		}
		return []string{name, last}, kind, nil
	case "list":
		id, err := strconv.ParseUint(last, 10, 64)
		if err != nil || id == 0 {
			return nil, "", errEventNotVisible
		}
		var current models.List
		if err := db.GetDB().First(&current, id).Error; err != nil {
			return nil, "", errEventNotVisible
		}
		allowed, err := models.ListCanView(&current, account)
		if err != nil {
			return nil, "", err
		}
		if !allowed {
			return nil, "", errEventNotVisible
		}
		return []string{"list", last}, kind, nil
	default:
		return nil, "", &StreamingErrorResponse{Err: "unknown channel"}
	}
}

func processRedisMsg(ws *websocket.Conn, account *models.Account, msg *redis.Message) error {
	return processRedisMsgWithStreams(ws, account, msg, nil)
}

func processRedisMsgWithStreams(ws *websocket.Conn, account *models.Account, msg *redis.Message, activeStreams map[string]bool) error {
	if msg == nil {
		return errEventNotVisible
	}
	stream, kind, err := streamForChannel(account, msg.Channel)
	if err != nil {
		return err
	}
	// Re-check on delivery so a policy change applies to open connections.
	if len(stream) > 0 && !visitorStreamAllowed(account, stream[0]) {
		return errEventNotVisible
	}
	var eventType, payload string
	switch kind {
	case "notification":
		eventType, payload, err = serializerNotificationEvent(account, msg.Payload)
	case "announcements":
		var ev struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err = json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
			return err
		}
		switch ev.Type {
		case "announcement":
			eventType, payload, err = serializerAnnouncementEvent(ev.Payload)
		case "announcement.delete":
			eventType, payload, err = serializerAnnouncementDeleteEvent(ev.Payload)
		case "announcement.reaction":
			eventType, payload, err = serializerAnnouncementReactionEvent(ev.Payload)
		default:
			return errEventNotVisible
		}
	case "direct":
		if isStatusStreamEvent(msg.Payload) {
			eventType, payload, err = serializerStatusEvent(account, msg.Payload, msg.Channel)
		} else {
			eventType, payload, err = serializerConversationEvent(account, msg.Payload)
		}
	default:
		eventType, payload, err = serializerStatusEvent(account, msg.Payload, msg.Channel)
	}
	if err != nil {
		return err
	}
	if kind == "notification" && activeStreams != nil && activeStreams["user"] && !activeStreams["user:notification"] {
		stream = []string{"user"}
	}
	_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if kind == "notification" && activeStreams != nil && activeStreams["user"] && activeStreams["user:notification"] {
		if err := ws.WriteJSON(StreamingResponse{Stream: []string{"user"}, Event: eventType, Payload: payload}); err != nil {
			return err
		}
		_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return ws.WriteJSON(StreamingResponse{Stream: []string{"user:notification"}, Event: eventType, Payload: payload})
	}
	return ws.WriteJSON(StreamingResponse{Stream: stream, Event: eventType, Payload: payload})
}

// CommunitySubprotocol is the only subprotocol our own clients negotiate.
const CommunitySubprotocol = secretredact.PublicSubprotocol

// selectedSubprotocol picks the Sec-WebSocket-Protocol the server answers
// with. It never selects a "community.bearer.*" entry: those carry the access
// token, and echoing one would put the token into the response headers (and
// every proxy/CDN log that records them).
//
// community.v1 always wins when offered. When allowRawToken is set (Mastodon
// /api/v1/streaming only) and community.v1 was not offered, the first offered
// value is echoed: Mastodon clients send their raw access token as the only
// subprotocol and browsers abort the handshake unless the server selects one
// of the offered values. That echo is the client's own token, matches
// upstream Mastodon behaviour, and response headers are scrubbed by
// secretredact before logging.
func selectedSubprotocol(r *http.Request, allowRawToken bool) string {
	offered := websocket.Subprotocols(r)
	for _, p := range offered {
		if p == CommunitySubprotocol {
			return p
		}
	}
	if allowRawToken && len(offered) > 0 && !strings.HasPrefix(offered[0], secretredact.BearerSubprotocolPrefix) {
		return offered[0]
	}
	return ""
}

func subprotocolHeader(selected string) http.Header {
	if selected == "" {
		return nil
	}
	header := http.Header{}
	header.Set("Sec-WebSocket-Protocol", selected)
	return header
}

// subprotocolResponseHeader is the Mastodon-compatible selection used by
// /api/v1/streaming (see selectedSubprotocol).
func subprotocolResponseHeader(r *http.Request) http.Header {
	return subprotocolHeader(selectedSubprotocol(r, true))
}

// feedSubprotocolResponseHeader is used by our own feed endpoint: it only
// ever answers community.v1, or nothing.
func feedSubprotocolResponseHeader(r *http.Request) http.Header {
	return subprotocolHeader(selectedSubprotocol(r, false))
}
