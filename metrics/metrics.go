// Package metrics 提供 Prometheus 监控指标
package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "community"

var (
	// HTTP 请求指标.
	HttpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	HttpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request duration in seconds",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	HttpRequestSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_request_size_bytes",
			Help:      "HTTP request size in bytes",
			Buckets:   prometheus.ExponentialBuckets(100, 10, 8), // 100B to 1GB
		},
		[]string{"method", "path"},
	)

	HttpResponseSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_response_size_bytes",
			Help:      "HTTP response size in bytes",
			Buckets:   prometheus.ExponentialBuckets(100, 10, 8),
		},
		[]string{"method", "path"},
	)

	// 数据库指标.
	DbQueryDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "db_query_duration_seconds",
			Help:      "Database query duration in seconds",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"operation", "table"},
	)

	DbQueryTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "db_queries_total",
			Help:      "Total number of database queries",
		},
		[]string{"operation", "table"},
	)

	// 业务指标.
	ActiveConnections = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "active_connections",
			Help:      "Number of active connections",
		},
	)

	ActiveUsers = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "active_users",
			Help:      "Number of active users",
		},
	)

	FederatedEvents = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "federated_events_total",
			Help:      "Total number of federated events",
		},
		[]string{"direction", "type", "status"},
	)

	// 缓存指标.
	CacheHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "cache_hits_total",
			Help:      "Total number of cache hits",
		},
		[]string{"cache_name"},
	)

	CacheMisses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "cache_misses_total",
			Help:      "Total number of cache misses",
		},
		[]string{"cache_name"},
	)
)

func init() {
	// 注册所有指标
	prometheus.MustRegister(HttpRequestsTotal)
	prometheus.MustRegister(HttpRequestDuration)
	prometheus.MustRegister(HttpRequestSize)
	prometheus.MustRegister(HttpResponseSize)
	prometheus.MustRegister(DbQueryDuration)
	prometheus.MustRegister(DbQueryTotal)
	prometheus.MustRegister(ActiveConnections)
	prometheus.MustRegister(ActiveUsers)
	prometheus.MustRegister(FederatedEvents)
	prometheus.MustRegister(CacheHits)
	prometheus.MustRegister(CacheMisses)
}

// MetricsMiddleware Prometheus 指标收集中间件.
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// 增加活跃连接数
		ActiveConnections.Inc()
		defer ActiveConnections.Dec()

		// 获取请求大小
		requestSize := c.Request.ContentLength

		c.Next()

		// 计算耗时
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())

		// 记录指标
		labels := []string{c.Request.Method, c.FullPath(), status}
		HttpRequestsTotal.WithLabelValues(labels...).Inc()
		HttpRequestDuration.WithLabelValues(c.Request.Method, c.FullPath()).Observe(duration)

		if requestSize > 0 {
			HttpRequestSize.WithLabelValues(c.Request.Method, c.FullPath()).Observe(float64(requestSize))
		}

		responseSize := c.Writer.Size()
		if responseSize > 0 {
			HttpResponseSize.WithLabelValues(c.Request.Method, c.FullPath()).Observe(float64(responseSize))
		}
	}
}

// Handler 返回 Prometheus metrics HTTP handler.
func Handler() gin.HandlerFunc {
	return gin.WrapH(promhttp.Handler())
}

// RecordDbQuery 记录数据库查询指标.
func RecordDbQuery(operation, table string, duration time.Duration) {
	DbQueryTotal.WithLabelValues(operation, table).Inc()
	DbQueryDuration.WithLabelValues(operation, table).Observe(duration.Seconds())
}

// RecordCacheHit 记录缓存命中.
func RecordCacheHit(cacheName string) {
	CacheHits.WithLabelValues(cacheName).Inc()
}

// RecordCacheMiss 记录缓存未命中.
func RecordCacheMiss(cacheName string) {
	CacheMisses.WithLabelValues(cacheName).Inc()
}

// RecordFederatedEvent 记录联邦事件.
func RecordFederatedEvent(direction, eventType, status string) {
	FederatedEvents.WithLabelValues(direction, eventType, status).Inc()
}
