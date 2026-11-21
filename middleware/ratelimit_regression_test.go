package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRateLimiterHonorsPerIPAndPerUserTiers(t *testing.T) {
	cfg := RateLimiterConfig{
		GlobalRPS:   1000,
		GlobalBurst: 1500,
		PerIPRPM:    12, // burst = max(1, 12/6) = 2
		PerUserRPM:  6,  // burst = max(1, 6/6) = 1
	}

	t.Run("memory limiter enforces ip and user bursts", func(t *testing.T) {
		lim := NewMemoryRateLimiter(cfg)
		require.True(t, lim.Allow("ip:1.2.3.4"))
		require.True(t, lim.Allow("ip:1.2.3.4"))
		require.False(t, lim.Allow("ip:1.2.3.4"), "3rd IP request must exceed burst=2 instead of inheriting GlobalBurst=1500")

		require.True(t, lim.Allow("user:42"))
		require.False(t, lim.Allow("user:42"), "2nd user request must exceed burst=1 instead of inheriting GlobalBurst=1500")
	})

	t.Run("redis multi-level pipeline enforces user tier in single RTT", func(t *testing.T) {
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = client.Close() })

		rl := NewRedisRateLimiter(client, cfg)
		keys := []string{"global", "ip:10.0.0.1", "user:99"}
		require.Equal(t, -1, rl.allowMulti(context.Background(), keys))
		// Second immediate call hits user:99 burst=1 first (global=1500, ip=2).
		require.Equal(t, 2, rl.allowMulti(context.Background(), keys))
	})

	t.Run("multi-level middleware with miniredis returns 429 on user burst exhaustion", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = client.Close() })

		handler := MultiLevelRateLimitMiddleware(client)
		// Default PerUserRPM=300 => burst=50.
		for range 50 {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/timelines/home", nil)
			c.Set("account", &models.Account{Id: 77})
			handler(c)
			require.False(t, c.IsAborted())
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/timelines/home", nil)
		c.Set("account", &models.Account{Id: 77})
		handler(c)
		require.True(t, c.IsAborted())
		require.Equal(t, http.StatusTooManyRequests, w.Code)
		require.Contains(t, w.Body.String(), "user rate limit exceeded")
	})
}
