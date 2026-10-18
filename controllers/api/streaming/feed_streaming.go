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
// browser tabs/connections. A peer keeps only the latest hint: a full HTTP
// window revalidation makes dropping intermediate hints safe and bounded.
type feedTopic struct {
	pub       *redis.PubSub
	ready     chan struct{}
	readyOnce sync.Once
	cancel    context.CancelFunc
	err       error
	peers     map[chan feed.ChangeHint]bool
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

func subscribeFeedHint(ctx context.Context, channel string) (<-chan feed.ChangeHint, func(), error) {
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
			topic = &feedTopic{ready: make(chan struct{}), cancel: cancel, peers: map[chan feed.ChangeHint]bool{}}
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
		peer := make(chan feed.ChangeHint, 1)
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
		topic.peers = map[chan feed.ChangeHint]bool{}
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
		var hint feed.ChangeHint
		if wire.Type == "feed.changed" {
			if json.Unmarshal(wire.Payload, &hint) != nil || len(hint.Epoch) > 128 || len(hint.Revision) > 128 {
				continue
			}
		} else {
			switch wire.Type {
			case "update", "delete", "status.update", "filters_changed", "notification", "conversation", "announcement":
			default:
				continue
			}
			hint = feed.ChangeHint{Epoch: "legacy", Revision: strconv.FormatUint(legacyHintRevision.Add(1), 10)}
		}
		feedTopics.Lock()
		for peer := range topic.peers {
			select {
			case peer <- hint:
			default:
				select {
				case <-peer:
				default:
				}
				select {
				case peer <- hint:
				default:
				}
			}
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
	merged := make(chan feed.ChangeHint, 1)
	mergeCtx, stopMerge := context.WithCancel(ctx)
	defer stopMerge()
	for _, channel := range channels {
		hints, stop, err := subscribeFeedHint(ctx, channel)
		if err != nil {
			c.AbortWithStatusJSON(503, gin.H{"error": "feed stream temporarily unavailable"})
			return
		}
		defer stop()
		go func(input <-chan feed.ChangeHint) {
			for {
				select {
				case <-mergeCtx.Done():
					return
				case hint, ok := <-input:
					if !ok {
						stopMerge()
						return
					}
					select {
					case merged <- hint:
					default:
					}
				}
			}
		}(hints)
	}
	// Redis subscription setup may outlive a token revocation or moderation change.
	if _, err := currentFeedStreamingAccount(c, account); err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication no longer valid"})
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Connection", "keep-alive")
	controller := http.NewResponseController(c.Writer)
	write := func(payload []byte) error {
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := c.Writer.Write(payload)
		if err != nil {
			return err
		}
		return controller.Flush()
	}
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	// Every SUBSCRIBE above is confirmed by Redis now, so anything published
	// from here on reaches this stream. A change committed before that (or
	// while the client's own on-connect refetch was in flight) would be lost,
	// so send one hint with a unique revision: the client recounts unread
	// state and reconciles its window after the subscriptions are live.
	if write(subscribedHintFrame()) != nil {
		return
	}
	_ = feed.RecordActivity(ctx, account)
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-mergeCtx.Done():
			return
		case hint := <-merged:
			current, err := currentFeedStreamingAccount(c, account)
			if err != nil {
				return
			}
			account = current
			if _, isList := timeline.(*list.ListTimeline); isList {
				if _, err := feedStreamTimeline(c, account); err != nil {
					return
				}
			}
			data, _ := json.Marshal(hint)
			frame := append([]byte("event: feed.changed\ndata: "), data...)
			frame = append(frame, '\n', '\n')
			if write(frame) != nil {
				return
			}
		case <-heartbeat.C:
			current, err := currentFeedStreamingAccount(c, account)
			if err != nil {
				return
			}
			account = current
			if _, isList := timeline.(*list.ListTimeline); isList {
				if _, err := feedStreamTimeline(c, account); err != nil {
					return
				}
			}
			if write([]byte(": keepalive\n\n")) != nil {
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
func subscribedHintFrame() []byte {
	data, _ := json.Marshal(feed.ChangeHint{Epoch: "subscribed", Revision: strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(subscribedHintSeq.Add(1), 10)})
	return append(append([]byte("event: feed.changed\ndata: "), data...), '\n', '\n')
}
