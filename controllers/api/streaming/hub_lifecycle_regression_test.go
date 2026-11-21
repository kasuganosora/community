package streaming

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	redisdb "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/feed"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func findSameShardChannel(base, prefix string) string {
	want := shardIndexForChannel(base)
	for i := 0; i < 1000; i++ {
		cand := fmt.Sprintf("%s-%d", prefix, i)
		if cand != base && shardIndexForChannel(cand) == want {
			return cand
		}
	}
	panic("no same-shard channel found")
}

func findDifferentShardChannel(base, prefix string) string {
	avoid := shardIndexForChannel(base)
	for i := 0; i < 1000; i++ {
		cand := fmt.Sprintf("%s-%d", prefix, i)
		if shardIndexForChannel(cand) != avoid {
			return cand
		}
	}
	panic("no different-shard channel found")
}

func useUniversalRedis(t *testing.T, client redis.UniversalClient) {
	t.Helper()
	prev := redisdb.HasUniversalRedis()
	var old redis.UniversalClient
	if prev {
		old = redisdb.GetUniversalRedis()
	}
	redisdb.SetUniversalRedis(client)
	t.Cleanup(func() {
		if prev {
			redisdb.SetUniversalRedis(old)
		} else {
			redisdb.SetUniversalRedis(nil)
		}
	})
}

func TestHubStaleRoomDetachAfterReplacementRetainsWireSubscription(t *testing.T) {
	resetFeedLifecycle(t)
	client := setupRedisForTest(t)
	useUniversalRedis(t, client)

	ctx := context.Background()
	chKeep := "subscribed:feed:synth:keep-alive"
	chTarget := findSameShardChannel(chKeep, "subscribed:feed:synth:target")

	_, stopKeep, err := subscribeFeedHints(ctx, []string{chKeep})
	require.NoError(t, err)
	defer stopKeep()

	oldSub, _, err := subscribeFeedHints(ctx, []string{chTarget})
	require.NoError(t, err)

	roomKey := fmt.Sprintf("%p/%s", client, chTarget)
	b := bucketForRoomKey(roomKey)
	b.mu.Lock()
	oldRoom := b.rooms[roomKey]
	require.NotNil(t, oldRoom)
	oldShard := oldRoom.shard
	require.NotNil(t, oldShard)
	delete(oldRoom.hintSubs, oldSub)
	delete(b.rooms, roomKey)
	b.mu.Unlock()

	newSub, stopNew, err := subscribeFeedHints(ctx, []string{chTarget})
	require.NoError(t, err)
	defer stopNew()

	b.mu.RLock()
	newRoom := b.rooms[roomKey]
	b.mu.RUnlock()
	require.NotNil(t, newRoom)
	require.NotSame(t, oldRoom, newRoom)
	require.Same(t, oldShard, newRoom.shard)

	detachRoomFromShard(oldShard, chTarget, oldRoom, true)

	counts, err := client.PubSubNumSub(ctx, chTarget).Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), counts[chTarget], "stale room detach must not unsubscribe the replacement")

	receivers, err := client.Publish(ctx, chTarget, `{"type":"update","payload":{"id":"9001"}}`).Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), receivers)

	select {
	case notice := <-newSub.ch:
		require.Equal(t, "update", notice.Event)
		require.Equal(t, "9001", notice.ID)
	case <-time.After(time.Second):
		t.Fatal("replacement subscriber did not receive the published message")
	}
}

func TestHubUnrelatedShardCleanupNonblockingUnderStalledNetwork(t *testing.T) {
	resetFeedLifecycle(t)
	working := setupRedisForTest(t)
	useUniversalRedis(t, working)

	chHealthy := "subscribed:feed:synth:healthy"
	chStalled := findDifferentShardChannel(chHealthy, "subscribed:feed:synth:stalled")
	_, stopHealthy, err := subscribeFeedHints(context.Background(), []string{chHealthy})
	require.NoError(t, err)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	stalled := redis.NewClient(&redis.Options{
		Addr:                  ln.Addr().String(),
		ContextTimeoutEnabled: true,
		ReadTimeout:           time.Second,
	})
	defer stalled.Close()
	redisdb.SetUniversalRedis(stalled)

	stalledDone := make(chan error, 1)
	go func() {
		_, cleanup, err := subscribeFeedHints(context.Background(), []string{chStalled})
		if cleanup != nil {
			cleanup()
		}
		stalledDone <- err
	}()
	select {
	case c := <-accepted:
		defer c.Close()
	case <-time.After(time.Second):
		t.Fatal("stalled subscribe did not connect")
	}

	cleaned := make(chan struct{})
	go func() {
		stopHealthy()
		close(cleaned)
	}()
	select {
	case <-cleaned:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("unrelated shard cleanup blocked on a stalled shard")
	}
	CloseFeedTopics()
	select {
	case err := <-stalledDone:
		require.Error(t, err)
	case <-time.After(4 * time.Second):
		t.Fatal("stalled subscribe did not exit on shutdown")
	}
}

// subscribeFailConn forwards bytes, then fails the Write that carries SUBSCRIBE
// for one channel after the server has already accepted that command.
type subscribeFailConn struct {
	net.Conn
	mu      sync.Mutex
	channel string
	armed   bool
}

func (c *subscribeFailConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, err := c.Conn.Write(p)
	if err != nil {
		return n, err
	}
	if !c.armed && isSubscribeWrite(p, c.channel) {
		c.armed = true
		return len(p), fmt.Errorf("synthetic subscribe write failure")
	}
	return n, nil
}

func isSubscribeWrite(p []byte, channel string) bool {
	body := string(p)
	return containsAll(body, "$9\r\nsubscribe\r\n", channel) && !containsAll(body, "unsubscribe")
}

func containsAll(body string, parts ...string) bool {
	for _, part := range parts {
		if !stringContains(body, part) {
			return false
		}
	}
	return true
}

func stringContains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || len(s) > 0 && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()))
}

func TestHubSubscribeWriteFailureUnsubscribesSharedShard(t *testing.T) {
	resetFeedLifecycle(t)
	mrClient := setupRedisForTest(t)
	addr := mrClient.Options().Addr
	chKeep := "subscribed:feed:synth:write-keep"
	chFail := findSameShardChannel(chKeep, "subscribed:feed:synth:write-fail")

	failing := redis.NewClient(&redis.Options{
		Addr: addr,
		Dialer: func(ctx context.Context, network, dialAddr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, dialAddr)
			if err != nil {
				return nil, err
			}
			return &subscribeFailConn{Conn: c, channel: chFail}, nil
		},
	})
	t.Cleanup(func() { _ = failing.Close() })
	useUniversalRedis(t, failing)
	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(failing)))

	ctx := context.Background()
	_, stopKeep, err := subscribeFeedHints(ctx, []string{chKeep})
	require.NoError(t, err)
	defer stopKeep()

	_, _, err = subscribeFeedHints(ctx, []string{chFail})
	require.Error(t, err)

	require.Eventually(t, func() bool {
		counts, err := mrClient.PubSubNumSub(ctx, chFail, chKeep).Result()
		return err == nil && counts[chFail] == 0 && counts[chKeep] == 1
	}, 2*time.Second, 10*time.Millisecond, "failed subscribe leaked a wire subscription or dropped the shared shard")
}

func TestHubAckTimeoutUnsubscribesSharedShard(t *testing.T) {
	resetFeedLifecycle(t)
	previous := shardReadyTimeout
	shardReadyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { shardReadyTimeout = previous })

	direct := setupRedisForTest(t)
	chKeep := "subscribed:feed:synth:ack-keep"
	chDrop := findSameShardChannel(chKeep, "subscribed:feed:synth:ack-drop")
	proxy := newDropSubscribeProxy(t, direct.Options().Addr, chDrop)
	proxied := redis.NewClient(&redis.Options{Addr: proxy})
	t.Cleanup(func() { _ = proxied.Close() })
	useUniversalRedis(t, proxied)

	ctx := context.Background()
	_, stopKeep, err := subscribeFeedHints(ctx, []string{chKeep})
	require.NoError(t, err)
	defer stopKeep()

	_, _, err = subscribeFeedHints(ctx, []string{chDrop})
	require.Error(t, err)

	require.Eventually(t, func() bool {
		counts, err := direct.PubSubNumSub(ctx, chDrop, chKeep).Result()
		return err == nil && counts[chDrop] == 0 && counts[chKeep] == 1
	}, 2*time.Second, 10*time.Millisecond, "ack timeout leaked the channel or dropped the shared shard")
}

func newDropSubscribeProxy(t *testing.T, upstream, dropChannel string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", upstream)
			if err != nil {
				_ = client.Close()
				continue
			}
			go func() { _, _ = copyBytes(server, client) }()
			go func() { _, _ = copyDropSubscribe(client, server, dropChannel) }()
		}
	}()
	return ln.Addr().String()
}

func copyBytes(dst, src net.Conn) (int64, error) {
	buf := make([]byte, 4096)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			written, werr := dst.Write(buf[:n])
			total += int64(written)
			if werr != nil {
				return total, werr
			}
		}
		if err != nil {
			return total, err
		}
	}
}

func copyDropSubscribe(dst, src net.Conn, channel string) (int64, error) {
	buf := make([]byte, 4096)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			if stringContains(chunk, "$9\r\nsubscribe\r\n") && stringContains(chunk, channel) {
				continue
			}
			written, werr := dst.Write(buf[:n])
			total += int64(written)
			if werr != nil {
				return total, werr
			}
		}
		if err != nil {
			return total, err
		}
	}
}
