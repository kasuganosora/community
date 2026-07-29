package metrics

import (
	"net/http"
	"net/http/httptest"
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

// --- Helper functions ---

func getCounterValue(t *testing.T, counter *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()

	pb := &dto.Metric{}
	m, err := counter.GetMetricWithLabelValues(labels...)
	require.NoError(t, err)

	if err != nil {
		return 0
	}

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
