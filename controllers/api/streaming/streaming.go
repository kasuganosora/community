package streaming

import (
	"context"
	"crypto/md5"
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
	return utils.Hash(md5.New(), raw)
}

type StreamingResponse struct {
	Stream  []string `json:"stream"`
	Event   string   `json:"event"`
	Payload string   `json:"payload"`
}

type StreamingErrResponse struct {
	error
	Err string `json:"error"`
}

func (err *StreamingErrResponse) Error() string {
	return err.Err
}

func Streaming(c *gin.Context) {
	var account *models.Account

	if acc, ok := c.Get("account"); ok {
		account = acc.(*models.Account)
	}

	ws, err := upgrade.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	closeChan := make(chan bool)
	readChan := make(chan []byte)

	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				closeChan <- true
				return
			}
			readChan <- msg
		}
	}()

	var redisPubSub *redis.PubSub
	redisMsgChan := make(<-chan *redis.Message)

	defer func() {
		if redisPubSub != nil {
			redisPubSub.Close()
		}
	}()

	for {
		select {
		case <-closeChan:
			return

		case msg := <-readChan:
			redisPubSub, err = processUserMsg(ws, msg, account, redisPubSub)
			if err != nil {
				continue
			}

			if redisPubSub != nil {
				redisMsgChan = redisPubSub.Channel()
			}

		case msg := <-redisMsgChan:
			err = processRedisMsg(ws, account, msg)
		default:

		}

	}

}

func processUserMsg(ws *websocket.Conn, msg []byte, account *models.Account, redisPub *redis.PubSub) (ret *redis.PubSub, err error) {
	req := &StreamingRequest{}
	if err = json.Unmarshal(msg, req); err != nil {
		return
	}

	if req.Type == "subscribe" {
		ret, err = subscribe(redisPub, account, req)
		if err != nil {
			ws.WriteJSON(err)
			return
		}
	}

	if req.Type == "unsubscribe" {
		ret, err = unsubscribe(redisPub, account, req)
		if err != nil {
			ws.WriteJSON(err)
			return
		}
	}

	ret = redisPub

	ws.WriteJSON(&StreamingErrResponse{Err: "unknown type"})
	return
}

func subscribe(pub *redis.PubSub, account *models.Account, s *StreamingRequest) (ret *redis.PubSub, err error) {
	channelName, err := getChannelName(account, s)
	if err != nil {
		return
	}

	if pub != nil {
		err = pub.Unsubscribe(context.Background(), channelName)
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
		err = &StreamingErrResponse{Err: "unsubscribe failed"}
		return
	}

	err = pub.Unsubscribe(context.Background(), channelName)
	if err != nil {
		err = errors.Wrap(err, "[unsubscribe] unsubscribe failed")
		log.ErrorV(err)
		err = &StreamingErrResponse{Err: "unsubscribe failed"}
		return
	}
	ret = pub
	return
}

func getChannelName(account *models.Account, s *StreamingRequest) (channelName string, err error) {
	switch {
	case s.Stream == "user": // 当前用户的 home timeline
		if account == nil {
			err = &StreamingErrResponse{Err: "need login"}
			return
		}
		channelName = feed.GetRedisChannelName(&home.HomeTimeline{Account: account})
	case strings.HasPrefix(s.Stream, "public"): // 公开时间线
		isLocal := strings.Contains(s.Type, ":local")
		isRemote := strings.Contains(s.Type, ":remote")
		isMedia := strings.Contains(s.Type, ":media")
		timeline := &public.PublicTimeline{OnlyLocal: isLocal, OnlyRemote: isRemote, OnlyMedia: isMedia}
		channelName = feed.GetRedisChannelName(timeline)

	case strings.Contains(s.Stream, "hashtag"):
		if s.Tag == "" {
			err = &StreamingErrResponse{Err: "tag name is empty"}
			return
		}

		isLocal := strings.Contains(s.Type, ":local")
		isRemote := strings.Contains(s.Type, ":remote")
		isMedia := strings.Contains(s.Type, ":media")
		timeline := &tag.TagTimeline{Tag: s.Tag, OnlyLocal: isLocal, OnlyRemote: isRemote, OnlyMedia: isMedia}
		channelName = feed.GetRedisChannelName(timeline)
	case s.Stream == "user:notification":
		if account == nil {
			err = &StreamingErrResponse{Err: "need login"}
			return
		}
		channelName = feed.GetRedisChannelName(&notification.NotificationTimeline{Account: account})
	case s.Stream == "list":
		if s.List == "" {
			err = &StreamingErrResponse{Err: "list name is empty"}
			return
		}
		ls := &models.List{}
		err = db.GetDB().Where("name = ?", s.List).First(ls).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				err = &StreamingErrResponse{Err: "list not found"}
				return
			} else {
				err = errors.Wrap(err, "[Streaming] get list failed")
				log.ErrorV(err)
				err = &StreamingErrResponse{Err: "get list failed"}
				return
			}
		}
		channelName = feed.GetRedisChannelName(&list.ListTimeline{List: ls})

	case s.Stream == "direct":
		if account == nil {
			err = &StreamingErrResponse{Err: "need login"}
			return
		}

		channelName = feed.GetRedisChannelName(&direct.DirectTimeline{Account: account})
	default:
		err = &StreamingErrResponse{Err: "stream not found"}
	}

	return
}

func processRedisMsg(ws *websocket.Conn, account *models.Account, msg *redis.Message) (err error) {
	var payload string
	var eventType string

	switch {
	case strings.HasPrefix(msg.Channel, "feed:home:") || strings.HasPrefix(msg.Channel, "feed:public") || strings.HasPrefix(msg.Channel, "feed:hashtag") || strings.HasPrefix(msg.Channel, "feed:list") || strings.HasPrefix(msg.Channel, "feed:direct"):
		eventType, payload, err = serializerStatusEvent(account, msg.Payload)
	case strings.HasPrefix(msg.Channel, "feed:notification"):
		eventType, payload, err = serializerNotificationEvent(account, msg.Payload)
	default:
		err = &StreamingErrResponse{Err: "unknown channel"}
	}

	if err != nil {
		return
	}

	ret := map[string]any{
		"event":   eventType,
		"payload": payload,
		"stream":  make([]string, 1),
	}

	// 通过redis的channel来区分不同的stream
	lastIndex := strings.LastIndex(msg.Channel, ":")
	ret["stream"].([]string)[0] = strings.Replace(msg.Channel[:lastIndex], "feed:", "", 1)

	// 如果是列表则需要返回列表名
	// example:
	//["hashtag", "ブルアカ"]
	if ret["stream"].([]string)[0] == "list" {
		ret["stream"] = append(ret["stream"].([]string), msg.Channel[lastIndex+1:])
	}

	retPayload, err := json.Marshal(ret)

	if err != nil {
		return
	}

	err = ws.WriteMessage(websocket.TextMessage, retPayload)

	return
}
