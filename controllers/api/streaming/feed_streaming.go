package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/direct"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/feed/notification"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/feed/tag"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
)

// Each process owns one Redis subscription per active channel, shared between
// browser tabs/connections. A peer keeps only the latest notice so a slow
// client cannot queue a walk of timeline bodies. Status ids outrank hints:
// dropping an intermediate id is recovered by the client's head check, and a
// hint must not evict an id that is already waiting.
type feedTopic struct {
	pub       *redis.PubSub
	ready     chan struct{}
	readyOnce sync.Once
	cancel    context.CancelFunc
	err       error
	peers     map[chan feedNotice]bool
}

// feedNotice is one invalidation hint or one status id. Status frames are
// ids only — post text never crosses this socket.
type feedNotice struct {
	Hint  feed.ChangeHint
	Event string
	ID    string
}

var feedTopics = struct {
	sync.Mutex
	entries map[string]*feedTopic
	closed  bool
}{entries: map[string]*feedTopic{}}

var legacyHintRevision atomic.Uint64

// CloseFeedTopics ends active SSE requests before HTTP graceful shutdown.
// No network operation runs while the registry lock is held.
func CloseFeedTopics() {
	feedTopics.Lock()
	feedTopics.closed = true
	topics := feedTopics.entries
	pubs := []*redis.PubSub{}
	feedTopics.entries = map[string]*feedTopic{}
	for _, topic := range topics {
		if topic.pub != nil {
			pubs = append(pubs, topic.pub)
		}
		topic.err = context.Canceled
		topic.cancel()
		topic.readyOnce.Do(func() { close(topic.ready) })
		for peer := range topic.peers {
			close(peer)
			delete(topic.peers, peer)
		}
	}
	feedTopics.Unlock()
	for _, pub := range pubs {
		_ = pub.Close()
	}
}

func subscribeFeedHint(ctx context.Context, channel string) (<-chan feedNotice, func(), error) {
	client := db.GetUniversalRedis()
	key := fmt.Sprintf("%p/%s", client, channel)
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		feedTopics.Lock()
		if feedTopics.closed {
			feedTopics.Unlock()
			return nil, nil, context.Canceled
		}
		topic := feedTopics.entries[key]
		if topic == nil {
			initCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			topic = &feedTopic{ready: make(chan struct{}), cancel: cancel, peers: map[chan feedNotice]bool{}}
			feedTopics.entries[key] = topic
			defer func() {
				feedTopics.Lock()
				unused := feedTopics.entries[key] == topic && len(topic.peers) == 0
				if unused {
					delete(feedTopics.entries, key)
				}
				pub := topic.pub
				feedTopics.Unlock()
				if unused {
					topic.cancel()
					if pub != nil {
						_ = pub.Close()
					}
				}
			}()
			feedTopics.Unlock()
			pub := client.Subscribe(initCtx, channel)
			feedTopics.Lock()
			topic.pub = pub
			abandoned := feedTopics.entries[key] != topic
			feedTopics.Unlock()
			if abandoned {
				_ = pub.Close()
				cancel()
				return nil, nil, context.Canceled
			}
			err := confirmSubscribed(initCtx, pub, channel)
			cancel()
			feedTopics.Lock()
			if feedTopics.entries[key] != topic {
				err = context.Canceled
			}
			topic.err = err
			if err != nil && feedTopics.entries[key] == topic {
				delete(feedTopics.entries, key)
			}
			topic.readyOnce.Do(func() { close(topic.ready) })
			feedTopics.Unlock()
			if err != nil {
				_ = topic.pub.Close()
				return nil, nil, err
			}
			go runFeedTopic(key, topic)
		} else {
			feedTopics.Unlock()
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-topic.ready:
		}
		feedTopics.Lock()
		if err := ctx.Err(); err != nil {
			feedTopics.Unlock()
			return nil, nil, err
		}
		if topic.err != nil {
			err := topic.err
			feedTopics.Unlock()
			return nil, nil, err
		}
		if feedTopics.closed {
			feedTopics.Unlock()
			return nil, nil, context.Canceled
		}
		if feedTopics.entries[key] != topic {
			feedTopics.Unlock()
			continue
		}
		peer := make(chan feedNotice, 1)
		topic.peers[peer] = true
		feedTopics.Unlock()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				feedTopics.Lock()
				delete(topic.peers, peer)
				last := len(topic.peers) == 0 && feedTopics.entries[key] == topic
				if last {
					delete(feedTopics.entries, key)
				}
				feedTopics.Unlock()
				if last {
					topic.cancel()
					_ = topic.pub.Close()
				}
			})
		}
		return peer, stop, nil
	}
}

func runFeedTopic(key string, topic *feedTopic) {
	defer func() {
		feedTopics.Lock()
		if feedTopics.entries[key] == topic {
			delete(feedTopics.entries, key)
		}
		for peer := range topic.peers {
			close(peer)
		}
		topic.peers = map[chan feedNotice]bool{}
		feedTopics.Unlock()
	}()
	for msg := range topic.pub.Channel(redis.WithChannelSize(32)) {
		var wire struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(msg.Payload), &wire) != nil {
			continue
		}
		var notice feedNotice
		if wire.Type == "feed.changed" {
			var hint feed.ChangeHint
			if json.Unmarshal(wire.Payload, &hint) != nil || len(hint.Epoch) > 128 || len(hint.Revision) > 128 {
				continue
			}
			notice.Hint = hint
		} else if (wire.Type == "update" || wire.Type == "delete" || wire.Type == "status.update") && !notificationBody(wire.Payload) {
			id, ok := feedStatusID(wire.Payload)
			if !ok {
				continue
			}
			notice.Event = wire.Type
			notice.ID = id
		} else {
			switch wire.Type {
			case "filters_changed", "notification", "conversation", "announcement", "update", "delete", "status.update":
			default:
				continue
			}
			notice.Hint = feed.ChangeHint{Epoch: "legacy", Revision: strconv.FormatUint(legacyHintRevision.Add(1), 10)}
		}
		feedTopics.Lock()
		for peer := range topic.peers {
			deliverNotice(peer, notice)
		}
		feedTopics.Unlock()
	}
}

func feedStreamTimeline(c *gin.Context, account *models.Account) (feed.Timeline, error) {
	stream := c.DefaultQuery("stream", "user")
	switch stream {
	case "user":
		return &home.HomeTimeline{Account: account}, nil
	case "user:notification", "notification":
		// Notification badge/list invalidations (e.g. a poll closing).
		return &notification.NotificationTimeline{Account: account}, nil
	case "public", "public:local", "public:remote", "public:media", "public:local:media", "public:remote:media":
		return &public.PublicTimeline{OnlyLocal: strings.Contains(stream, ":local"), OnlyRemote: strings.Contains(stream, ":remote"), OnlyMedia: strings.Contains(stream, ":media")}, nil
	case "hashtag", "hashtag:local", "hashtag:remote":
		name := models.TagKey(c.Query("tag"))
		if name == "" || len(name) > 128 {
			return nil, fmt.Errorf("invalid tag")
		}
		return &tag.TagTimeline{Tag: name, OnlyLocal: strings.Contains(stream, ":local"), OnlyRemote: strings.Contains(stream, ":remote")}, nil
	case "list":
		id, err := strconv.ParseUint(c.Query("list"), 10, 64)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("invalid list")
		}
		var item models.List
		if err := db.GetDB().WithContext(c.Request.Context()).First(&item, id).Error; err != nil {
			return nil, fmt.Errorf("list unavailable")
		}
		allowed, err := models.ListCanView(&item, account)
		if err != nil || !allowed {
			return nil, fmt.Errorf("list unavailable")
		}
		return &list.ListTimeline{List: &item}, nil
	default:
		return nil, fmt.Errorf("unknown feed stream")
	}
}

// FeedStreaming sends only changes, never a serialized status. Authorization
// and removals are rechecked by the client's bounded HTTP window reconciliation.
func FeedStreaming(c *gin.Context) {
	account := controllers.GetAccount(c)
	if account == nil {
		c.AbortWithStatusJSON(401, gin.H{"error": "authentication required"})
		return
	}
	timeline, err := feedStreamTimeline(c, account)
	if err != nil {
		c.AbortWithStatusJSON(403, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	channels := []string{feed.ChangedChannel(timeline), feed.GetRedisChannelName(timeline)}
	personal := feed.ChangedChannel(&home.HomeTimeline{Account: account})
	if personal != channels[0] {
		channels = append(channels, personal)
	}
	if _, isNotification := timeline.(*notification.NotificationTimeline); isNotification && c.Query("unread") == "1" {
		// The nav badges: new direct messages and announcement changes wake
		// the same stream, so one connection keeps all three dots current.
		channels = append(channels, feed.GetRedisChannelName(&direct.DirectTimeline{Account: account}), feed.GetRedisChannelName(feed.AnnouncementsChannel{}))
	}
	merged := make(chan feedNotice, 1)
	mergeCtx, stopMerge := context.WithCancel(ctx)
	defer stopMerge()
	for _, channel := range channels {
		hints, stop, err := subscribeFeedHint(ctx, channel)
		if err != nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "feed stream temporarily unavailable"})
			return
		}
		defer stop()
		go func(input <-chan feedNotice) {
			for {
				select {
				case <-mergeCtx.Done():
					return
				case notice, ok := <-input:
					if !ok {
						stopMerge()
						return
					}
					deliverNotice(merged, notice)
				}
			}
		}(hints)
	}
	// Redis subscription setup may outlive a token revocation or moderation change.
	if _, err := revalidateFeedStreamingAccount(c, account); err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication no longer valid"})
		return
	}
	var out feedTransport
	if websocket.IsWebSocketUpgrade(c.Request) {
		ws, err := upgrade.Upgrade(c.Writer, c.Request, feedSubprotocolResponseHeader(c.Request))
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		wsOut := &feedWSTransport{ws: ws, closed: make(chan struct{})}
		go wsOut.readLoop(stopMerge)
		out = wsOut
	} else {
		out = newFeedSSETransport(c)
	}
	defer out.done()
	// Every SUBSCRIBE above is confirmed by Redis now, so anything published
	// from here on reaches this stream. A change committed before that (or
	// while the client's own on-connect refetch was in flight) would be lost,
	// so send one hint with a unique revision: the client recounts unread
	// state and reconciles its window after the subscriptions are live.
	if out.open(subscribedHint()) != nil {
		return
	}
	_ = feed.RecordActivity(ctx, account)
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-mergeCtx.Done():
			return
		case notice := <-merged:
			current, err := revalidateFeedStreamingAccount(c, account)
			if err != nil {
				return
			}
			account = current
			if _, isList := timeline.(*list.ListTimeline); isList {
				if _, err := feedStreamTimeline(c, account); err != nil {
					return
				}
			}
			if notice.Event != "" {
				if out.status(notice.Event, notice.ID) != nil {
					return
				}
				continue
			}
			if out.hint(notice.Hint) != nil {
				return
			}
		case <-heartbeat.C:
			current, err := revalidateFeedStreamingAccount(c, account)
			if err != nil {
				return
			}
			account = current
			if _, isList := timeline.(*list.ListTimeline); isList {
				if _, err := feedStreamTimeline(c, account); err != nil {
					return
				}
			}
			if out.heartbeat() != nil {
				return
			}
			_ = feed.RecordActivity(ctx, account)
		}
	}
}

// confirmSubscribed waits for Redis to acknowledge SUBSCRIBE for channel.
func confirmSubscribed(ctx context.Context, pub *redis.PubSub, channel string) error {
	reply, err := pub.Receive(ctx)
	if err != nil {
		return err
	}
	sub, ok := reply.(*redis.Subscription)
	if !ok || sub.Kind != "subscribe" || sub.Channel != channel {
		return fmt.Errorf("feed stream: unexpected subscribe reply %T", reply)
	}
	return nil
}

var subscribedHintSeq atomic.Uint64

// subscribedHintFrame is the first frame of every feed stream; its revision is
// never repeated, so clients that de-duplicate hints always act on it.
func subscribedHint() feed.ChangeHint {
	return feed.ChangeHint{Epoch: "subscribed", Revision: strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(subscribedHintSeq.Add(1), 10)}
}

func subscribedHintFrame() []byte { return sseHintFrame(subscribedHint()) }

func sseHintFrame(hint feed.ChangeHint) []byte {
	data, _ := json.Marshal(hint)
	return append(append([]byte("event: feed.changed\ndata: "), data...), '\n', '\n')
}

func sseStatusFrame(event, id string) []byte {
	data, _ := json.Marshal(struct {
		ID string `json:"id"`
	}{ID: id})
	frame := make([]byte, 0, len(event)+len(data)+16)
	frame = append(frame, "event: "...)
	frame = append(frame, event...)
	frame = append(frame, "\ndata: "...)
	frame = append(frame, data...)
	frame = append(frame, '\n', '\n')
	return frame
}

// deliverNotice keeps one pending notice. A hint does not replace a status id.
func deliverNotice(dst chan feedNotice, notice feedNotice) {
	select {
	case dst <- notice:
		return
	default:
	}
	select {
	case pending := <-dst:
		if notice.Event == "" && pending.Event != "" {
			select {
			case dst <- pending:
			default:
			}
			return
		}
	default:
	}
	select {
	case dst <- notice:
	default:
	}
}

// notificationBody is a Mastodon notification: its own id plus the status it
// is about. Status-edit notifications use type "update", which is also a
// timeline event name, so the nested status must not be read as a home post.
func notificationBody(raw json.RawMessage) bool {
	if len(raw) == 0 || raw[0] != '{' {
		return false
	}
	var payload struct {
		Type   string          `json:"type"`
		Status json.RawMessage `json:"status"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Type == "" || len(payload.Status) == 0 || payload.Status[0] != '{' {
		return false
	}
	switch payload.Type {
	case "mention", "reblog", "favourite", "follow", "follow_request", "poll", "update", "emoji_reaction", "admin.report", "add_list", "remove_list":
		return true
	default:
		return false
	}
}

// feedStatusID copies a decimal id out of a redis status payload and drops every other field.
func feedStatusID(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return "", false
	}
	var payload struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(raw, &payload) != nil || len(payload.ID) == 0 || len(payload.ID) > 40 {
		return "", false
	}
	id := strings.Trim(string(payload.ID), `"`)
	if id == "" || len(id) > 20 {
		return "", false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return id, true
}

// feedTransport carries feed hints over SSE or WebSocket. Both deliver the
// same "subscribed" confirmation first, then hints and heartbeats.
type feedTransport interface {
	open(first feed.ChangeHint) error
	hint(feed.ChangeHint) error
	status(event, id string) error
	heartbeat() error
	done()
}

type feedSSETransport struct {
	c          *gin.Context
	controller *http.ResponseController
}

func newFeedSSETransport(c *gin.Context) *feedSSETransport {
	setSSEHeaders(c.Writer.Header())
	return &feedSSETransport{c: c, controller: http.NewResponseController(c.Writer)}
}

func (t *feedSSETransport) write(payload []byte) error {
	_ = t.controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := t.c.Writer.Write(payload); err != nil {
		return err
	}
	return t.controller.Flush()
}

func (t *feedSSETransport) open(first feed.ChangeHint) error {
	return t.write(append(append([]byte{}, sseInitialPadding...), sseHintFrame(first)...))
}
func (t *feedSSETransport) hint(h feed.ChangeHint) error { return t.write(sseHintFrame(h)) }
func (t *feedSSETransport) status(event, id string) error {
	return t.write(sseStatusFrame(event, id))
}
func (t *feedSSETransport) heartbeat() error { return t.write(sseHeartbeat) }
func (t *feedSSETransport) done()            { _ = t.controller.SetWriteDeadline(time.Time{}) }

// feedWSTransport sends Mastodon-shaped messages:
// {"stream":["feed"],"event":"feed.changed","payload":"{\"epoch\":...}"}.
// A heartbeat is an application message too (besides the ping) so browser
// clients, which cannot see pings, can detect a dead connection.
type feedWSTransport struct {
	ws     *websocket.Conn
	mu     sync.Mutex
	closed chan struct{}
}

func (t *feedWSTransport) readLoop(onClose func()) {
	defer close(t.closed)
	defer onClose()
	t.ws.SetReadLimit(4096)
	for {
		if _, _, err := t.ws.ReadMessage(); err != nil {
			return
		}
	}
}

func (t *feedWSTransport) send(event, payload string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = t.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return t.ws.WriteJSON(StreamingResponse{Stream: []string{"feed"}, Event: event, Payload: payload})
}

func (t *feedWSTransport) open(first feed.ChangeHint) error { return t.hint(first) }
func (t *feedWSTransport) hint(h feed.ChangeHint) error {
	data, _ := json.Marshal(h)
	return t.send("feed.changed", string(data))
}
func (t *feedWSTransport) status(event, id string) error {
	data, _ := json.Marshal(struct {
		ID string `json:"id"`
	}{ID: id})
	return t.send(event, string(data))
}
func (t *feedWSTransport) heartbeat() error {
	t.mu.Lock()
	err := t.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
	t.mu.Unlock()
	if err != nil {
		return err
	}
	return t.send("heartbeat", "")
}
func (t *feedWSTransport) done() {}
