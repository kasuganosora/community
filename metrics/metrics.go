// Package metrics 提供 Prometheus 监控指标
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	namespace = "community"

	// unknownLabel 是用于用户可控但无法识别的 label 取值的归一化占位符。
	// 任何不属于已知有限集合的取值都会被映射为它，从而限制 label cardinality。
	unknownLabel = "unknown"
	// unmatchedLabel 是未匹配到任何已注册路由的请求使用的 path 占位符。
	// gin 的 FullPath() 对 404 请求返回空字符串，直接用作 label 会让大量
	// 不存在的 URL 产生不同的时间序列（cardinality 爆炸），这里统一归一化。
	unmatchedLabel = "unmatched"
)

// validHTTPMethods 记录 RFC 9110 定义的标准 HTTP 方法集合。
// 用作 label 的 HTTP 方法必须限定在这个有限集合内，否则攻击者可以构造
// 任意自定义方法（如 "FOO"/"BAR"/"XYZZY"）来制造无限多个时间序列，
// 最终耗尽 Prometheus 的内存。gin 不会拦截非法方法，因此必须在这里兜底。
var validHTTPMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodHead:    {},
	http.MethodOptions: {},
	http.MethodConnect: {},
	http.MethodTrace:   {},
}

// normalizeMethod 将 HTTP method label 限定在标准方法集合内，
// 非标准方法统一归一化为 unknownLabel，避免 cardinality 爆炸。
func normalizeMethod(method string) string {
	if _, ok := validHTTPMethods[method]; ok {
		return method
	}
	return unknownLabel
}

// normalizePath 将路由 path label 限定在已注册路由集合内。
// 对未匹配到路由（FullPath 为空）的请求统一归一化为 unmatchedLabel，
// 防止每个不存在的 URL 都产生独立的时间序列。
func normalizePath(fullPath string) string {
	if fullPath == "" {
		return unmatchedLabel
	}
	return fullPath
}

// normalizeLabel 将任意用户/调用方传入的 label 取值归一化。
// 空字符串会被映射为 unknownLabel，避免出现难以查询的空 label 取值。
// 注意：这里只能防御空值这类显式异常取值；调用方仍需保证传入的 label
// 取值来自有限、可控的枚举集合（例如缓存名、表名等），否则仍会造成
// cardinality 爆炸。
func normalizeLabel(value string) string {
	if value == "" {
		return unknownLabel
	}
	return value
}

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

		// 归一化 label 取值，避免 cardinality 爆炸：
		// - method: 非标准 HTTP 方法归一化为 "unknown"（防止伪造方法攻击）
		// - path: 未匹配路由（FullPath 为空）归一化为 "unmatched"（防止不存在的 URL 产生无限序列）
		method := normalizeMethod(c.Request.Method)
		path := normalizePath(c.FullPath())

		// 记录指标
		HttpRequestsTotal.WithLabelValues(method, path, status).Inc()
		HttpRequestDuration.WithLabelValues(method, path).Observe(duration)

		if requestSize > 0 {
			HttpRequestSize.WithLabelValues(method, path).Observe(float64(requestSize))
		}

		responseSize := c.Writer.Size()
		if responseSize > 0 {
			HttpResponseSize.WithLabelValues(method, path).Observe(float64(responseSize))
		}
	}
}

// Handler 返回 Prometheus metrics HTTP handler.
func Handler() gin.HandlerFunc {
	return gin.WrapH(promhttp.Handler())
}

// RecordDbQuery 记录数据库查询指标.
// operation 和 table 作为 label 取值，空字符串会被归一化为 "unknown"
// 以避免 cardinality 意外增长（空 label 取值在 Prometheus 中虽然合法但难以查询）。
func RecordDbQuery(operation, table string, duration time.Duration) {
	DbQueryTotal.WithLabelValues(normalizeLabel(operation), normalizeLabel(table)).Inc()
	DbQueryDuration.WithLabelValues(normalizeLabel(operation), normalizeLabel(table)).Observe(duration.Seconds())
}

// RecordCacheHit 记录缓存命中.
func RecordCacheHit(cacheName string) {
	CacheHits.WithLabelValues(normalizeLabel(cacheName)).Inc()
}

// RecordCacheMiss 记录缓存未命中.
func RecordCacheMiss(cacheName string) {
	CacheMisses.WithLabelValues(normalizeLabel(cacheName)).Inc()
}

// RecordFederatedEvent 记录联邦事件.
func RecordFederatedEvent(direction, eventType, status string) {
	FederatedEvents.WithLabelValues(
		normalizeLabel(direction),
		normalizeLabel(eventType),
		normalizeLabel(status),
	).Inc()
}
