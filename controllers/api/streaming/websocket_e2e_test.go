package streaming

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/kasuganosora/community/db"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// e2eRedis prefers a real Redis server (pub/sub semantics, PUBSUB CHANNELS,
// separate subscriber connections) and falls back to miniredis.
func e2eRedis(t *testing.T) *redis.Client {
	t.Helper()
	var client *redis.Client
	if testutil.IsRedisAvailable() {
		client = redis.NewClient(&redis.Options{Addr: testutil.GetRedisAddr(), Password: testutil.GetRedisPassword(), DB: testutil.GetRedisDB()})
	} else {
		mr, err := miniredis.Run()
		require.NoError(t, err)
		t.Cleanup(mr.Close)
		client = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	}
	t.Cleanup(func() { _ = client.Close() })
	prev := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{client})
	t.Cleanup(func() { cRedis.SetRedisesForTest(prev) })
	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(client)))
	feed.ResetSubscriptionCache()
	return client
}

func e2eServer(t *testing.T, account *models.Account) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/streaming", func(c *gin.Context) {
		if account != nil {
			c.Set("account", account)
		}
	}, Streaming)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func e2eDial(t *testing.T, srv *httptest.Server, query string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/streaming?" + query
	return websocket.DefaultDialer.Dial(url, nil)
}

func e2eWaitSubscribed(t *testing.T, client *redis.Client, channel string) {
	t.Helper()
	require.Eventually(t, func() bool {
		n, err := client.PubSubNumSub(context.Background(), channel).Result()
		return err == nil && n[channel] > 0
	}, 3*time.Second, 20*time.Millisecond, "no subscriber on %s", channel)
}

// Mastodon clients select the stream with ?stream= and never send a subscribe
// message. Live c.hime.at sockets opened that way upgraded (101) but were never
// subscribed, so they received nothing. Drive the real handler, real Redis
// pub/sub and the real legacy publication path end to end.
func TestWebSocketQueryStreamReceivesLocalStatusEndToEnd(t *testing.T) {
	setupTestDB(t)
	client := e2eRedis(t)
	require.NoError(t, feed.ConfigureRuntime("shadow"))

	author := &models.Account{Username: "e2eauthor"}
	require.NoError(t, db.GetDB().Create(author).Error)
	srv := e2eServer(t, nil)

	for _, stream := range []string{"public", "public:local"} {
		ws, resp, err := e2eDial(t, srv, "stream="+stream)
		require.NoError(t, err, stream)
		require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
		defer ws.Close()
		name := "subscribed:feed:public:all"
		if stream == "public:local" {
			name = "subscribed:feed:public:local"
		}
		e2eWaitSubscribed(t, client, name)

		status := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "hello " + stream}}
		require.NoError(t, models.CreateStatusCommitted(db.GetDB(), status))

		require.NoError(t, ws.SetReadDeadline(time.Now().Add(5*time.Second)))
		for {
			_, data, err := ws.ReadMessage()
			require.NoError(t, err, "no update on %s", stream)
			var frame StreamingResponse
			require.NoError(t, json.Unmarshal(data, &frame), string(data))
			if frame.Event != "update" {
				continue
			}
			require.Equal(t, []string{stream}, frame.Stream)
			require.Contains(t, frame.Payload, `"id":"`+strconv.FormatUint(status.Id, 10)+`"`)
			break
		}
	}
}

// A remote reblog (Announce) is not local and must never reach public:local.
func TestWebSocketPublicLocalSkipsRemoteStatus(t *testing.T) {
	setupTestDB(t)
	client := e2eRedis(t)
	require.NoError(t, feed.ConfigureRuntime("shadow"))
	domain := "remote.example"
	remote := &models.Account{Username: "far", Domain: &domain}
	require.NoError(t, db.GetDB().Create(remote).Error)
	srv := e2eServer(t, nil)
	ws, _, err := e2eDial(t, srv, "stream=public:local")
	require.NoError(t, err)
	defer ws.Close()
	e2eWaitSubscribed(t, client, "subscribed:feed:public:local")
	status := &models.Status{AccountId: remote.Id, Domain: &domain, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "far"}}
	require.NoError(t, models.CreateStatusCommitted(db.GetDB(), status))
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(700*time.Millisecond)))
	_, data, err := ws.ReadMessage()
	require.Error(t, err, "remote status leaked into public:local: %s", data)
}

// stream=user without a token is rejected before the upgrade instead of
// returning 101 and then staying silent.
func TestWebSocketQueryStreamRequiresLoginBeforeUpgrade(t *testing.T) {
	setupTestDB(t)
	e2eRedis(t)
	srv := e2eServer(t, nil)
	for _, stream := range []string{"user", "user:notification", "direct"} {
		_, resp, err := e2eDial(t, srv, "stream="+stream)
		require.Error(t, err, stream)
		require.NotNil(t, resp, stream)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode, stream)
	}
	_, resp, err := e2eDial(t, srv, "stream=nope")
	require.Error(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// The subscribe-message protocol keeps working, also alongside ?stream=.
func TestWebSocketSubscribeMessageStillWorks(t *testing.T) {
	setupTestDB(t)
	client := e2eRedis(t)
	srv := e2eServer(t, nil)
	ws, _, err := e2eDial(t, srv, "")
	require.NoError(t, err)
	defer ws.Close()
	require.NoError(t, ws.WriteJSON(map[string]string{"type": "subscribe", "stream": "public:local"}))
	e2eWaitSubscribed(t, client, "subscribed:feed:public:local")
}
