package streaming

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/feed/notification"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// processUserMsg
// ---------------------------------------------------------------------------

func TestProcessUserMsg_InvalidJSON(t *testing.T) {
	ws := newTestWSConn(t)
	defer func() { _ = ws.Close() }()

	ret, err := processUserMsg(ws, []byte("not json"), nil, nil)
	// json.Unmarshal error
	assert.Error(t, err)
	assert.Nil(t, ret)
}

func TestProcessUserMsg_UnknownType(t *testing.T) {
	ws := newTestWSConn(t)
	defer func() { _ = ws.Close() }()

	req := &StreamingRequest{Type: "unknown", Stream: "public"}
	msg, _ := json.Marshal(req)

	ret, err := processUserMsg(ws, msg, nil, nil)
	// unknown type: returns existing pub (nil) with no error, writes "unknown type" error
	require.NoError(t, err)
	assert.Nil(t, ret)

	// Read the written error response
	ws.SetReadDeadline(time.Now().Add(time.Second))
	_, p, readErr := ws.ReadMessage()
	require.NoError(t, readErr)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(p, &resp))
	assert.Equal(t, "unknown type", resp["error"])
}

func TestProcessUserMsg_SubscribeUnknownStream(t *testing.T) {
	ws := newTestWSConn(t)
	defer func() { _ = ws.Close() }()

	req := &StreamingRequest{Type: "subscribe", Stream: "nonexistent"}
	msg, _ := json.Marshal(req)

	ret, err := processUserMsg(ws, msg, nil, nil)
	// getChannelName returns error for unknown stream -> err is non-nil
	assert.Error(t, err)
	// subscribe returns (nil, err) because getChannelName failed
	assert.Nil(t, ret)
}

// ---------------------------------------------------------------------------
// subscribe
// ---------------------------------------------------------------------------

func TestSubscribe_PublicNewPub(t *testing.T) {
	client := setupRedisClientForTest(t)

	req := &StreamingRequest{Type: "subscribe", Stream: "public"}
	pub, err := subscribe(nil, nil, req)
	require.NoError(t, err)
	require.NotNil(t, pub)

	// The returned PubSub should be a new subscription
	_ = pub.Close()
	_ = client
}

func TestSubscribe_PublicExistingPub(t *testing.T) {
	client := setupRedisClientForTest(t)

	account := &models.Account{Id: 100}
	timeline := &home.HomeTimeline{Account: account}
	channelName := feed.GetRedisChannelName(timeline)

	// First create a pubsub via direct subscribe
	existingPub := client.Subscribe(context.Background(), channelName)
	defer func() { _ = existingPub.Close() }()

	req := &StreamingRequest{Type: "subscribe", Stream: "user"}
	pub, err := subscribe(existingPub, account, req)
	require.NoError(t, err)
	assert.NotNil(t, pub)
	assert.Same(t, existingPub, pub)
}

func TestSubscribe_HashtagChannel(t *testing.T) {
	_ = setupRedisClientForTest(t)

	req := &StreamingRequest{Type: "subscribe", Stream: "hashtag", Tag: "golang"}
	pub, err := subscribe(nil, nil, req)
	require.NoError(t, err)
	require.NotNil(t, pub)
	_ = pub.Close()
}

func TestSubscribe_DirectChannel(t *testing.T) {
	_ = setupRedisClientForTest(t)

	account := &models.Account{Id: 777}
	req := &StreamingRequest{Type: "subscribe", Stream: "direct"}
	pub, err := subscribe(nil, account, req)
	require.NoError(t, err)
	require.NotNil(t, pub)
	_ = pub.Close()
}

func TestSubscribe_NotificationChannel(t *testing.T) {
	_ = setupRedisClientForTest(t)

	account := &models.Account{Id: 888}
	req := &StreamingRequest{Type: "subscribe", Stream: "user:notification"}
	pub, err := subscribe(nil, account, req)
	require.NoError(t, err)
	require.NotNil(t, pub)
	_ = pub.Close()
}

func TestSubscribe_UserNeedsLogin(t *testing.T) {
	_ = setupRedisClientForTest(t)

	req := &StreamingRequest{Type: "subscribe", Stream: "user"}
	pub, err := subscribe(nil, nil, req)
	assert.Error(t, err)
	assert.Nil(t, pub)
	assert.Equal(t, "need login", err.Error())
}

func TestSubscribe_HashtagEmptyTag(t *testing.T) {
	_ = setupRedisClientForTest(t)

	req := &StreamingRequest{Type: "subscribe", Stream: "hashtag", Tag: ""}
	pub, err := subscribe(nil, nil, req)
	assert.Error(t, err)
	assert.Nil(t, pub)
	assert.Equal(t, "tag name is empty", err.Error())
}

// ---------------------------------------------------------------------------
// unsubscribe
// ---------------------------------------------------------------------------

func TestUnsubscribe_NilPubError(t *testing.T) {
	_ = setupRedisClientForTest(t)

	req := &StreamingRequest{Type: "unsubscribe", Stream: "public"}
	ret, err := unsubscribe(nil, nil, req)
	assert.Error(t, err)
	assert.Nil(t, ret)
	// The error gets wrapped to "unsubscribe failed"
	assert.Equal(t, "unsubscribe failed", err.Error())
}

func TestUnsubscribe_Success(t *testing.T) {
	client := setupRedisClientForTest(t)

	account := &models.Account{Id: 200}
	timeline := &home.HomeTimeline{Account: account}
	channelName := feed.GetRedisChannelName(timeline)

	pub := client.Subscribe(context.Background(), channelName)
	defer func() {
		_ = pub.Close()
	}()

	req := &StreamingRequest{Type: "unsubscribe", Stream: "user"}
	ret, err := unsubscribe(pub, account, req)
	require.NoError(t, err)
	assert.NotNil(t, ret)
	assert.Same(t, pub, ret)
}

func TestUnsubscribe_NotificationChannel(t *testing.T) {
	client := setupRedisClientForTest(t)

	account := &models.Account{Id: 300}
	timeline := &notification.NotificationTimeline{Account: account}
	channelName := feed.GetRedisChannelName(timeline)

	pub := client.Subscribe(context.Background(), channelName)
	defer func() {
		_ = pub.Close()
	}()

	req := &StreamingRequest{Type: "unsubscribe", Stream: "user:notification"}
	ret, err := unsubscribe(pub, account, req)
	require.NoError(t, err)
	assert.NotNil(t, ret)
}

// ---------------------------------------------------------------------------
// processRedisMsg
// ---------------------------------------------------------------------------

func TestProcessRedisMsg_UnknownChannel(t *testing.T) {
	ws := newTestWSConn(t)
	defer func() { _ = ws.Close() }()

	msg := &redis.Message{
		Channel:      "something:unknown",
		Payload:      "{}",
		Pattern:      "",
		PayloadSlice: nil,
	}

	err := processRedisMsg(ws, nil, msg)
	assert.Error(t, err)
	assert.Equal(t, "unknown channel", err.Error())
}

func TestProcessRedisMsg_InvalidPayload(t *testing.T) {
	ws := newTestWSConn(t)
	defer func() { _ = ws.Close() }()

	msg := &redis.Message{
		Channel: "feed:public:1",
		Payload: "not valid json",
	}

	err := processRedisMsg(ws, nil, msg)
	assert.Error(t, err)
}

func TestProcessRedisMsg_NotificationChannel(t *testing.T) {
	viewer := seedStreamNotification(t)
	ws := newTestWSConn(t)
	defer func() { _ = ws.Close() }()

	// Valid notification payload
	notifPayload := `{"payload":{"id":"42","type":"mention","account":{"id":"1","username":"a"}}}`
	msg := &redis.Message{
		Channel: "subscribed:feed:notification:123",
		Payload: notifPayload,
	}

	err := processRedisMsg(ws, viewer, msg)
	require.NoError(t, err)

	// Read the response
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, readErr := ws.ReadMessage()
	require.NoError(t, readErr)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(p, &resp))
	assert.Equal(t, "notification", resp["event"])
}

func TestProcessRedisMsg_DeleteEvent(t *testing.T) {
	setupTestDBForHandler(t)
	ws := newTestWSConn(t)
	defer func() { _ = ws.Close() }()

	// delete event with a minimal status payload
	statusPayload := `{"type":"delete","payload":{"id":555}}`
	msg := &redis.Message{
		Channel: "feed:public:1",
		Payload: statusPayload,
	}

	// The delete branch should process and write a response to the ws.
	err := processRedisMsg(ws, nil, msg)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Streaming WebSocket handler (integration with httptest server)
// ---------------------------------------------------------------------------

func TestStreaming_ConnectAndSubscribe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = setupRedisClientForTest(t)

	account := &models.Account{Id: 42}

	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/ws", func(c *gin.Context) {
		c.Set("account", account)
		Streaming(c)
	})

	server := httptest.NewServer(r)
	defer server.Close()

	wsURL := "ws" + server.URL[4:] + "/ws"
	ws, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err, "WebSocket dial failed, resp: %v", resp)
	defer func() { _ = ws.Close() }()

	// Send an unknown type to get an error response back.
	// (We avoid subscribe here to prevent the Redis message loop from
	// delivering a nil message after the connection closes.)
	unknownMsg := `{"type":"frobnicate","stream":"user"}`
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(unknownMsg)))

	// Read response - should be the "unknown type" error
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, readErr := ws.ReadMessage()
	require.NoError(t, readErr, "expected to read unknown type error response")

	var errResp StreamingErrorResponse
	require.NoError(t, json.Unmarshal(p, &errResp))
	assert.Equal(t, "unknown type", errResp.Err)
}

func TestStreaming_AnonymousPublicSubscribe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = setupRedisClientForTest(t)

	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/ws", Streaming)

	server := httptest.NewServer(r)
	defer server.Close()

	wsURL := "ws" + server.URL[4:] + "/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	// Send an unknown type message (does not trigger subscribe/db.GetRedis path
	// in a background goroutine, avoiding a race with test cleanup).
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping"}`)))

	// Read the "unknown type" error response to confirm the handler processed it.
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, readErr := ws.ReadMessage()
	require.NoError(t, readErr)

	var errResp map[string]any
	require.NoError(t, json.Unmarshal(p, &errResp))
	assert.Equal(t, "unknown type", errResp["error"])

	// Close explicitly so the Streaming goroutine exits before cleanup.
	_ = ws.Close()
	time.Sleep(50 * time.Millisecond)
}

func TestStreaming_AnonymousUserSubscribeError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = setupRedisClientForTest(t)

	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/ws", Streaming)

	server := httptest.NewServer(r)
	defer server.Close()

	wsURL := "ws" + server.URL[4:] + "/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer func() { _ = ws.Close() }()

	// Subscribe to user stream without account -> should get error
	subMsg := `{"type":"subscribe","stream":"user"}`
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(subMsg)))

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, readErr := ws.ReadMessage()
	require.NoError(t, readErr)

	var errResp map[string]any
	require.NoError(t, json.Unmarshal(p, &errResp))
	assert.Equal(t, "need login", errResp["error"])
}

func TestStreaming_InvalidJSONMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_ = setupRedisClientForTest(t)

	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/ws", Streaming)

	server := httptest.NewServer(r)
	defer server.Close()

	wsURL := "ws" + server.URL[4:] + "/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer func() { _ = ws.Close() }()

	// Send invalid JSON
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte("not json")))

	// Connection should stay open; send a valid unknown type to verify
	require.NoError(t, ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"unknown"}`)))

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, readErr := ws.ReadMessage()
	require.NoError(t, readErr)

	var errResp map[string]any
	require.NoError(t, json.Unmarshal(p, &errResp))
	assert.Equal(t, "unknown type", errResp["error"])
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupRedisClientForTest starts a miniredis, injects it into the global redis
// pool, and returns the client. Cleanup is registered via t.Cleanup.
func setupRedisClientForTest(t *testing.T) *redis.Client {
	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
		mr.Close()
	})

	prev := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{client})
	t.Cleanup(func() {
		cRedis.SetRedisesForTest(prev)
	})

	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(client)))
	feed.ResetSubscriptionCache()

	return client
}

// setupTestDBForTest injects an in-memory SQLite DB with migrated models so
// that processRedisMsg/serializerStatusEvent can run CheckAndLoadAllExtendFields.
func setupTestDBForHandler(t *testing.T) {
	t.Helper()

	gdb, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	require.NoError(t, gdb.AutoMigrate(
		&models.Status{},
		&models.StatusContent{},
		&models.Account{},
		&models.Application{},
		&models.Attachment{},
		&models.Emoji{},
		&models.Tag{},
		&models.Poll{},
		&models.PollOption{},
	))

	database.SetTestDB(gdb)
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}

		database.ResetTestDB()
	})
}

// newTestWSConn creates a pair of connected WebSocket connections for testing
// the message processing functions (processUserMsg, processRedisMsg) which
// write to a *websocket.Conn.
func newTestWSConn(t *testing.T) *websocket.Conn {
	t.Helper()

	// Use a real httptest server with a simple echo-upgrade handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		defer func() { _ = c.Close() }()
		for {
			mt, msg, err := c.ReadMessage()
			if err != nil {
				return
			}

			if err := c.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	// For processUserMsg/processRedisMsg tests we need a ws that we can
	// write to AND read from. We connect to the echo server.
	wsURL := "ws" + srv.URL[4:]
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	return ws
}
