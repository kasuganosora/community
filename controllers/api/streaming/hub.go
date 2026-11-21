package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed"
	"github.com/redis/go-redis/v9"
)

const (
	numStreamBuckets = 64
	numPubSubShards  = 8
)

type hintSubscriber struct {
	ch        chan feedNotice
	closed    chan struct{}
	closeOnce sync.Once
	closeChan bool
}

func (s *hintSubscriber) signalDrop() {
	s.closeOnce.Do(func() {
		close(s.closed)
		if s.closeChan {
			close(s.ch)
		}
	})
}

type rawStreamMsg struct {
	Channel string
	Payload string
}

type rawSubscriber struct {
	ch        chan rawStreamMsg
	closed    chan struct{}
	closeOnce sync.Once
}

func (s *rawSubscriber) push(msg rawStreamMsg) {
	select {
	case s.ch <- msg:
	default:
		// Non-blocking drop on slow SSE consumer (openBshi Channel.Push pattern)
		// so a stalled reader never blocks room broadcast across healthy peers.
	}
}

func (s *rawSubscriber) signalDrop() {
	s.closeOnce.Do(func() {
		close(s.closed)
	})
}

// streamRoom is an openBshi-style room holding all local subscribers watching
// a single Redis channel (both FeedStreaming hint/ID subscribers and Mastodon
// SSE raw message subscribers).
type streamRoom struct {
	key       string
	channel   string
	shard     *pubsubShard
	ready     chan struct{}
	readyOnce sync.Once
	err       error
	hintSubs  map[*hintSubscriber]struct{}
	rawSubs   map[*rawSubscriber]struct{}
}

func (r *streamRoom) empty() bool {
	return len(r.hintSubs) == 0 && len(r.rawSubs) == 0
}

type streamBucket struct {
	mu    sync.RWMutex
	rooms map[string]*streamRoom
}

var streamBuckets [numStreamBuckets]streamBucket

func bucketForRoomKey(key string) *streamBucket {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &streamBuckets[h.Sum32()&(numStreamBuckets-1)]
}

func shardIndexForChannel(channel string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(channel))
	return h.Sum32() & (numPubSubShards - 1)
}

type pubsubShardKey struct {
	client redis.UniversalClient
	index  uint32
}

// pubsubShard multiplexes many Redis channels onto one persistent *redis.PubSub
// TCP socket (collapsing O(users * channels) sockets down to <= 8 per process).
type pubsubShard struct {
	key       pubsubShardKey
	mu        sync.Mutex
	pub       *redis.PubSub
	startOnce sync.Once
	closed    chan struct{}
	closeOnce sync.Once
	dead      bool
	rooms     map[string]*streamRoom // channel -> room
}

var shardPool = struct {
	mu     sync.Mutex
	shards map[pubsubShardKey]*pubsubShard
}{shards: map[pubsubShardKey]*pubsubShard{}}

func activeTopicCount() int {
	total := 0
	for i := range streamBuckets {
		b := &streamBuckets[i]
		b.mu.RLock()
		total += len(b.rooms)
		b.mu.RUnlock()
	}
	return total
}

func closeAllStreamHub() {
	shardPool.mu.Lock()
	shards := make([]*pubsubShard, 0, len(shardPool.shards))
	for k, s := range shardPool.shards {
		shards = append(shards, s)
		delete(shardPool.shards, k)
	}
	pubs := make([]*redis.PubSub, 0, len(shards))
	for _, s := range shards {
		s.mu.Lock()
		s.dead = true
		s.rooms = map[string]*streamRoom{}
		if s.pub != nil {
			pubs = append(pubs, s.pub)
		}
		s.mu.Unlock()
		s.closeOnce.Do(func() { close(s.closed) })
	}
	shardPool.mu.Unlock()

	var rooms []*streamRoom
	for i := range streamBuckets {
		b := &streamBuckets[i]
		b.mu.Lock()
		for _, r := range b.rooms {
			rooms = append(rooms, r)
		}
		b.rooms = nil
		b.mu.Unlock()
	}
	for _, r := range rooms {
		evictRoom(r, context.Canceled)
	}
	for _, pub := range pubs {
		_ = pub.Close()
	}
}

func acquireShardAndSubscribe(ctx context.Context, client redis.UniversalClient, channel string, room *streamRoom) (*pubsubShard, error) {
	skey := pubsubShardKey{client: client, index: shardIndexForChannel(channel)}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		shardPool.mu.Lock()
		if feedTopics.isClosed() {
			shardPool.mu.Unlock()
			return nil, context.Canceled
		}
		shard := shardPool.shards[skey]
		if shard == nil {
			// Create an empty PubSub bound to Background() so go-redis never retains
			// a short-lived caller initCtx inside PubSub; dialing happens in Subscribe below.
			shard = &pubsubShard{
				key:    skey,
				pub:    client.Subscribe(context.Background()),
				closed: make(chan struct{}),
				rooms:  map[string]*streamRoom{channel: room},
			}
			shardPool.shards[skey] = shard
		} else {
			shard.mu.Lock()
			if shard.dead {
				shard.mu.Unlock()
				delete(shardPool.shards, skey)
				shardPool.mu.Unlock()
				continue
			}
			shard.rooms[channel] = room
			shard.mu.Unlock()
		}
		pub := shard.pub
		shardPool.mu.Unlock()

		if err := pub.Subscribe(ctx, channel); err != nil {
			detachRoomFromShard(shard, channel, room, false)
			return nil, err
		}
		shard.mu.Lock()
		dead := shard.dead
		shard.mu.Unlock()
		if dead {
			_ = pub.Close()
			return nil, context.Canceled
		}
		shard.startOnce.Do(func() {
			allCh := pub.ChannelWithSubscriptions(redis.WithChannelSize(256))
			go shard.run(allCh)
		})
		return shard, nil
	}
}

func detachRoomFromShard(shard *pubsubShard, channel string, room *streamRoom, sendUnsub bool) {
	if shard == nil {
		return
	}
	var closePub *redis.PubSub
	var unsubPub *redis.PubSub
	shardPool.mu.Lock()
	shard.mu.Lock()
	if shard.rooms[channel] == room {
		delete(shard.rooms, channel)
	}
	if len(shard.rooms) == 0 && !shard.dead {
		shard.dead = true
		if shardPool.shards[shard.key] == shard {
			delete(shardPool.shards, shard.key)
		}
		closePub = shard.pub
	} else if sendUnsub && !shard.dead {
		unsubPub = shard.pub
	}
	shard.mu.Unlock()
	shardPool.mu.Unlock()

	if closePub != nil {
		shard.closeOnce.Do(func() { close(shard.closed) })
		_ = closePub.Close()
		return
	}
	if unsubPub != nil {
		unsubCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = unsubPub.Unsubscribe(unsubCtx, channel)
		cancel()
	}
}

func (s *pubsubShard) run(allCh <-chan any) {
	defer func() {
		shardPool.mu.Lock()
		s.mu.Lock()
		s.dead = true
		if shardPool.shards[s.key] == s {
			delete(shardPool.shards, s.key)
		}
		rooms := make([]*streamRoom, 0, len(s.rooms))
		for _, r := range s.rooms {
			rooms = append(rooms, r)
		}
		s.rooms = map[string]*streamRoom{}
		pub := s.pub
		s.mu.Unlock()
		shardPool.mu.Unlock()

		s.closeOnce.Do(func() { close(s.closed) })
		if pub != nil {
			_ = pub.Close()
		}
		for _, r := range rooms {
			evictRoom(r, context.Canceled)
		}
	}()

	for item := range allCh {
		switch msg := item.(type) {
		case *redis.Subscription:
			if msg.Kind == "subscribe" {
				s.mu.Lock()
				room := s.rooms[msg.Channel]
				s.mu.Unlock()
				if room != nil {
					b := bucketForRoomKey(room.key)
					b.mu.Lock()
					room.readyOnce.Do(func() { close(room.ready) })
					b.mu.Unlock()
				}
			}
		case *redis.Message:
			s.mu.Lock()
			room := s.rooms[msg.Channel]
			s.mu.Unlock()
			if room != nil {
				dispatchRoomMessage(room, msg.Channel, msg.Payload)
			}
		}
	}
}

func evictRoom(room *streamRoom, cause error) {
	b := bucketForRoomKey(room.key)
	b.mu.Lock()
	if b.rooms[room.key] == room {
		delete(b.rooms, room.key)
	}
	if room.err == nil {
		room.err = cause
	}
	room.readyOnce.Do(func() { close(room.ready) })
	hints := make([]*hintSubscriber, 0, len(room.hintSubs))
	for sub := range room.hintSubs {
		hints = append(hints, sub)
	}
	raws := make([]*rawSubscriber, 0, len(room.rawSubs))
	for sub := range room.rawSubs {
		raws = append(raws, sub)
	}
	room.hintSubs = nil
	room.rawSubs = nil
	b.mu.Unlock()

	for _, sub := range hints {
		sub.signalDrop()
	}
	for _, sub := range raws {
		sub.signalDrop()
	}
}

func parseFeedNotice(payload string) (feedNotice, bool) {
	var wire struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal([]byte(payload), &wire) != nil {
		return feedNotice{}, false
	}
	var notice feedNotice
	if wire.Type == "feed.changed" {
		var hint feed.ChangeHint
		if json.Unmarshal(wire.Payload, &hint) != nil || len(hint.Epoch) > 128 || len(hint.Revision) > 128 {
			return feedNotice{}, false
		}
		notice.Hint = hint
		return notice, true
	}
	if (wire.Type == "update" || wire.Type == "delete" || wire.Type == "status.update") && !notificationBody(wire.Payload) {
		id, ok := feedStatusID(wire.Payload)
		if !ok {
			return feedNotice{}, false
		}
		notice.Event = wire.Type
		notice.ID = id
		return notice, true
	}
	switch wire.Type {
	case "filters_changed", "notification", "conversation", "announcement", "update", "delete", "status.update":
		notice.Hint = feed.ChangeHint{Epoch: "legacy", Revision: strconv.FormatUint(legacyHintRevision.Add(1), 10)}
		return notice, true
	default:
		return feedNotice{}, false
	}
}

func dispatchRoomMessage(room *streamRoom, channel, payload string) {
	b := bucketForRoomKey(room.key)
	b.mu.RLock()
	hasHints := len(room.hintSubs) > 0
	hasRaws := len(room.rawSubs) > 0
	b.mu.RUnlock()
	if !hasHints && !hasRaws {
		return
	}

	var notice feedNotice
	var noticeOK bool
	if hasHints {
		notice, noticeOK = parseFeedNotice(payload)
	}
	rawMsg := rawStreamMsg{Channel: channel, Payload: payload}

	b.mu.RLock()
	if noticeOK {
		for sub := range room.hintSubs {
			deliverNotice(sub.ch, notice)
		}
	}
	for sub := range room.rawSubs {
		sub.push(rawMsg)
	}
	b.mu.RUnlock()
}

func ensureSubscribedRoom(ctx context.Context, client redis.UniversalClient, channel string, attach func(*streamRoom)) (*streamRoom, error) {
	key := fmt.Sprintf("%p/%s", client, channel)
	b := bucketForRoomKey(key)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if feedTopics.isClosed() {
			return nil, context.Canceled
		}
		b.mu.Lock()
		if feedTopics.isClosed() {
			b.mu.Unlock()
			return nil, context.Canceled
		}
		if b.rooms == nil {
			b.rooms = make(map[string]*streamRoom)
		}
		room := b.rooms[key]
		created := false
		if room == nil {
			room = &streamRoom{
				key:      key,
				channel:  channel,
				ready:    make(chan struct{}),
				hintSubs: map[*hintSubscriber]struct{}{},
				rawSubs:  map[*rawSubscriber]struct{}{},
			}
			b.rooms[key] = room
			created = true
		}
		b.mu.Unlock()

		if created {
			initCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			shard, err := acquireShardAndSubscribe(initCtx, client, channel, room)
			if err == nil {
				b.mu.Lock()
				room.shard = shard
				b.mu.Unlock()
				select {
				case <-room.ready:
				case <-shard.closed:
					err = context.Canceled
				case <-initCtx.Done():
					err = initCtx.Err()
				}
			}
			cancel()
			if err != nil {
				b.mu.Lock()
				room.err = err
				if b.rooms[key] == room {
					delete(b.rooms, key)
				}
				room.readyOnce.Do(func() { close(room.ready) })
				b.mu.Unlock()
				detachRoomFromShard(shard, channel, room, false)
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, ctxErr
				}
				return nil, err
			}
			// If caller canceled while created room finished subscribing and nobody else attached, clean up unused room.
			defer func(r *streamRoom, s *pubsubShard) {
				b.mu.Lock()
				unused := b.rooms[key] == r && r.empty()
				if unused {
					delete(b.rooms, key)
				}
				b.mu.Unlock()
				if unused {
					detachRoomFromShard(s, channel, r, true)
				}
			}(room, shard)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-room.ready:
		}

		b.mu.Lock()
		if err := ctx.Err(); err != nil {
			b.mu.Unlock()
			return nil, err
		}
		if room.err != nil {
			err := room.err
			b.mu.Unlock()
			return nil, err
		}
		if feedTopics.isClosed() {
			b.mu.Unlock()
			return nil, context.Canceled
		}
		if b.rooms[key] != room {
			b.mu.Unlock()
			continue
		}
		attach(room)
		b.mu.Unlock()
		return room, nil
	}
}

func detachHintSubscriber(room *streamRoom, sub *hintSubscriber) {
	b := bucketForRoomKey(room.key)
	b.mu.Lock()
	if room.hintSubs != nil {
		delete(room.hintSubs, sub)
	}
	last := room.empty() && b.rooms[room.key] == room
	var shard *pubsubShard
	if last {
		delete(b.rooms, room.key)
		shard = room.shard
	}
	b.mu.Unlock()
	if last {
		detachRoomFromShard(shard, room.channel, room, true)
	}
}

func detachRawSubscriber(room *streamRoom, sub *rawSubscriber) {
	b := bucketForRoomKey(room.key)
	b.mu.Lock()
	if room.rawSubs != nil {
		delete(room.rawSubs, sub)
	}
	last := room.empty() && b.rooms[room.key] == room
	var shard *pubsubShard
	if last {
		delete(b.rooms, room.key)
		shard = room.shard
	}
	b.mu.Unlock()
	if last {
		detachRoomFromShard(shard, room.channel, room, true)
	}
}

// subscribeFeedHints attaches a single hintSubscriber directly to multiple rooms
// with zero per-channel forwarding goroutines per client.
func subscribeFeedHints(ctx context.Context, channels []string) (*hintSubscriber, func(), error) {
	client := db.GetUniversalRedis()
	sub := &hintSubscriber{
		ch:     make(chan feedNotice, 1),
		closed: make(chan struct{}),
	}
	rooms := make([]*streamRoom, 0, len(channels))
	cleanup := func() {
		for _, r := range rooms {
			detachHintSubscriber(r, sub)
		}
	}
	for _, channel := range channels {
		room, err := ensureSubscribedRoom(ctx, client, channel, func(r *streamRoom) {
			r.hintSubs[sub] = struct{}{}
		})
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		rooms = append(rooms, room)
	}
	var once sync.Once
	return sub, func() { once.Do(cleanup) }, nil
}

// subscribeRawChannels attaches a single rawSubscriber across one or more Mastodon
// SSE channels, sharing the sharded Redis PubSub pool across concurrent viewers.
func subscribeRawChannels(ctx context.Context, channels []string) (*rawSubscriber, func(), error) {
	client := db.GetUniversalRedis()
	sub := &rawSubscriber{
		ch:     make(chan rawStreamMsg, 64),
		closed: make(chan struct{}),
	}
	rooms := make([]*streamRoom, 0, len(channels))
	cleanup := func() {
		for _, r := range rooms {
			detachRawSubscriber(r, sub)
		}
	}
	for _, channel := range channels {
		room, err := ensureSubscribedRoom(ctx, client, channel, func(r *streamRoom) {
			r.rawSubs[sub] = struct{}{}
		})
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		rooms = append(rooms, room)
	}
	var once sync.Once
	return sub, func() { once.Do(cleanup) }, nil
}
