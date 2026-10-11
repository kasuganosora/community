package middleware

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type authCacheCommands struct{ sets atomic.Int32 }

func (h *authCacheCommands) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *authCacheCommands) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *authCacheCommands) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "set" {
			h.sets.Add(1)
		}
		return next(ctx, cmd)
	}
}

// Accept TCP and consume commands forever without responding. This reproduces
// a stopped Redis process; connection-refused tests cannot expose read stalls.
func stalledAuthRedis(t *testing.T) (*authCacheCommands, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var mu sync.Mutex
	var connections []net.Conn
	accepted := &atomic.Int32{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			mu.Lock()
			connections = append(connections, conn)
			mu.Unlock()
			go func() { _, _ = io.Copy(io.Discard, conn) }()
		}
	}()
	client := redis.NewClient(&redis.Options{Addr: listener.Addr().String(), ContextTimeoutEnabled: true, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, MaxRetries: 1})
	hook := &authCacheCommands{}
	client.AddHook(hook)
	old := globalRedisClient
	globalRedisClientOnce.Do(func() {})
	globalRedisClient = client
	t.Cleanup(func() {
		globalRedisClient = old
		_ = client.Close()
		_ = listener.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range connections {
			_ = conn.Close()
		}
	})
	return hook, accepted
}

func TestAuthCacheStalledRedisFallsBackWithinBudgetAndKeepsAuthorization(t *testing.T) {
	setup := testutil.NewTestSetup(t, testutil.WithModels(&models.User{}, &models.Account{}, &models.AuthToken{}))
	database.SetTestMode()
	database.SetTestDB(setup.GormDB())
	t.Cleanup(database.ResetTestDB)
	hook, accepted := stalledAuthRedis(t)
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "0123456789abcdef0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })
	conn := setup.GormDB()
	user := models.User{Id: 810, UUID: "cache-timeout", UserName: "cachetimeout", EmailHash: "cache-timeout"}
	account := models.Account{Id: 811, UserId: user.Id, Username: user.UserName}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&user).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)
	raw := (models.AuthTokenData{UserId: user.Id, AuthType: models.AuthTokenTypeWeb}).GetTokenStr(config.C.EncryptedKey)
	row := models.AuthToken{UserId: user.Id, Token: strings.TrimPrefix(tokenCacheKey(raw), "token_cache:")}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&row).Error)
	router := gin.New()
	router.Use(AuthMiddleware())
	router.GET("/protected", func(c *gin.Context) { c.Status(204) })
	request := func() int {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		response := httptest.NewRecorder()
		start := time.Now()
		router.ServeHTTP(response, req)
		require.Less(t, time.Since(start), time.Second, "optional Redis cache must not stall SQL authorization for seconds")
		return response.Code
	}
	require.Equal(t, 204, request(), "cache timeout must not cancel the independent SQL authorization")
	require.Positive(t, accepted.Load(), "fault fixture must accept actual TCP")
	require.Zero(t, hook.sets.Load(), "a failed Redis GET must skip cache SET")
	require.NoError(t, conn.Model(&user).UpdateColumn("is_ban", true).Error)
	require.Equal(t, 401, request(), "current SQL account moderation must remain enforced")
	require.NoError(t, conn.Model(&user).UpdateColumn("is_ban", false).Error)
	require.NoError(t, conn.Model(&row).UpdateColumn("revoke", time.Now()).Error)
	require.Equal(t, 401, request(), "SQL revocation must remain enforced during Redis outage")
	require.Zero(t, hook.sets.Load())
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := getTokenWithCacheContext(cancelled, raw)
	require.ErrorIs(t, err, context.Canceled)
}
