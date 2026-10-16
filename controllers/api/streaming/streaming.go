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
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"strconv"
	"strings"
)

// 用户设定要订阅什么主题
// {"type":"subscribe","stream":"user"}

// 收到用户订阅的主题后，返回主题的内容
// {"stream":["user"],"event":"update","payload":"{\"id\":\"110868746257279010\",\"created_at\":\"2023-08-11T03:00:10.192Z\",\"in_reply_to_id\":null,\"in_reply_to_account_id\":null,\"sensitive\":false,\"spoiler_text\":\"\",\"visibility\":\"public\",\"language\":null,\"uri\":\"https://misskey.io/notes/9i9ke7v4ws\",\"url\":\"https://misskey.io/notes/9i9ke7v4ws\",\"replies_count\":0,\"reblogs_count\":0,\"favourites_count\":0,\"edited_at\":null,\"content\":\"<p>​:ohayoo:​</p>\",\"reblog\":null,\"account\":{\"id\":\"110145337597585017\",\"username\":\"neoki_ohaE\",\"acct\":\"neoki_ohaE@misskey.io\",\"display_name\":\"neoki\",\"locked\":false,\"bot\":false,\"discoverable\":true,\"group\":false,\"created_at\":\"2023-04-05T00:00:00.000Z\",\"note\":\"<p><span>自創作キャラデザ/昼寝が好きです<br>Twitter⇨ </span><a href=\\\"https://twitter.com/neoki_ohae/\\\" rel=\\\"nofollow noopener noreferrer\\\" target=\\\"_blank\\\">https://twitter.com/neoki_ohae/</a><span><br>Instagram⇨ </span><a href=\\\"https://instagram.com/neoki_ohae\\\" rel=\\\"nofollow noopener noreferrer\\\" target=\\\"_blank\\\">https://instagram.com/neoki_ohae</a><span><br>ご依頼⇨ </span><a href=\\\"https://coconala.com/users/3836159\\\" rel=\\\"nofollow noopener noreferrer\\\" target=\\\"_blank\\\">https://coconala.com/users/3836159</a><span><br><br></span>📎<span>  </span><a href=\\\"https://misskey.io/clips/9fnq2pi55d\\\" rel=\\\"nofollow noopener noreferrer\\\" target=\\\"_blank\\\">https://misskey.io/clips/9fnq2pi55d</a><span><br>　　</span><a href=\\\"https://misskey.io/clips/9fnq9r0q5k\\\" rel=\\\"nofollow noopener noreferrer\\\" target=\\\"_blank\\\">https://misskey.io/clips/9fnq9r0q5k</a></p>\",\"url\":\"https://misskey.io/@neoki_ohaE\",\"avatar\":\"https://files.meow.cloud/cache/accounts/avatars/110/145/337/597/585/017/original/fb3c2010a8dbd6e4.gif\",\"avatar_static\":\"https://files.meow.cloud/cache/accounts/avatars/110/145/337/597/585/017/static/fb3c2010a8dbd6e4.png\",\"header\":\"https://files.meow.cloud/cache/accounts/headers/110/145/337/597/585/017/original/0819085a5e4a2ced.png\",\"header_static\":\"https://files.meow.cloud/cache/accounts/headers/110/145/337/597/585/017/original/0819085a5e4a2ced.png\",\"followers_count\":1671,\"following_count\":90,\"statuses_count\":4302,\"last_status_at\":\"2023-08-11\",\"emojis\":[],\"fields\":[]},\"media_attachments\":[],\"mentions\":[],\"tags\":[],\"emojis\":[{\"shortcode\":\"ohayoo\",\"url\":\"https://files.meow.cloud/cache/custom_emojis/images/000/011/476/original/ef1d553b2e9752de.png\",\"static_url\":\"https://files.meow.cloud/cache/custom_emojis/images/000/011/476/static/ef1d553b2e9752de.png\",\"visible_in_picker\":true}],\"card\":null,\"poll\":null,\"favourited\":false,\"reblogged\":false,\"muted\":false,\"bookmarked\":false,\"filtered\":[]}"}

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

	ws, err := upgrade.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer func() { _ = ws.Close() }()

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

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

	redisMsgChan := make(<-chan *redis.Message)

	defer func() {
		if redisPubSub != nil {
			_ = redisPubSub.Close()
		}
	}()

	for {
		select {
		case <-closeChan:
			return

		case msg, ok := <-readChan:
			if !ok {
				return
			}

			redisPubSub, err = processUserMsg(ws, msg, account, redisPubSub)
			if err != nil {
				continue
			}

			if redisPubSub != nil {
				redisMsgChan = redisPubSub.Channel()
			}

		case msg, open := <-redisMsgChan:
			if !open || msg == nil {
				return
			}
			if err := processRedisMsg(ws, account, msg); err != nil {
				logEventError(err)
			}
		}
	}
}

func processUserMsg(ws *websocket.Conn, msg []byte, account *models.Account, redisPub *redis.PubSub) (ret *redis.PubSub, err error) {
	req := &StreamingRequest{}
	if err = json.Unmarshal(msg, req); err != nil {
		return
	}

	if req.Type == "subscribe" { //nolint:goconst
		ret, err = subscribe(redisPub, account, req)
		if err != nil {
			_ = ws.WriteJSON(err)
		}

		return
	}

	if req.Type == "unsubscribe" { //nolint:goconst
		ret, err = unsubscribe(redisPub, account, req)
		if err != nil {
			_ = ws.WriteJSON(err)
		}

		return
	}

	ret = redisPub
	_ = ws.WriteJSON(&StreamingErrorResponse{Err: "unknown type"})

	return
}

func subscribe(pub *redis.PubSub, account *models.Account, s *StreamingRequest) (ret *redis.PubSub, err error) {
	channelName, err := getChannelName(account, s)
	if err != nil {
		return
	}

	if pub != nil {
		err = pub.Subscribe(context.Background(), channelName)
		if err != nil {
			return
		}

		ret = pub

		return
	}

	ret = db.GetRedis().Subscribe(context.Background(), channelName)

	return
}

func unsubscribe(pub *redis.PubSub, account *models.Account, s *StreamingRequest) (ret *redis.PubSub, err error) {
	channelName, err := getChannelName(account, s)
	if err != nil {
		return
	}

	if pub == nil {
		err = errors.New("[unsubscribe]pub is nil")
		log.ErrorV(err)
		err = &StreamingErrorResponse{Err: "unsubscribe failed"}

		return
	}

	err = pub.Unsubscribe(context.Background(), channelName)
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

		// Check ownership or follow relationship
		if account == nil {
			err = &StreamingErrorResponse{Err: "need login"}

			return channelName, err
		}

		allowed, permissionErr := models.ListCanView(ls, account)
		if permissionErr != nil {
			return "", permissionErr
		}
		if !allowed {
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
		if account == nil {
			return nil, "", errEventNotVisible
		}
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
	case "direct":
		eventType, payload, err = serializerConversationEvent(account, msg.Payload)
	default:
		eventType, payload, err = serializerStatusEvent(account, msg.Payload, msg.Channel)
	}
	if err != nil {
		return err
	}
	return ws.WriteJSON(StreamingResponse{Stream: stream, Event: eventType, Payload: payload})
}
