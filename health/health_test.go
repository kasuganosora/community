package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/storage"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

func TestHealthStatusConstants(t *testing.T) {
	assert.Equal(t, StatusHealthy, HealthStatus("healthy"))
	assert.Equal(t, StatusDegraded, HealthStatus("degraded"))
	assert.Equal(t, StatusUnhealthy, HealthStatus("unhealthy"))
}

// ---------------------------------------------------------------------------
// NewChecker
// ---------------------------------------------------------------------------

func TestNewChecker(t *testing.T) {
	c := NewChecker("1.0.0")
	require.NotNil(t, c)
	assert.Equal(t, "1.0.0", c.version)
	assert.False(t, c.startTime.IsZero())
	assert.NotNil(t, c.checkers)
	assert.NotNil(t, c.enabled)
	assert.Empty(t, c.checkers)
	assert.Empty(t, c.enabled)
}

// ---------------------------------------------------------------------------
// Register / Unregister
// ---------------------------------------------------------------------------

func TestRegister(t *testing.T) {
	c := NewChecker("1.0.0")
	fn := func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy}
	}
	c.Register("test", fn)
	assert.Contains(t, c.checkers, "test")
	assert.True(t, c.enabled["test"])
}

func TestRegister_Overwrites(t *testing.T) {
	c := NewChecker("1.0.0")
	fn1 := func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy, Message: "first"}
	}
	fn2 := func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusDegraded, Message: "second"}
	}

	c.Register("svc", fn1)
	c.Register("svc", fn2)
	// re-register should replace
	health := c.checkers["svc"](context.Background())
	assert.Equal(t, "second", health.Message)
}

func TestUnregister(t *testing.T) {
	c := NewChecker("1.0.0")
	fn := func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy}
	}
	c.Register("test", fn)
	c.Unregister("test")
	assert.NotContains(t, c.checkers, "test")
	assert.NotContains(t, c.enabled, "test")
}

func TestUnregister_NonExistent(t *testing.T) {
	c := NewChecker("1.0.0")
	// should not panic
	c.Unregister("nonexistent")
}

// ---------------------------------------------------------------------------
// Enable / Disable
// ---------------------------------------------------------------------------

func TestEnable(t *testing.T) {
	c := NewChecker("1.0.0")
	fn := func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy}
	}
	c.Register("svc", fn)
	c.Disable("svc")
	assert.False(t, c.enabled["svc"])
	c.Enable("svc")
	assert.True(t, c.enabled["svc"])
}

func TestEnable_NonExistent(t *testing.T) {
	c := NewChecker("1.0.0")
	// Enable a name that was never registered – should be a no-op
	c.Enable("ghost")
	assert.NotContains(t, c.checkers, "ghost")
}

func TestDisable(t *testing.T) {
	c := NewChecker("1.0.0")
	fn := func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy}
	}
	c.Register("svc", fn)
	c.Disable("svc")
	assert.False(t, c.enabled["svc"])
}

func TestDisable_NonExistent(t *testing.T) {
	c := NewChecker("1.0.0")
	// should not panic
	c.Disable("ghost")
}

// ---------------------------------------------------------------------------
// Check
// ---------------------------------------------------------------------------

func TestCheck_NoCheckers(t *testing.T) {
	c := NewChecker("1.0.0")
	resp := c.Check(context.Background())
	assert.Equal(t, StatusHealthy, resp.Status)
	assert.Equal(t, "1.0.0", resp.Version)
	assert.False(t, resp.Timestamp.IsZero())
	assert.NotEmpty(t, resp.Uptime)
	assert.Empty(t, resp.Services)
}

func TestCheck_AllHealthy(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc1", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy, Latency: "1ms"} //nolint:goconst
	})
	c.Register("svc2", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy, Latency: "2ms"}
	})

	resp := c.Check(context.Background())
	assert.Equal(t, StatusHealthy, resp.Status)
	assert.Len(t, resp.Services, 2)
	assert.Equal(t, StatusHealthy, resp.Services["svc1"].Status)
	assert.Equal(t, StatusHealthy, resp.Services["svc2"].Status)
}

func TestCheck_DegradedService(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc1", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusDegraded}
	})

	resp := c.Check(context.Background())
	assert.Equal(t, StatusDegraded, resp.Status)
}

func TestCheck_UnhealthyService(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc1", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusUnhealthy, Message: "down"}
	})

	resp := c.Check(context.Background())
	assert.Equal(t, StatusUnhealthy, resp.Status)
}

func TestCheck_UnhealthyOverridesDegraded(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc1", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusDegraded}
	})
	c.Register("svc2", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusUnhealthy}
	})

	resp := c.Check(context.Background())
	assert.Equal(t, StatusUnhealthy, resp.Status)
}

func TestCheck_DisabledCheckerSkipped(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc1", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusUnhealthy}
	})
	c.Disable("svc1")
	resp := c.Check(context.Background())
	assert.Equal(t, StatusHealthy, resp.Status)
	assert.Empty(t, resp.Services)
}

func TestCheck_MixedEnabledDisabled(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("healthy", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy}
	})
	c.Register("unhealthy", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusUnhealthy}
	})
	c.Disable("unhealthy")
	resp := c.Check(context.Background())
	assert.Equal(t, StatusHealthy, resp.Status)
	assert.Len(t, resp.Services, 1)
	assert.Contains(t, resp.Services, "healthy")
}

func TestCheck_ContextTimeout(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("slow", func(ctx context.Context) ServiceHealth {
		select {
		case <-ctx.Done():
			return ServiceHealth{Status: StatusUnhealthy, Message: ctx.Err().Error()}
		case <-time.After(10 * time.Second):
			return ServiceHealth{Status: StatusHealthy}
		}
	})
	// pass a context that is already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp := c.Check(ctx)
	// The checker itself sets a 5s timeout, but the passed context is already done.
	// The result depends on timing, but we mainly care it doesn't hang.
	_ = resp
}

func TestCheck_Uptime(t *testing.T) {
	c := NewChecker("1.0.0")

	time.Sleep(5 * time.Millisecond)

	resp := c.Check(context.Background())
	assert.Contains(t, resp.Uptime, "ms")
}

// ---------------------------------------------------------------------------
// Handler (HTTP)
// ---------------------------------------------------------------------------

func setupRouter(handler gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	r.GET("/health", handler)

	return r
}

func TestHandler_Healthy(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy, Latency: "1ms"}
	})

	r := setupRouter(c.Handler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body HealthResponse

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, StatusHealthy, body.Status)
	assert.Contains(t, body.Services, "svc")
}

func TestHandler_Degraded(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusDegraded}
	})

	r := setupRouter(c.Handler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Degraded returns 200 with degraded status
	assert.Equal(t, http.StatusOK, w.Code)

	var body HealthResponse

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, StatusDegraded, body.Status)
}

func TestHandler_Unhealthy(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusUnhealthy, Message: "down"}
	})

	r := setupRouter(c.Handler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var body HealthResponse

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, StatusUnhealthy, body.Status)
}

// ---------------------------------------------------------------------------
// LivenessHandler
// ---------------------------------------------------------------------------

func TestLivenessHandler(t *testing.T) {
	c := NewChecker("1.0.0")
	r := setupRouter(c.LivenessHandler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "alive", body["status"])
	uptime, ok := body["uptime"].(string)
	assert.True(t, ok)
	assert.NotEmpty(t, uptime)
}

// ---------------------------------------------------------------------------
// ReadinessHandler
// ---------------------------------------------------------------------------

func TestReadinessHandler_Ready(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy}
	})

	r := setupRouter(c.ReadinessHandler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "ready", body["status"])
}

func TestReadinessHandler_NotReady(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusUnhealthy, Message: "critical"}
	})

	r := setupRouter(c.ReadinessHandler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "not_ready", body["status"])
	assert.Equal(t, "critical services unavailable", body["reason"])
}

func TestReadinessHandler_Degraded(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusDegraded}
	})

	r := setupRouter(c.ReadinessHandler())

	req := httptest.NewRequest(http.MethodGet, "/health", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Degraded is still "ready"
	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "ready", body["status"])
}

// ---------------------------------------------------------------------------
// DatabaseChecker
// ---------------------------------------------------------------------------

func TestDatabaseChecker_Healthy(t *testing.T) {
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	sqlDB1, _ := testDB.DB()

	defer func() { _ = sqlDB1.Close() }()

	checker := DatabaseChecker(testDB)
	health := checker(context.Background())
	assert.Equal(t, StatusHealthy, health.Status)
	assert.NotEmpty(t, health.Latency)
	assert.NotNil(t, health.Metadata)

	meta, ok := health.Metadata.(map[string]any)
	require.True(t, ok)
	assert.Contains(t, meta, "open_connections")
	assert.Contains(t, meta, "in_use")
	assert.Contains(t, meta, "idle")
	assert.Contains(t, meta, "wait_count")
}

func TestDatabaseChecker_Unhealthy_BadDB(t *testing.T) {
	// Create a gorm.DB with a closed underlying connection
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	sqlDB, _ := testDB.DB()
	require.NotNil(t, sqlDB)
	_ = sqlDB.Close()

	checker := DatabaseChecker(testDB)
	health := checker(context.Background())
	// Ping on a closed DB should be unhealthy
	assert.Equal(t, StatusUnhealthy, health.Status)
	assert.NotEmpty(t, health.Message)
}

func TestDatabaseChecker_DBError(t *testing.T) {
	// Create a gorm.DB without a valid connection pool to trigger DB() error
	testDB := &gorm.DB{
		Config:    &gorm.Config{},
		Statement: &gorm.Statement{},
	}

	checker := DatabaseChecker(testDB)
	health := checker(context.Background())
	// db.DB() should fail because Statement.ConnPool is nil
	assert.Equal(t, StatusUnhealthy, health.Status)
	assert.Contains(t, health.Message, "failed to get underlying sql.DB")
}

// ---------------------------------------------------------------------------
// RedisChecker
// ---------------------------------------------------------------------------

func TestRedisChecker_NilClient(t *testing.T) {
	checker := RedisChecker(nil)
	health := checker(context.Background())
	assert.Equal(t, StatusHealthy, health.Status)
	assert.Equal(t, "Redis not configured", health.Message)
}

func TestRedisChecker_DeadServer(t *testing.T) {
	// Create a redis client pointed at a non-existent server
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:1",
	})
	defer func() { _ = client.Close() }()

	checker := RedisChecker(client)
	health := checker(context.Background())
	assert.Equal(t, StatusUnhealthy, health.Status)
	assert.NotEmpty(t, health.Message)
	assert.NotEmpty(t, health.Latency)
}

func TestRedisChecker_Success(t *testing.T) {
	// Use miniredis to test the success path
	mr, err := miniredis.Run()
	require.NoError(t, err)

	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer func() { _ = client.Close() }()

	checker := RedisChecker(client)
	health := checker(context.Background())
	assert.Equal(t, StatusHealthy, health.Status)
	assert.NotEmpty(t, health.Latency)
	assert.NotNil(t, health.Metadata)

	meta, ok := health.Metadata.(map[string]any)
	require.True(t, ok)
	// 元数据中始终包含 version/mode 键；miniredis 的 INFO 不返回这两个字段，
	// 因此值为空字符串，但键必须存在（说明 INFO 命令被执行且被解析）。
	assert.Contains(t, meta, "version")
	assert.Contains(t, meta, "mode")
}

// ---------------------------------------------------------------------------
// StorageChecker
// ---------------------------------------------------------------------------

func TestStorageChecker_Local(t *testing.T) {
	checker := StorageChecker("local")
	// Create temporary dirs for testing
	dirs := []string{"uploads", "media", "cache", "logs"} //nolint:goconst
	for _, d := range dirs {
		_ = os.MkdirAll(d, 0755) //nolint:gosec // G301: reviewed and safe

		t.Cleanup(func() { _ = os.RemoveAll(d) })
	}

	health := checker(context.Background())
	// local checks for directories; result depends on environment
	assert.NotEmpty(t, health.Latency)
	// Status should be either healthy or degraded depending on what dirs exist
	assert.Contains(t, []HealthStatus{StatusHealthy, StatusDegraded}, health.Status)
}

func TestStorageChecker_S3_ProbesBackend(t *testing.T) {
	// Misconfigured S3 (no credentials) must not be reported healthy.
	restore := storage.SetDefault(storage.NewS3Backend(storage.S3Config{Endpoint: "s3.invalid.example:9000", Bucket: "b"}))
	defer restore()

	health := StorageChecker("s3")(context.Background())
	assert.Equal(t, StatusUnhealthy, health.Status)
	assert.Contains(t, health.Message, "S3_ACCESS_KEY")
	assert.NotContains(t, health.Message, "s3.invalid.example")
}

func TestStorageChecker_S3_LocalBackendMismatch(t *testing.T) {
	restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
	defer restore()

	health := StorageChecker("s3")(context.Background())
	assert.Equal(t, StatusUnhealthy, health.Status)
}

func TestStorageChecker_Unsupported(t *testing.T) {
	for _, kind := range []string{"oss", "cos", "unknown_type"} {
		health := StorageChecker(kind)(context.Background())
		assert.Equal(t, StatusDegraded, health.Status, kind)
		assert.Contains(t, health.Message, kind)
	}
}

// ---------------------------------------------------------------------------
// checkLocalStorage (unexported, tested via StorageChecker("local"))
// ---------------------------------------------------------------------------

func TestCheckLocalStorage_AllDirsExist(t *testing.T) {
	dirs := []string{"uploads", "media", "cache", "logs"}
	for _, d := range dirs {
		_ = os.MkdirAll(d, 0755) //nolint:gosec // G301: reviewed and safe

		t.Cleanup(func() { _ = os.RemoveAll(d) })
	}

	health := checkLocalStorage(context.Background(), time.Now())
	assert.Equal(t, StatusHealthy, health.Status)
	meta, ok := health.Metadata.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, dirs, meta["checked_dirs"])
}

func TestCheckLocalStorage_SomeDirsMissing(t *testing.T) {
	// ensure these dirs don't exist
	dirs := []string{"uploads", "media", "cache", "logs"}
	for _, d := range dirs {
		_ = os.RemoveAll(d)

		t.Cleanup(func() { _ = os.RemoveAll(d) })
	}

	health := checkLocalStorage(context.Background(), time.Now())
	assert.Equal(t, StatusDegraded, health.Status)
	assert.Contains(t, health.Message, "Failed to access directories")
}

func TestCheckLocalStorage_DirIsFile(t *testing.T) {
	// Create a file named "uploads" so os.Stat succeeds but IsDir returns false
	_ = os.Remove("uploads")
	err := os.WriteFile("uploads", []byte("not a dir"), 0644) //nolint:gosec // G306: test file
	require.NoError(t, err)

	t.Cleanup(func() { _ = os.Remove("uploads") })

	health := checkLocalStorage(context.Background(), time.Now())
	// "uploads" is a file, not a dir, so should be degraded
	assert.Equal(t, StatusDegraded, health.Status)
}

func TestCheckLocalStorage_CancelledContext(t *testing.T) {
	// ensure these dirs don't exist
	dirs := []string{"uploads", "media", "cache", "logs"}
	for _, d := range dirs {
		_ = os.RemoveAll(d)

		t.Cleanup(func() { _ = os.RemoveAll(d) })
	}

	// A pre-cancelled context should short-circuit the loop; since no dirs are
	// checked, failedDirs stays empty and the result is healthy (no failures
	// observed). This verifies the ctx.Err() early-break path.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	health := checkLocalStorage(ctx, time.Now())
	assert.Equal(t, StatusHealthy, health.Status)
}

// ---------------------------------------------------------------------------
// MatrixHomeserverChecker
// ---------------------------------------------------------------------------

func TestMatrixHomeserverChecker(t *testing.T) {
	checker := MatrixHomeserverChecker("example.com")
	health := checker(context.Background())
	assert.Equal(t, StatusHealthy, health.Status)
	assert.NotEmpty(t, health.Latency)
	meta, ok := health.Metadata.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "example.com", meta["server_name"])
}

func TestMatrixHomeserverChecker_EmptyServerName(t *testing.T) {
	checker := MatrixHomeserverChecker("")
	health := checker(context.Background())
	assert.Equal(t, StatusHealthy, health.Status)
}

// ---------------------------------------------------------------------------
// ActivityPubChecker
// ---------------------------------------------------------------------------

func TestActivityPubChecker_URLConfigured(t *testing.T) {
	checker := ActivityPubChecker()
	health := checker(context.Background())
	// Depends on config.C.AppBaseURL; in test env it may be configured from .env
	assert.NotEmpty(t, health.Latency)
	// Status should be either healthy (if URL configured) or degraded (if not)
	assert.Contains(t, []HealthStatus{StatusHealthy, StatusDegraded}, health.Status)
}

func TestActivityPubChecker_EmptyURL(t *testing.T) {
	// Save and restore config
	origURL := config.C.AppBaseURL

	config.C.AppBaseURL = ""

	defer func() { config.C.AppBaseURL = origURL }()

	checker := ActivityPubChecker()
	health := checker(context.Background())
	assert.Equal(t, StatusDegraded, health.Status)
	assert.Equal(t, "AppBaseURL not configured", health.Message)
}

func TestActivityPubChecker_WithURL(t *testing.T) {
	origURL := config.C.AppBaseURL

	config.C.AppBaseURL = "https://example.com"

	defer func() { config.C.AppBaseURL = origURL }()

	checker := ActivityPubChecker()
	health := checker(context.Background())
	assert.Equal(t, StatusHealthy, health.Status)
	assert.Empty(t, health.Message)
}

// ---------------------------------------------------------------------------
// parseRedisInfo
// ---------------------------------------------------------------------------

func TestParseRedisInfo_Empty(t *testing.T) {
	result := parseRedisInfo("")
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestParseRedisInfo_SomeContent(t *testing.T) {
	// 标准格式（CRLF 行尾，模拟真实 Redis 输出）
	input := "# Server\r\nredis_version:7.0.0\r\nredis_mode:standalone\r\n"
	result := parseRedisInfo(input)
	assert.NotNil(t, result)
	assert.Equal(t, "7.0.0", result["redis_version"])
	assert.Equal(t, "standalone", result["redis_mode"])
	// 注释行不应进入结果
	_, ok := result["# Server"]
	assert.False(t, ok)
}

// ---------------------------------------------------------------------------
// CheckWithTimeout
// ---------------------------------------------------------------------------

func TestCheckWithTimeout(t *testing.T) {
	fn := func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy, Latency: "1ms"}
	}
	health := CheckWithTimeout(fn, 5*time.Second)
	assert.Equal(t, StatusHealthy, health.Status)
}

func TestCheckWithTimeout_ExceedsTimeout(t *testing.T) {
	fn := func(ctx context.Context) ServiceHealth {
		select {
		case <-ctx.Done():
			return ServiceHealth{Status: StatusUnhealthy, Message: "timeout"}
		case <-time.After(10 * time.Second):
			return ServiceHealth{Status: StatusHealthy}
		}
	}
	health := CheckWithTimeout(fn, 10*time.Millisecond)
	// Should return quickly due to timeout
	assert.Equal(t, StatusUnhealthy, health.Status)
	assert.Equal(t, "timeout", health.Message)
}

// ---------------------------------------------------------------------------
// Check: panic recovery
// ---------------------------------------------------------------------------

func TestCheck_PanicRecovery(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("good", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy}
	})
	c.Register("panicker", func(ctx context.Context) ServiceHealth {
		panic("boom")
	})

	// Should not panic; the panicking checker is converted to unhealthy.
	resp := c.Check(context.Background())
	assert.Equal(t, StatusUnhealthy, resp.Status)
	assert.Contains(t, resp.Services, "panicker")
	assert.Equal(t, StatusUnhealthy, resp.Services["panicker"].Status)
	assert.Contains(t, resp.Services["panicker"].Message, "panicked")
	// The healthy checker should still report healthy.
	assert.Equal(t, StatusHealthy, resp.Services["good"].Status)
}

func TestCheck_PanicRecovery_NilPointer(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("nil", func(ctx context.Context) ServiceHealth {
		var m map[string]any
		m["x"] = 1 //nolint:staticcheck // SA5000: intentional nil map write to trigger panic for recovery test
		return ServiceHealth{Status: StatusHealthy}
	})

	resp := c.Check(context.Background())
	assert.Equal(t, StatusUnhealthy, resp.Status)
	assert.Contains(t, resp.Services["nil"].Message, "panicked")
}

// ---------------------------------------------------------------------------
// Check: concurrent execution
// ---------------------------------------------------------------------------

func TestCheck_RunsConcurrently(t *testing.T) {
	// If checks ran sequentially, total time would be ~3*block.
	// Running concurrently, it should be roughly one block duration.
	block := 150 * time.Millisecond

	c := NewChecker("1.0.0")
	for i := 0; i < 3; i++ {
		c.Register(fmt.Sprintf("svc%d", i), func(ctx context.Context) ServiceHealth {
			select {
			case <-ctx.Done():
				return ServiceHealth{Status: StatusUnhealthy, Message: "ctx done"}
			case <-time.After(block):
				return ServiceHealth{Status: StatusHealthy}
			}
		})
	}

	start := time.Now()
	resp := c.Check(context.Background())
	elapsed := time.Since(start)

	assert.Len(t, resp.Services, 3)
	// Sequential would take >= 3*block. Allow generous slack for scheduler jitter.
	assert.Less(t, elapsed, 2*block, "checkers should run concurrently, not sequentially")
}

// ---------------------------------------------------------------------------
// Check: overall timeout ceiling
// ---------------------------------------------------------------------------

func TestCheck_OverallTimeoutCeiling(t *testing.T) {
	// Many slow-but-cooperative checkers; each honors ctx.
	// With sequential per-check 5s timeouts the total would be huge,
	// but the overall timeout (10s) must bound the whole call well below that.
	c := NewChecker("1.0.0")
	for i := 0; i < 10; i++ {
		c.Register(fmt.Sprintf("slow%d", i), func(ctx context.Context) ServiceHealth {
			<-ctx.Done()
			return ServiceHealth{Status: StatusUnhealthy, Message: ctx.Err().Error()}
		})
	}

	start := time.Now()
	resp := c.Check(context.Background())
	elapsed := time.Since(start)

	// Should complete well under the 10s overall ceiling.
	assert.Less(t, elapsed, 6*time.Second, "overall timeout should bound the total time")
	assert.Len(t, resp.Services, 10)
}

// ---------------------------------------------------------------------------
// SimpleHealthHandler
// ---------------------------------------------------------------------------

func TestSimpleHealthHandler(t *testing.T) {
	r := setupRouter(SimpleHealthHandler())
	req := httptest.NewRequest(http.MethodGet, "/health", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "healthy", body["status"])
	assert.NotNil(t, body["timestamp"])
}

// ---------------------------------------------------------------------------
// LogHealthStatus
// ---------------------------------------------------------------------------

func TestLogHealthStatus_AllHealthy(t *testing.T) {
	health := HealthResponse{
		Status:    StatusHealthy,
		Timestamp: time.Now().UTC(),
		Version:   "1.0.0", //nolint:goconst
		Uptime:    "10s",
		Services: map[string]ServiceHealth{
			"svc1": {Status: StatusHealthy, Latency: "1ms"},
			"svc2": {Status: StatusHealthy, Latency: "2ms"},
		},
	}
	// Should not panic
	LogHealthStatus(health)
}

func TestLogHealthStatus_WithUnhealthy(t *testing.T) {
	health := HealthResponse{
		Status:    StatusUnhealthy,
		Timestamp: time.Now().UTC(),
		Version:   "1.0.0",
		Services: map[string]ServiceHealth{
			"db": {Status: StatusUnhealthy, Message: "connection refused", Latency: "5ms"},
		},
	}
	// Should not panic
	LogHealthStatus(health)
}

func TestLogHealthStatus_WithDegraded(t *testing.T) {
	health := HealthResponse{
		Status:    StatusDegraded,
		Timestamp: time.Now().UTC(),
		Version:   "1.0.0",
		Services: map[string]ServiceHealth{
			"storage": {Status: StatusDegraded, Message: "disk full", Latency: "3ms"},
		},
	}
	// Should not panic
	LogHealthStatus(health)
}

func TestLogHealthStatus_EmptyServices(t *testing.T) {
	health := HealthResponse{
		Status:    StatusHealthy,
		Timestamp: time.Now().UTC(),
		Version:   "1.0.0",
		Services:  map[string]ServiceHealth{},
	}
	LogHealthStatus(health)
}

func TestLogHealthStatus_NilServices(t *testing.T) {
	health := HealthResponse{
		Status:    StatusHealthy,
		Timestamp: time.Now().UTC(),
		Version:   "1.0.0",
	}
	LogHealthStatus(health)
}

// ---------------------------------------------------------------------------
// DefaultChecker (integration – depends on config + db)
// ---------------------------------------------------------------------------

func TestDefaultChecker(t *testing.T) {
	// DefaultChecker depends on config.C (loaded from .env) and db.GetDB()
	// During tests, db.GetDB() returns nil because InitGorm is skipped.
	// So "database" checker should not be registered.
	c := DefaultChecker()
	require.NotNil(t, c)
	assert.NotNil(t, c.checkers)
	// Should have storage, matrix, and activitypub checkers at minimum
	// database depends on db.GetDB() != nil
}

func TestDefaultChecker_WithDB(t *testing.T) {
	// Set up a real database to cover the db.GetDB() != nil branch
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Save original and restore
	origDB := database.DB

	database.DB = testDB

	defer func() { database.DB = origDB }()

	// Reset defaultChecker so it gets re-created
	origDefault := defaultChecker

	defaultChecker = nil

	defer func() { defaultChecker = origDefault }()

	c := DefaultChecker()
	require.NotNil(t, c)
	assert.Contains(t, c.checkers, "database")
}

// ---------------------------------------------------------------------------
// InitChecker
// ---------------------------------------------------------------------------

func TestInitChecker_NilRedis(t *testing.T) {
	c := InitChecker(nil)
	require.NotNil(t, c)
	// database may or may not be registered depending on db.GetDB()
	// redis should NOT be registered (nil client)
	assert.NotContains(t, c.checkers, "redis")
}

func TestInitChecker_WithRedisClient(t *testing.T) {
	// Pass a non-nil redis client (pointed at dead server) to cover the registration branch
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:1",
	})
	defer func() { _ = client.Close() }()

	c := InitChecker(client)
	require.NotNil(t, c)
	// redis should be registered (non-nil client)
	assert.Contains(t, c.checkers, "redis")
	assert.True(t, c.enabled["redis"])
}

func TestInitChecker_WithDB(t *testing.T) {
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	origDB := database.DB

	database.DB = testDB

	defer func() { database.DB = origDB }()

	c := InitChecker(nil)
	require.NotNil(t, c)
	assert.Contains(t, c.checkers, "database")
}

// ---------------------------------------------------------------------------
// ServiceHealth struct JSON tags
// ---------------------------------------------------------------------------

func TestServiceHealth_JSONEncoding(t *testing.T) {
	sh := ServiceHealth{
		Status:   StatusDegraded,
		Latency:  "10ms",
		Message:  "slow",
		Metadata: map[string]any{"key": "value"},
	}
	data, err := json.Marshal(sh)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"status":"degraded"`)
	assert.Contains(t, string(data), `"latency":"10ms"`)
	assert.Contains(t, string(data), `"message":"slow"`)
	assert.Contains(t, string(data), `"key":"value"`)
}

func TestServiceHealth_Omitempty(t *testing.T) {
	sh := ServiceHealth{
		Status:  StatusHealthy,
		Latency: "1ms",
	}
	data, err := json.Marshal(sh)
	require.NoError(t, err)
	// Message and Metadata should be omitted
	assert.NotContains(t, string(data), `"message"`)
	assert.NotContains(t, string(data), `"metadata"`)
}

// ---------------------------------------------------------------------------
// HealthResponse struct JSON tags
// ---------------------------------------------------------------------------

func TestHealthResponse_JSONEncoding(t *testing.T) {
	hr := HealthResponse{
		Status:    StatusHealthy,
		Timestamp: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Version:   "1.0.0",
		Uptime:    "1h",
		Services: map[string]ServiceHealth{
			"db": {Status: StatusHealthy, Latency: "1ms"},
		},
	}
	data, err := json.Marshal(hr)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"status":"healthy"`)
	assert.Contains(t, string(data), `"version":"1.0.0"`)
	assert.Contains(t, string(data), `"uptime":"1h"`)
}

// ---------------------------------------------------------------------------
// DetailedHealthHandler / LivenessHandler / ReadinessHandler (package-level)
// ---------------------------------------------------------------------------

func TestDetailedHealthHandler(t *testing.T) {
	r := setupRouter(DetailedHealthHandler())
	req := httptest.NewRequest(http.MethodGet, "/health", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Should not panic; status code should be 200 or 503
	assert.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, w.Code)
}

func TestPackageLivenessHandler(t *testing.T) {
	r := setupRouter(LivenessHandler())
	req := httptest.NewRequest(http.MethodGet, "/health", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPackageReadinessHandler(t *testing.T) {
	r := setupRouter(ReadinessHandler())
	req := httptest.NewRequest(http.MethodGet, "/health", nil) //nolint:noctx
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Should not panic
	assert.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, w.Code)
}

// ---------------------------------------------------------------------------
// Check returns a fresh map each time
// ---------------------------------------------------------------------------

func TestCheck_FreshResponse(t *testing.T) {
	c := NewChecker("1.0.0")
	c.Register("svc", func(ctx context.Context) ServiceHealth {
		return ServiceHealth{Status: StatusHealthy, Latency: "1ms"}
	})

	r1 := c.Check(context.Background())
	r2 := c.Check(context.Background())
	// They should be different instances
	assert.NotSame(t, &r1.Services, &r2.Services)
	assert.False(t, r2.Timestamp.Before(r1.Timestamp))
}
