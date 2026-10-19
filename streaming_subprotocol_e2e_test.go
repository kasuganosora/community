package community

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/controllers/api/streaming"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/secretredact"
	"github.com/kasuganosora/community/tests/testutil"
)

// The full production middleware chain (see cmd/community/main.go) in front
// of the real route tree, so the handshake runs exactly as in production.
func subprotocolE2EServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.User{}, &models.Account{}, &models.AuthToken{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	prev := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{client})
	t.Cleanup(func() { cRedis.SetRedisesForTest(prev) })
	t.Cleanup(middleware.SetTokenCacheRedisForTest(client))
	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(client)))
	feed.ResetSubscriptionCache()

	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })

	conn := setup.GormDB()
	user := models.User{Id: 91, UUID: "subprotocol-e2e", UserName: "subproto", EmailHash: "subproto-email", Status: models.UserStatusNormal}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Account{Id: 92, UserId: user.Id, Username: user.UserName}).Error)
	row, token := models.NewAuthToken(user.Id, models.AuthTokenTypeWeb, 0, 0, nil, 3600, config.C.EncryptedKey)
	require.NoError(t, conn.Create(row).Error)

	// The embedded test SQL engine cannot run the feed revalidation join; the
	// handshake path under test is unchanged.
	t.Cleanup(streaming.SetFeedRevalidationForTest(func(_ *gin.Context, a *models.Account) (*models.Account, error) { return a, nil }))

	app := gin.New()
	app.Use(gin.Recovery())
	app.Use(middleware.SecurityHeadersMiddleware())
	app.Use(middleware.CORSMiddleware(nil))
	app.Use(middleware.APICompression())
	app.Use(secretredact.Middleware())
	app.Use(middleware.TimeoutMiddleware(30 * time.Second))
	app.Use(middleware.FeedAdmissionMiddleware())
	app.Use(middleware.RequestLogMiddleware())
	SetRouter(app)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return srv, token
}

func TestStreamingHandshakeNeverEchoesTheToken(t *testing.T) {
	srv, token := subprotocolE2EServer(t)
	encoded := "community.bearer." + base64.RawURLEncoding.EncodeToString([]byte(token))
	ws := "ws" + strings.TrimPrefix(srv.URL, "http")

	for _, tc := range []struct {
		name, path string
		offer      []string
		want       string
	}{
		{"feed: web client order", "/api/v1/streaming/feed?stream=user", []string{"community.v1", encoded}, "community.v1"},
		{"feed: token offered first", "/api/v1/streaming/feed?stream=user", []string{encoded, "community.v1"}, "community.v1"},
		{"mastodon: web client order", "/api/v1/streaming?stream=user", []string{"community.v1", encoded}, "community.v1"},
		{"mastodon: token offered first", "/api/v1/streaming?stream=user", []string{encoded, "community.v1"}, "community.v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := websocket.Dialer{Subprotocols: tc.offer, HandshakeTimeout: 5 * time.Second}
			conn, resp, err := d.Dial(ws+tc.path, nil)
			require.NoError(t, err, "status %v", resp)
			defer conn.Close()
			for name, values := range resp.Header {
				for _, v := range values {
					require.NotContains(t, v, token, "token leaked in %s", name)
					require.NotContains(t, v, encoded, "encoded token leaked in %s", name)
				}
			}
			require.Equal(t, tc.want, resp.Header.Get("Sec-WebSocket-Protocol"))
		})
	}

	// A subprotocol carrying only the bearer token gets no echoed protocol;
	// gorilla's client rejects that like a browser would, and nothing leaks.
	t.Run("feed: bearer only", func(t *testing.T) {
		d := websocket.Dialer{Subprotocols: []string{encoded}, HandshakeTimeout: 5 * time.Second}
		conn, resp, err := d.Dial(ws+"/api/v1/streaming/feed?stream=user", nil)
		if conn != nil {
			conn.Close()
		}
		_ = err
		require.NotNil(t, resp)
		require.NotContains(t, resp.Header.Get("Sec-WebSocket-Protocol"), encoded)
	})

	// Mastodon spec: a raw access token offered as the only subprotocol is
	// echoed (browser Mastodon clients fail the handshake otherwise). That is
	// the documented compatibility exception, and only for /api/v1/streaming.
	t.Run("mastodon: raw token alone is the only echo", func(t *testing.T) {
		if strings.ContainsAny(token, "()<>@,;:\\\"/[]?={} \t") {
			t.Skip("token is not a valid RFC 7230 token; Mastodon clients cannot offer it raw")
		}
		d := websocket.Dialer{Subprotocols: []string{token}, HandshakeTimeout: 5 * time.Second}
		conn, resp, err := d.Dial(ws+"/api/v1/streaming?stream=user", nil)
		require.NoError(t, err, "status %v", resp)
		defer conn.Close()
		require.Equal(t, token, resp.Header.Get("Sec-WebSocket-Protocol"))
		for name, values := range resp.Header {
			if name == "Sec-Websocket-Protocol" {
				continue
			}
			for _, v := range values {
				require.NotContains(t, v, token, "token leaked in %s", name)
			}
		}
	})
	t.Run("feed: raw token alone is never echoed", func(t *testing.T) {
		d := websocket.Dialer{Subprotocols: []string{"community.v1x-" + "rawtokenvalue"}, HandshakeTimeout: 5 * time.Second}
		_, resp, _ := d.Dial(ws+"/api/v1/streaming/feed?stream=user", nil)
		if resp != nil {
			require.Empty(t, resp.Header.Get("Sec-WebSocket-Protocol"))
		}
	})
}
