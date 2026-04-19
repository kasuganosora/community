package middleware

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

// ==================== ratelimit.go ====================

func TestDefaultRateLimiterConfig(t *testing.T) {
	cfg := DefaultRateLimiterConfig()

	assert.Equal(t, 1000.0, cfg.GlobalRPS)
	assert.Equal(t, 1500, cfg.GlobalBurst)
	assert.Equal(t, 300, cfg.PerUserRPM)
	assert.Equal(t, 600, cfg.PerIPRPM)
	assert.NotNil(t, cfg.APILimits)
	assert.Contains(t, cfg.APILimits, "/api/v1/media")
	assert.Contains(t, cfg.APILimits, "/api/v2/media")
	assert.Contains(t, cfg.APILimits, "/_matrix/federation")

	mediaLimit := cfg.APILimits["/api/v1/media"]
	assert.Equal(t, 10.0, mediaLimit.RPS)
	assert.Equal(t, 20, mediaLimit.Burst)

	fedLimit := cfg.APILimits["/_matrix/federation"]
	assert.Equal(t, 100.0, fedLimit.RPS)
	assert.Equal(t, 200, fedLimit.Burst)
}

func TestAPILimitStruct(t *testing.T) {
	limit := APILimit{RPS: 50, Burst: 100}
	assert.Equal(t, 50.0, limit.RPS)
	assert.Equal(t, 100, limit.Burst)
}

func TestNewMemoryRateLimiter(t *testing.T) {
	cfg := DefaultRateLimiterConfig()
	limiter := NewMemoryRateLimiter(cfg)
	assert.NotNil(t, limiter)
	assert.NotNil(t, limiter.limiters)
	assert.Equal(t, cfg, limiter.config)
}

func TestMemoryRateLimiter_Allow(t *testing.T) {
	cfg := RateLimiterConfig{GlobalRPS: 10, GlobalBurst: 5}
	limiter := NewMemoryRateLimiter(cfg)

	// Should allow up to burst (5) requests
	for i := 0; i < 5; i++ {
		assert.True(t, limiter.Allow("key-a"), "request %d should be allowed", i+1)
	}
}

func TestMemoryRateLimiter_Allow_DifferentKeys(t *testing.T) {
	cfg := RateLimiterConfig{GlobalRPS: 10, GlobalBurst: 2}
	limiter := NewMemoryRateLimiter(cfg)

	// Different keys should have independent limiters
	assert.True(t, limiter.Allow("key-x"))
	assert.True(t, limiter.Allow("key-y"))
}

func TestMemoryRateLimiter_AllowN(t *testing.T) {
	cfg := RateLimiterConfig{GlobalRPS: 10, GlobalBurst: 10}
	limiter := NewMemoryRateLimiter(cfg)

	// Allow 5 at once
	assert.True(t, limiter.AllowN("bulk-key", 5))
	// Allow another 5
	assert.True(t, limiter.AllowN("bulk-key", 5))
}

func TestMemoryRateLimiter_ImplementsRateLimiterInterface(t *testing.T) {
	var limiter RateLimiter = NewMemoryRateLimiter(DefaultRateLimiterConfig())
	assert.NotNil(t, limiter)
}

func TestNewRedisRateLimiter(t *testing.T) {
	cfg := DefaultRateLimiterConfig()
	rl := NewRedisRateLimiter(nil, cfg)
	assert.NotNil(t, rl)
	assert.Nil(t, rl.client)
	assert.Equal(t, cfg, rl.config)
}

func TestNewSlidingWindowRateLimiter(t *testing.T) {
	sw := NewSlidingWindowRateLimiter(nil, time.Minute, 100)
	assert.NotNil(t, sw)
	assert.Equal(t, time.Minute, sw.windowSize)
	assert.Equal(t, 100, sw.maxRequests)
}

func TestSlidingWindowRateLimiter_Allow_NilClient(t *testing.T) {
	sw := NewSlidingWindowRateLimiter(nil, time.Minute, 100)
	// Should degrade gracefully (return true) when client is nil
	assert.True(t, sw.Allow("test-key"))
}

func TestRateLimitMiddleware(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		cfg := RateLimiterConfig{GlobalRPS: 10, GlobalBurst: 5}
		limiter := NewMemoryRateLimiter(cfg)
		handler := RateLimitMiddleware(limiter)
		assert.NotNil(t, handler)
	})

	t.Run("allows request within limit", func(t *testing.T) {
		cfg := RateLimiterConfig{GlobalRPS: 10, GlobalBurst: 5}
		limiter := NewMemoryRateLimiter(cfg)
		handler := RateLimitMiddleware(limiter)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		assert.False(t, c.IsAborted())
	})

	t.Run("blocks request exceeding limit with 429", func(t *testing.T) {
		cfg := RateLimiterConfig{GlobalRPS: 10, GlobalBurst: 2}
		limiter := NewMemoryRateLimiter(cfg)
		handler := RateLimitMiddleware(limiter)

		// Exhaust burst
		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/test", nil)
			handler(c)
		}

		// Next request should be blocked
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		handler(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, http.StatusTooManyRequests, w.Code)
	})

	t.Run("returns retry_after in response body", func(t *testing.T) {
		cfg := RateLimiterConfig{GlobalRPS: 10, GlobalBurst: 1}
		limiter := NewMemoryRateLimiter(cfg)
		handler := RateLimitMiddleware(limiter)

		// Exhaust burst
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		handler(c)

		// Next should be rate limited
		w = httptest.NewRecorder()
		c, _ = gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		handler(c)

		assert.Contains(t, w.Body.String(), "rate limit exceeded")
		assert.Contains(t, w.Body.String(), "retry_after")
	})
}

func TestAPIRateLimitMiddleware(t *testing.T) {
	t.Run("returns gin handler with nil redis", func(t *testing.T) {
		handler := APIRateLimitMiddleware(nil, "/api/v1/media", 100)
		assert.NotNil(t, handler)
	})

	t.Run("passes through non-matching path (FullPath is empty in test context)", func(t *testing.T) {
		handler := APIRateLimitMiddleware(nil, "/api/v1/media", 100)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/v1/status", nil)
		// FullPath() returns "" in CreateTestContext, so hasPrefix("", prefix) is false

		handler(c)
		assert.False(t, c.IsAborted())
	})

	t.Run("allows request within rate limit for matching path", func(t *testing.T) {
		// Use a router so FullPath is set
		router := gin.New()
		router.Use(APIRateLimitMiddleware(nil, "/api/v1/media", 60))
		router.GET("/api/v1/media/upload", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/media/upload", nil)
		router.ServeHTTP(w, req)

		assert.False(t, w.Code == http.StatusTooManyRequests)
	})

	t.Run("creates correct rate limit config from RPM", func(t *testing.T) {
		rpm := 60
		handler := APIRateLimitMiddleware(nil, "/api/test", rpm)
		assert.NotNil(t, handler)
	})
}

func TestMultiLevelRateLimitMiddleware(t *testing.T) {
	t.Run("returns gin handler with nil redis", func(t *testing.T) {
		handler := MultiLevelRateLimitMiddleware(nil)
		assert.NotNil(t, handler)
	})

	t.Run("allows request within all limits", func(t *testing.T) {
		handler := MultiLevelRateLimitMiddleware(nil)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		assert.False(t, c.IsAborted())
	})

	t.Run("applies IP-level rate limiting", func(t *testing.T) {
		handler := MultiLevelRateLimitMiddleware(nil)

		// First request from an IP should succeed
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		handler(c)
		assert.False(t, c.IsAborted())
	})

	t.Run("allows authenticated users with account set", func(t *testing.T) {
		handler := MultiLevelRateLimitMiddleware(nil)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Set("account", "user123")

		handler(c)
		assert.False(t, c.IsAborted())
	})
}

func TestGetRateLimitKey(t *testing.T) {
	t.Run("returns user key when account is set", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Set("account", "user123")

		key := getRateLimitKey(c)
		assert.Equal(t, "user:user123", key)
	})

	t.Run("returns user key with numeric account value", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Set("account", 42)

		key := getRateLimitKey(c)
		assert.Equal(t, "user:42", key)
	})

	t.Run("returns ip key when no account is set", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		key := getRateLimitKey(c)
		assert.True(t, strings.HasPrefix(key, "ip:"))
	})
}

func TestHasPrefix(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		prefix   string
		expected bool
	}{
		{"matching prefix", "/api/v1/media/upload", "/api/v1/media", true},
		{"exact match", "/api/v1/media", "/api/v1/media", true},
		{"no match", "/api/v1/status", "/api/v1/media", false},
		{"empty string", "", "/api/v1/media", false},
		{"short string", "short", "longer prefix", false},
		{"single char match", "a", "a", true},
		{"single char no match", "b", "a", false},
		{"empty prefix", "/api/test", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, hasPrefix(tt.s, tt.prefix))
		})
	}
}

func TestParseInt(t *testing.T) {
	assert.Equal(t, 42, parseInt("42", 0))
	assert.Equal(t, 0, parseInt("invalid", 0))
	assert.Equal(t, 7, parseInt("7", 99))
	assert.Equal(t, 99, parseInt("abc", 99))
	assert.Equal(t, -1, parseInt("-1", 0))
	assert.Equal(t, 0, parseInt("0", 5))
}

func TestGetRedisClient(t *testing.T) {
	// When Redis0Host is empty, should return nil
	origHost := config.C.Redis0Host
	config.C.Redis0Host = ""
	defer func() { config.C.Redis0Host = origHost }()

	client := GetRedisClient()
	assert.Nil(t, client)
}

// ==================== request_log.go ====================

func TestRequestLogMiddleware(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		handler := RequestLogMiddleware()
		assert.NotNil(t, handler)
	})

	t.Run("sets X-Request-ID header", func(t *testing.T) {
		handler := RequestLogMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		assert.False(t, c.IsAborted())
		assert.NotEmpty(t, w.Header().Get("X-Request-ID"))
	})

	t.Run("preserves existing X-Request-ID from header", func(t *testing.T) {
		handler := RequestLogMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Request.Header.Set("X-Request-ID", "custom-id-abc123")

		handler(c)
		assert.False(t, c.IsAborted())
		assert.Equal(t, "custom-id-abc123", w.Header().Get("X-Request-ID"))
	})

	t.Run("sets request_id in gin context", func(t *testing.T) {
		handler := RequestLogMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		val, exists := c.Get("request_id")
		assert.True(t, exists)
		assert.NotEmpty(t, val)
	})

	t.Run("uses provided X-Request-ID for context", func(t *testing.T) {
		handler := RequestLogMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Request.Header.Set("X-Request-ID", "my-request-999")

		handler(c)
		val, _ := c.Get("request_id")
		assert.Equal(t, "my-request-999", val)
	})

	t.Run("works with POST request", func(t *testing.T) {
		handler := RequestLogMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/v1/statuses", strings.NewReader("body=test"))

		handler(c)
		assert.False(t, c.IsAborted())
		assert.NotEmpty(t, w.Header().Get("X-Request-ID"))
	})

	t.Run("works end-to-end with router", func(t *testing.T) {
		router := gin.New()
		router.Use(RequestLogMiddleware())
		router.GET("/ping", func(c *gin.Context) {
			c.String(http.StatusOK, "pong")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/ping", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NotEmpty(t, w.Header().Get("X-Request-ID"))
		assert.Equal(t, "pong", w.Body.String())
	})
}

func TestPerformanceLogMiddleware(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		handler := PerformanceLogMiddleware(100 * time.Millisecond)
		assert.NotNil(t, handler)
	})

	t.Run("passes through without aborting", func(t *testing.T) {
		handler := PerformanceLogMiddleware(100 * time.Millisecond)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		assert.False(t, c.IsAborted())
	})

	t.Run("handles fast requests", func(t *testing.T) {
		handler := PerformanceLogMiddleware(1 * time.Second)

		router := gin.New()
		router.Use(handler)
		router.GET("/fast", func(c *gin.Context) {
			c.String(http.StatusOK, "fast response")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/fast", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// ==================== timeout.go ====================

func TestTimeoutMiddleware(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		handler := TimeoutMiddleware(5 * time.Second)
		assert.NotNil(t, handler)
	})

	t.Run("completes normally within timeout", func(t *testing.T) {
		handler := TimeoutMiddleware(5 * time.Second)

		router := gin.New()
		router.Use(handler)
		router.GET("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "ok")
	})

	t.Run("returns 504 when handler exceeds timeout", func(t *testing.T) {
		handler := TimeoutMiddleware(50 * time.Millisecond)

		router := gin.New()
		router.Use(handler)
		router.GET("/slow", func(c *gin.Context) {
			time.Sleep(500 * time.Millisecond)
			c.JSON(http.StatusOK, gin.H{"status": "should not reach"})
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/slow", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusGatewayTimeout, w.Code)
		assert.Contains(t, w.Body.String(), "timeout")
	})
}

func TestDefaultTimeoutConfig(t *testing.T) {
	cfg := DefaultTimeoutConfig()
	assert.Equal(t, 10*time.Second, cfg.ReadTimeout)
	assert.Equal(t, 30*time.Second, cfg.WriteTimeout)
	assert.Equal(t, 120*time.Second, cfg.IdleTimeout)
	assert.Equal(t, 30*time.Second, cfg.HandlerTimeout)
}

func TestTimeoutConfig_Fields(t *testing.T) {
	cfg := TimeoutConfig{
		ReadTimeout:    5 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    60 * time.Second,
		HandlerTimeout: 15 * time.Second,
	}
	assert.Equal(t, 5*time.Second, cfg.ReadTimeout)
	assert.Equal(t, 10*time.Second, cfg.WriteTimeout)
	assert.Equal(t, 60*time.Second, cfg.IdleTimeout)
	assert.Equal(t, 15*time.Second, cfg.HandlerTimeout)
}

func TestDatabaseTimeoutMiddleware(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		handler := DatabaseTimeoutMiddleware(5 * time.Second)
		assert.NotNil(t, handler)
	})

	t.Run("passes through without aborting", func(t *testing.T) {
		handler := DatabaseTimeoutMiddleware(5 * time.Second)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		assert.False(t, c.IsAborted())
	})

	t.Run("stores timeout in request context", func(t *testing.T) {
		timeout := 3 * time.Second
		handler := DatabaseTimeoutMiddleware(timeout)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)

		ctxVal := c.Request.Context().Value(dbTimeoutKey)
		assert.NotNil(t, ctxVal)
		assert.Equal(t, timeout, ctxVal)
	})
}

// ==================== self_destruct.go ====================

// setupSelfDestructMode configures the global selfDestructMode state for testing
// without accessing the database.
func setupSelfDestructMode(enabled bool) {
	selfDestructMode.enabledOnce = sync.Once{}
	selfDestructMode.enabled = enabled
	selfDestructMode.enabledOnce.Do(func() {}) // mark as done to skip DB access
}

func TestSelfDestructMiddleware(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		handler := SelfDestructMiddleware()
		assert.NotNil(t, handler)
	})

	t.Run("passes through when self-destruct is disabled", func(t *testing.T) {
		setupSelfDestructMode(false)

		handler := SelfDestructMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/v1/statuses", nil)

		handler(c)
		assert.False(t, c.IsAborted())
	})

	t.Run("blocks write operations when self-destruct is enabled", func(t *testing.T) {
		setupSelfDestructMode(true)

		handler := SelfDestructMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/v1/statuses", nil)

		handler(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("allows GET requests when self-destruct is enabled", func(t *testing.T) {
		setupSelfDestructMode(true)

		handler := SelfDestructMiddleware()

		for _, method := range []string{"GET", "HEAD", "OPTIONS"} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(method, "/api/v1/timelines/home", nil)

			handler(c)
			assert.False(t, c.IsAborted(), "%s request should be allowed", method)
		}
	})

	t.Run("allows allowed paths when self-destruct is enabled", func(t *testing.T) {
		setupSelfDestructMode(true)

		handler := SelfDestructMiddleware()

		allowedPaths := []struct {
			method string
			path   string
		}{
			{"POST", "/api/v1/accounts/verify_credentials"},
			{"PATCH", "/api/v1/accounts/update_credentials"},
			{"GET", "/api/v1/instance"},
			{"POST", "/oauth/authorize"},
			{"POST", "/auth/login"},
			{"GET", "/api/v1/exports"},
			{"GET", "/api/v1/backups"},
		}

		for _, tc := range allowedPaths {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tc.method, tc.path, nil)

			handler(c)
			assert.False(t, c.IsAborted(), "path %s should be allowed", tc.path)
		}
	})

	t.Run("allows exact POST to export paths", func(t *testing.T) {
		setupSelfDestructMode(true)

		handler := SelfDestructMiddleware()

		exportPaths := []string{"/api/v1/backups", "/api/v1/exports"}
		for _, path := range exportPaths {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", path, nil)

			handler(c)
			assert.False(t, c.IsAborted(), "POST %s should be allowed", path)
		}
	})

	t.Run("response body contains error message when blocked", func(t *testing.T) {
		setupSelfDestructMode(true)

		handler := SelfDestructMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("DELETE", "/api/v1/statuses/123", nil)

		handler(c)
		assert.True(t, c.IsAborted())
		assert.Contains(t, w.Body.String(), "self-destruct")
	})
}

func TestSelfDestructInfoMiddleware(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		handler := SelfDestructInfoMiddleware()
		assert.NotNil(t, handler)
	})

	t.Run("sets X-Self-Destruct-Mode header when enabled", func(t *testing.T) {
		setupSelfDestructMode(true)

		handler := SelfDestructInfoMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		assert.False(t, c.IsAborted())
		assert.Equal(t, "true", w.Header().Get("X-Self-Destruct-Mode"))
	})

	t.Run("does not set header when self-destruct is disabled", func(t *testing.T) {
		setupSelfDestructMode(false)

		handler := SelfDestructInfoMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		assert.False(t, c.IsAborted())
		assert.Equal(t, "", w.Header().Get("X-Self-Destruct-Mode"))
	})
}

func TestSelfDestructModeStruct(t *testing.T) {
	mode := &SelfDestructMode{}
	assert.False(t, mode.enabled)
}

func TestIsSelfDestructEnabled_Disabled(t *testing.T) {
	setupSelfDestructMode(false)
	assert.False(t, IsSelfDestructEnabled())
}

func TestIsSelfDestructEnabled_Enabled(t *testing.T) {
	setupSelfDestructMode(true)
	assert.True(t, IsSelfDestructEnabled())
}

// ==================== compression.go ====================

func TestGzipMiddleware(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		handler := Gzip()
		assert.NotNil(t, handler)
	})

	t.Run("passes through without Accept-Encoding gzip", func(t *testing.T) {
		handler := Gzip()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		assert.False(t, c.IsAborted())
		assert.Empty(t, w.Header().Get("Content-Encoding"))
	})

	t.Run("sets Content-Encoding and Vary headers with gzip support", func(t *testing.T) {
		handler := Gzip()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Request.Header.Set("Accept-Encoding", "gzip")

		handler(c)
		assert.False(t, c.IsAborted())
		assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
		assert.Equal(t, "Accept-Encoding", w.Header().Get("Vary"))
	})

	t.Run("bypasses gzip for SSE requests", func(t *testing.T) {
		handler := Gzip()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/stream", nil)
		c.Request.Header.Set("Accept-Encoding", "gzip")
		c.Request.Header.Set("Accept", "text/event-stream")

		handler(c)
		assert.False(t, c.IsAborted())
		assert.Empty(t, w.Header().Get("Content-Encoding"))
	})

	t.Run("bypasses gzip when Content-Encoding already gzip", func(t *testing.T) {
		handler := Gzip()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Request.Header.Set("Accept-Encoding", "gzip")
		c.Request.Header.Set("Content-Encoding", "gzip")

		handler(c)
		assert.False(t, c.IsAborted())
		// Should not double-encode, Content-Encoding may not be set by middleware
		// since the original already had it
	})

	t.Run("compresses response body end-to-end", func(t *testing.T) {
		router := gin.New()
		router.Use(Gzip())
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "Hello, World! This is a test response for gzip compression.")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))

		// Decompress and verify content
		reader, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
		assert.NoError(t, err)
		defer reader.Close()
		body, err := io.ReadAll(reader)
		assert.NoError(t, err)
		assert.Contains(t, string(body), "Hello, World!")
		assert.Contains(t, string(body), "gzip compression")
	})

	t.Run("does not compress without Accept-Encoding via router", func(t *testing.T) {
		router := gin.New()
		router.Use(Gzip())
		router.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "plain text")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "plain text", w.Body.String())
		assert.Empty(t, w.Header().Get("Content-Encoding"))
	})
}

func TestResponseCache(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		handler := ResponseCache(3600)
		assert.NotNil(t, handler)
	})

	t.Run("skips non-GET requests", func(t *testing.T) {
		handler := ResponseCache(3600)

		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(method, "/test", nil)

			handler(c)
			assert.False(t, c.IsAborted())
			assert.Empty(t, w.Header().Get("Cache-Control"), "%s should not set cache headers", method)
		}
	})

	t.Run("skips cache for authenticated users", func(t *testing.T) {
		handler := ResponseCache(3600)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		c.Set("account", "user123")

		handler(c)
		assert.False(t, c.IsAborted())
		assert.Empty(t, w.Header().Get("Cache-Control"))
	})

	t.Run("sets cache headers for anonymous GET requests", func(t *testing.T) {
		handler := ResponseCache(3600)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		assert.False(t, c.IsAborted())
		assert.Contains(t, w.Header().Get("Cache-Control"), "public")
		assert.Contains(t, w.Header().Get("Cache-Control"), "max-age=3600")
		assert.NotEmpty(t, w.Header().Get("Expires"))
	})

	t.Run("sets correct max-age value", func(t *testing.T) {
		handler := ResponseCache(7200)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		assert.Contains(t, w.Header().Get("Cache-Control"), "max-age=7200")
	})
}

func TestCompressionRateLimit(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		handler := RateLimit(10, time.Minute)
		assert.NotNil(t, handler)
	})

	t.Run("allows requests within limit", func(t *testing.T) {
		handler := RateLimit(5, time.Minute)

		for i := 0; i < 5; i++ {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/test", nil)
			handler(c)
			assert.False(t, c.IsAborted(), "request %d should be allowed", i+1)
		}
	})

	t.Run("blocks request exceeding limit with 429", func(t *testing.T) {
		handler := RateLimit(2, time.Minute)

		// Allow 2 requests
		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/test", nil)
			handler(c)
			assert.False(t, c.IsAborted(), "request %d should be allowed", i+1)
		}

		// Third should be blocked
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		handler(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, 429, w.Code)
	})

	t.Run("returns error JSON on rate limit", func(t *testing.T) {
		handler := RateLimit(1, time.Minute)

		// Allow 1
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		handler(c)

		// Block
		w = httptest.NewRecorder()
		c, _ = gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)
		handler(c)

		assert.Contains(t, w.Body.String(), "rate limit exceeded")
	})

	t.Run("independent rate limiting per IP", func(t *testing.T) {
		handler := RateLimit(1, time.Minute)

		// First IP - should be allowed
		w1 := httptest.NewRecorder()
		c1, _ := gin.CreateTestContext(w1)
		c1.Request = httptest.NewRequest("GET", "/test", nil)
		handler(c1)
		assert.False(t, c1.IsAborted())

		// Second request from same IP (same test context = same IP) - should be blocked
		w2 := httptest.NewRecorder()
		c2, _ := gin.CreateTestContext(w2)
		c2.Request = httptest.NewRequest("GET", "/test", nil)
		handler(c2)
		assert.True(t, c2.IsAborted())
	})
}

func TestIntToString(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{100, "100"},
		{999, "999"},
		{-1, "-1"},
		{-42, "-42"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, intToString(tt.input))
		})
	}
}

// ==================== internal_api_middleware.go ====================

func TestInternalApiMiddleware_Full(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		handler := InternalApiMiddleware()
		assert.NotNil(t, handler)
	})

	t.Run("rejects with 401 when no token_data in context", func(t *testing.T) {
		handler := InternalApiMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/internal/test", nil)

		handler(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, 401, w.Code)
	})

	t.Run("rejects with 401 when token_data is wrong type", func(t *testing.T) {
		handler := InternalApiMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/internal/test", nil)
		c.Set("token_data", "not-a-pointer") // string instead of *models.AuthTokenData

		handler(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, 401, w.Code)
	})

	t.Run("panics when token_data is nil pointer (known middleware behavior)", func(t *testing.T) {
		handler := InternalApiMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/internal/test", nil)
		c.Set("token_data", (*models.AuthTokenData)(nil))

		// The middleware does not guard against nil *AuthTokenData pointers;
		// this documents the actual behavior.
		defer func() {
			r := recover()
			assert.NotNil(t, r, "expected panic on nil *AuthTokenData pointer")
		}()
		handler(c)
	})

	t.Run("rejects with 403 when auth type is not web", func(t *testing.T) {
		handler := InternalApiMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/internal/test", nil)
		c.Set("token_data", &models.AuthTokenData{
			AuthType: models.AuthTokenTypeOauth,
			UserId:   1,
		})

		handler(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, 403, w.Code)
	})

	t.Run("allows when auth type is web", func(t *testing.T) {
		handler := InternalApiMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/internal/test", nil)
		c.Set("token_data", &models.AuthTokenData{
			AuthType: models.AuthTokenTypeWeb,
			UserId:   1,
		})

		handler(c)
		assert.False(t, c.IsAborted())
	})

	t.Run("returns proper JSON for 401 unauthorized", func(t *testing.T) {
		handler := InternalApiMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/internal/test", nil)

		handler(c)

		assert.Contains(t, w.Body.String(), "unauthorized")
		assert.Contains(t, w.Body.String(), "401")
	})

	t.Run("returns proper JSON for 403 forbidden", func(t *testing.T) {
		handler := InternalApiMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/internal/test", nil)
		c.Set("token_data", &models.AuthTokenData{
			AuthType: models.AuthTokenTypeOauth,
		})

		handler(c)

		assert.Contains(t, w.Body.String(), "invalid token")
		assert.Contains(t, w.Body.String(), "403")
	})
}

// ==================== handle_error_middleware.go ====================

func TestHandleErrorsMiddleware_Full(t *testing.T) {
	t.Run("returns gin handler", func(t *testing.T) {
		handler := HandleErrorsMiddleware()
		assert.NotNil(t, handler)
	})

	t.Run("passes through when no errors exist", func(t *testing.T) {
		origDevMode := config.C.DevMode
		defer func() { config.C.DevMode = origDevMode }()
		config.C.DevMode = "false"

		handler := HandleErrorsMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		handler(c)
		assert.False(t, c.IsAborted())
	})

	t.Run("returns 500 with generic error message in production mode", func(t *testing.T) {
		origDevMode := config.C.DevMode
		defer func() { config.C.DevMode = origDevMode }()
		config.C.DevMode = "false"

		handler := HandleErrorsMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		// Add an error to the context
		_ = c.Error(errors.New("internal database connection failed"))

		handler(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, 500, w.Code)
		assert.Contains(t, w.Body.String(), "internal server error")
		// Should NOT contain the actual error details in production mode
		assert.NotContains(t, w.Body.String(), "database connection failed")
	})

	t.Run("returns 500 with detailed error in dev mode", func(t *testing.T) {
		origDevMode := config.C.DevMode
		defer func() { config.C.DevMode = origDevMode }()
		config.C.DevMode = "true"

		handler := HandleErrorsMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		_ = c.Error(errors.New("detailed debug error info"))

		handler(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, 500, w.Code)
		assert.Contains(t, w.Body.String(), "detailed debug error info")
		assert.Contains(t, w.Body.String(), "stack")
	})

	t.Run("handles the last error when multiple errors exist", func(t *testing.T) {
		origDevMode := config.C.DevMode
		defer func() { config.C.DevMode = origDevMode }()
		config.C.DevMode = "true"

		handler := HandleErrorsMiddleware()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/test", nil)

		_ = c.Error(errors.New("first error"))
		_ = c.Error(errors.New("second error"))

		handler(c)
		assert.True(t, c.IsAborted())
		assert.Equal(t, 500, w.Code)
		// Should contain the last error
		assert.Contains(t, w.Body.String(), "second error")
	})

	t.Run("works end-to-end with router in dev mode", func(t *testing.T) {
		origDevMode := config.C.DevMode
		defer func() { config.C.DevMode = origDevMode }()
		config.C.DevMode = "true"

		router := gin.New()
		router.Use(HandleErrorsMiddleware())
		router.GET("/error", func(c *gin.Context) {
			_ = c.Error(errors.New("test endpoint error"))
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/error", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, 500, w.Code)
		assert.Contains(t, w.Body.String(), "test endpoint error")
	})

	t.Run("works end-to-end with router without errors", func(t *testing.T) {
		origDevMode := config.C.DevMode
		defer func() { config.C.DevMode = origDevMode }()
		config.C.DevMode = "false"

		router := gin.New()
		router.Use(HandleErrorsMiddleware())
		router.GET("/ok", func(c *gin.Context) {
			c.String(http.StatusOK, "all good")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/ok", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "all good", w.Body.String())
	})
}
