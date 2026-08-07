package metrics

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// --- Metric Variable Tests ---

func TestAllMetricsRegistered(t *testing.T) {
	// Verify all expected metrics are registered by attempting to unregister them.
	// DefaultGatherer.Gather() only returns metrics with observations, so
	// CounterVec/HistogramVec with no data would be missed.
	expectedMetrics := []struct {
		name string
		c    prometheus.Collector
	}{
		{"community_http_requests_total", HttpRequestsTotal},
		{"community_http_request_duration_seconds", HttpRequestDuration},
		{"community_http_request_size_bytes", HttpRequestSize},
		{"community_http_response_size_bytes", HttpResponseSize},
		{"community_db_query_duration_seconds", DbQueryDuration},
		{"community_db_queries_total", DbQueryTotal},
		{"community_active_connections", ActiveConnections},
		{"community_active_users", ActiveUsers},
		{"community_federated_events_total", FederatedEvents},
		{"community_cache_hits_total", CacheHits},
		{"community_cache_misses_total", CacheMisses},
	}

	for _, m := range expectedMetrics {
		ok := prometheus.DefaultRegisterer.Unregister(m.c)
		assert.True(t, ok, "Expected metric %s to be registered", m.name)
		// Re-register immediately so other tests are not affected
		prometheus.MustRegister(m.c)
	}
}

// --- RecordDbQuery Tests ---

func TestRecordDbQuery(t *testing.T) {
	RecordDbQuery("select", "accounts", 10*time.Millisecond)

	val := getCounterValue(t, DbQueryTotal, "select", "accounts")
	assert.GreaterOrEqual(t, val, float64(1))
}

func TestRecordDbQuery_DifferentOperations(t *testing.T) {
	operations := []string{"select", "insert", "update", "delete"}
	tables := []string{"accounts", "statuses", "notifications"}

	for _, op := range operations {
		for _, table := range tables {
			RecordDbQuery(op, table, time.Duration(0))
		}
	}
}

// --- RecordCacheHit Tests ---

func TestRecordCacheHit(t *testing.T) {
	RecordCacheHit("accounts")

	val := getCounterValue(t, CacheHits, "accounts")
	assert.GreaterOrEqual(t, val, float64(1))
}

func TestRecordCacheHit_DifferentCaches(t *testing.T) {
	caches := []string{"accounts", "statuses", "rules", "notifications"}

	for _, cache := range caches {
		RecordCacheHit(cache)
	}
}

// --- RecordCacheMiss Tests ---

func TestRecordCacheMiss(t *testing.T) {
	RecordCacheMiss("statuses")

	val := getCounterValue(t, CacheMisses, "statuses")
	assert.GreaterOrEqual(t, val, float64(1))
}

func TestRecordCacheMiss_DifferentCaches(t *testing.T) {
	caches := []string{"accounts", "statuses", "rules", "tags"}

	for _, cache := range caches {
		RecordCacheMiss(cache)
	}
}

// --- RecordFederatedEvent Tests ---

func TestRecordFederatedEvent(t *testing.T) {
	RecordFederatedEvent("incoming", "follow", "success")

	val := getCounterValue(t, FederatedEvents, "incoming", "follow", "success")
	assert.GreaterOrEqual(t, val, float64(1))
}

func TestRecordFederatedEvent_DifferentDirections(t *testing.T) {
	RecordFederatedEvent("outgoing", "create", "success")
	RecordFederatedEvent("incoming", "delete", "error")
	RecordFederatedEvent("outgoing", "update", "success")
}

// --- MetricsMiddleware Tests -----

func TestMetricsMiddleware_IncrementsRequests(t *testing.T) {
	before := getCounterValue(t, HttpRequestsTotal, "GET", "/api/test", "200")

	router := gin.New()
	router.Use(MetricsMiddleware())
	router.GET("/api/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	after := getCounterValue(t, HttpRequestsTotal, "GET", "/api/test", "200")
	assert.Greater(t, after, before)
}

func TestMetricsMiddleware_TracksActiveConnections(t *testing.T) {
	beforeVal := getGaugeValue(t, ActiveConnections)

	router := gin.New()
	router.Use(MetricsMiddleware())
	router.GET("/api/test", func(c *gin.Context) {
		// During request processing, active connections should be > before
		duringVal := getGaugeValue(t, ActiveConnections)
		assert.InDelta(t, beforeVal+1, duringVal, 0)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	// After request, should be back to baseline
	afterVal := getGaugeValue(t, ActiveConnections)
	if beforeVal == 0 {
		// InEpsilon fails when expected is 0, use exact match instead
		assert.InDelta(t, beforeVal, afterVal, 0)
	} else {
		assert.InEpsilon(t, beforeVal, afterVal, 0.001)
	}
}

func TestMetricsMiddleware_RecordsDuration(t *testing.T) {
	router := gin.New()
	router.Use(MetricsMiddleware())
	router.GET("/api/slow", func(c *gin.Context) {
		time.Sleep(10 * time.Millisecond)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/slow", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMetricsMiddleware_DifferentStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"200", http.StatusOK},
		{"404", http.StatusNotFound},
		{"500", http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Use(MetricsMiddleware())
			router.GET("/api/status-test", func(c *gin.Context) {
				c.Status(tt.statusCode)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/status-test", nil) //nolint:noctx
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.statusCode, w.Code)
		})
	}
}

func TestMetricsMiddleware_DifferentMethods(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{"GET", http.MethodGet},
		{"POST", http.MethodPost},
		{"PUT", http.MethodPut},
		{"DELETE", http.MethodDelete},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Use(MetricsMiddleware())
			router.Any("/api/method-test", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, "/api/method-test", nil) //nolint:noctx
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

// --- Handler Tests ---

func TestHandler_ReturnsHandler(t *testing.T) {
	handler := Handler()
	assert.NotNil(t, handler)
}

func TestHandler_ServesMetrics(t *testing.T) {
	router := gin.New()
	router.GET("/metrics", Handler())

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "community_http_requests_total")
}

// --- ActiveUsers Gauge Tests ---

func TestActiveUsers_Set(t *testing.T) {
	before := getGaugeValue(t, ActiveUsers)
	ActiveUsers.Set(100)
	after := getGaugeValue(t, ActiveUsers)
	assert.InDelta(t, float64(100), after, 0)

	// Reset
	ActiveUsers.Set(before)
}

func TestActiveUsers_IncDec(t *testing.T) {
	before := getGaugeValue(t, ActiveUsers)
	ActiveUsers.Inc()
	assert.InDelta(t, before+1, getGaugeValue(t, ActiveUsers), 0)
	ActiveUsers.Dec()
	assert.InDelta(t, before, getGaugeValue(t, ActiveUsers), 0)
}

// --- Label Cardinality Normalization Tests ---
//
// 这些测试验证 cardinality 爆炸防护：用户/攻击者可控的 label 取值
// 必须被归一化为有限集合，避免 Prometheus 时间序列无限增长。

func TestNormalizeMethod_StandardMethods(t *testing.T) {
	// 所有 RFC 9110 标准方法必须原样保留
	standard := []string{
		http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodHead, http.MethodOptions,
		http.MethodConnect, http.MethodTrace,
	}
	for _, m := range standard {
		assert.Equal(t, m, normalizeMethod(m), "standard method %q should be preserved", m)
	}
}

func TestNormalizeMethod_NonStandardMethods(t *testing.T) {
	// 攻击者可构造的任意自定义方法必须全部归一化为 unknownLabel，
	// 这是防止 cardinality 爆炸攻击的核心防护点。
	nonStandard := []string{"FOO", "BAR", "XYZZY", "HACK", "", "get", "Get", "POST "}
	for _, m := range nonStandard {
		assert.Equal(t, unknownLabel, normalizeMethod(m),
			"non-standard method %q should be normalized to %q", m, unknownLabel)
	}
}

func TestNormalizePath_MatchedRoute(t *testing.T) {
	// 已匹配路由的 FullPath 必须原样保留
	assert.Equal(t, "/api/v1/users", normalizePath("/api/v1/users"))
	assert.Equal(t, "/users/:id", normalizePath("/users/:id"))
}

func TestNormalizePath_UnmatchedRoute(t *testing.T) {
	// gin 对 404 返回空字符串的 FullPath，必须归一化为 unmatchedLabel，
	// 否则每个不存在的 URL 都会创建一个独立时间序列。
	assert.Equal(t, unmatchedLabel, normalizePath(""))
}

func TestNormalizeLabel_EmptyAndNonEmpty(t *testing.T) {
	assert.Equal(t, unknownLabel, normalizeLabel(""))
	assert.Equal(t, "accounts", normalizeLabel("accounts"))
}

// TestNormalizeMethod_CardinalityBound 验证无论输入多少种不同的非法方法，
// normalizeMethod 的输出空间始终有界（仅标准方法 + unknown）。
// 这是对 cardinality 爆炸攻击的端到端验证。
func TestNormalizeMethod_CardinalityBound(t *testing.T) {
	// 模拟攻击者发送大量不同自定义方法
	attackerMethods := make([]string, 0, 10000)
	for i := 0; i < 10000; i++ {
		attackerMethods = append(attackerMethods, "FAKE"+strconv.Itoa(i))
	}

	outputs := make(map[string]struct{})
	for _, m := range attackerMethods {
		outputs[normalizeMethod(m)] = struct{}{}
	}

	// 全部 10000 种非法方法必须只映射到一个 label 取值
	assert.Len(t, outputs, 1, "10000 distinct fake methods must collapse to a single label value")
	assert.Contains(t, outputs, unknownLabel)
}

// --- Middleware Cardinality Tests ---

// TestMetricsMiddleware_NonStandardMethodCollapsed 验证中间件端到端地
// 将非标准 HTTP 方法归一化为 unknownLabel，而不是为每个伪造方法创建独立时间序列。
func TestMetricsMiddleware_NonStandardMethodCollapsed(t *testing.T) {
	router := gin.New()
	router.Use(MetricsMiddleware())
	// 用 NoRoute 让 gin 对任意路径都走 404 而不 panic
	router.NoRoute(func(c *gin.Context) { c.Status(http.StatusNotFound) })

	// 发送多个不同的伪造方法请求
	fakeMethods := []string{"FOO", "BAR", "BAZ", "QUX", "XYZZY"}

	// 使用 before/after 增量断言，因为计数器是全局累积的（在 -count=N 下会复用）
	before := getCounterValue(t, HttpRequestsTotal, unknownLabel, unmatchedLabel, "404")
	for _, m := range fakeMethods {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(m, "/api/test", nil) //nolint:noctx
		router.ServeHTTP(w, req)
	}
	after := getCounterValue(t, HttpRequestsTotal, unknownLabel, unmatchedLabel, "404")

	// 所有伪造方法必须被归一化到 unknownLabel 这一个时间序列
	assert.Equal(t, float64(len(fakeMethods)), after-before,
		"all non-standard methods must be counted under the single %q label", unknownLabel)

	// 不应出现任何伪造方法对应的独立时间序列。
	// 由于这些伪造方法从未被作为 label 使用，对应的 WithLabelValues 会即时
	// 创建一个值为 0 的新序列，因此断言它们为 0。
	for _, m := range fakeMethods {
		v := getCounterValue(t, HttpRequestsTotal, m, unmatchedLabel, "404")
		assert.Equal(t, float64(0), v, "fake method %q must not have its own time series", m)
	}
}

// TestMetricsMiddleware_UnmatchedRouteNormalized 验证 404 路径被归一化为
// unmatchedLabel，而不是为每个不存在的 URL 创建独立时间序列。
func TestMetricsMiddleware_UnmatchedRouteNormalized(t *testing.T) {
	router := gin.New()
	router.Use(MetricsMiddleware())
	router.GET("/api/exists", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.NoRoute(func(c *gin.Context) { c.Status(http.StatusNotFound) })

	// 发送多个不同的不存在路径
	paths := []string{
		"/nonexistent1", "/nonexistent2", "/totally/fake/path/1",
		"/totally/fake/path/2", "/does-not-exist-at-all",
	}
	before := getCounterValue(t, HttpRequestsTotal, "GET", unmatchedLabel, "404")
	for _, p := range paths {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil) //nolint:noctx
		router.ServeHTTP(w, req)
	}
	after := getCounterValue(t, HttpRequestsTotal, "GET", unmatchedLabel, "404")

	// 所有不存在的路径必须累加到同一个 unmatchedLabel 时间序列
	assert.Equal(t, float64(len(paths)), after-before,
		"all unmatched paths must be counted under the single %q label", unmatchedLabel)
}

// TestMetricsMiddleware_StandardMethodPreserved 验证标准方法仍被正确记录，
// 确保归一化没有破坏正常功能。
func TestMetricsMiddleware_StandardMethodPreserved(t *testing.T) {
	router := gin.New()
	router.Use(MetricsMiddleware())
	router.GET("/api/card-test", func(c *gin.Context) { c.Status(http.StatusOK) })

	before := getCounterValue(t, HttpRequestsTotal, "GET", "/api/card-test", "200")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/card-test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	after := getCounterValue(t, HttpRequestsTotal, "GET", "/api/card-test", "200")
	assert.Equal(t, float64(1), after-before, "standard GET method should be recorded under its real label")
}

// --- Record Function Cardinality Tests ---

func TestRecordDbQuery_EmptyLabelsNormalized(t *testing.T) {
	before := getCounterValue(t, DbQueryTotal, unknownLabel, unknownLabel)
	RecordDbQuery("", "", 5*time.Millisecond)
	after := getCounterValue(t, DbQueryTotal, unknownLabel, unknownLabel)
	assert.Greater(t, after, before, "empty operation/table should be normalized to %q", unknownLabel)
}

func TestRecordCacheHit_EmptyLabelNormalized(t *testing.T) {
	before := getCounterValue(t, CacheHits, unknownLabel)
	RecordCacheHit("")
	after := getCounterValue(t, CacheHits, unknownLabel)
	assert.Greater(t, after, before, "empty cache name should be normalized to %q", unknownLabel)
}

func TestRecordCacheMiss_EmptyLabelNormalized(t *testing.T) {
	before := getCounterValue(t, CacheMisses, unknownLabel)
	RecordCacheMiss("")
	after := getCounterValue(t, CacheMisses, unknownLabel)
	assert.Greater(t, after, before, "empty cache name should be normalized to %q", unknownLabel)
}

func TestRecordFederatedEvent_EmptyLabelsNormalized(t *testing.T) {
	before := getCounterValue(t, FederatedEvents, unknownLabel, unknownLabel, unknownLabel)
	RecordFederatedEvent("", "", "")
	after := getCounterValue(t, FederatedEvents, unknownLabel, unknownLabel, unknownLabel)
	assert.Greater(t, after, before, "empty labels should be normalized to %q", unknownLabel)
}

// --- Helper functions ---

func getCounterValue(t *testing.T, counter *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()

	pb := &dto.Metric{}
	m, err := counter.GetMetricWithLabelValues(labels...)
	require.NoError(t, err)

	err = m.Write(pb)
	require.NoError(t, err)

	return pb.GetCounter().GetValue()
}

func getGaugeValue(t *testing.T, gauge prometheus.Gauge) float64 {
	t.Helper()

	pb := &dto.Metric{}
	err := gauge.Write(pb)
	require.NoError(t, err)

	return pb.GetGauge().GetValue()
}
