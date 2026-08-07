# metrics (指标监控)

基于 [Prometheus](https://prometheus.io/) 的应用指标监控模块，提供 HTTP、数据库、缓存、联邦和业务运行时的可观测性指标，并通过 `/metrics` 端点暴露给 Prometheus 抓取。

所有指标在包初始化时（`init()`）自动注册到 Prometheus 默认注册表，并统一使用 `community` 作为 namespace。所有指标类型（Counter、Gauge、Histogram）均由 Prometheus client 提供，天然线程安全，可在任意 goroutine 中并发调用。

## 核心功能

- **Counter（计数器）**：单调递增的累计指标，用于统计请求总数、查询次数、缓存命中/未命中等。提供 `Inc()` 递增与 `Add(n)` 递增指定值。
- **Gauge（仪表盘）**：可增可减的瞬时值指标，用于反映当前活跃连接数、活跃用户数等可上下波动的状态。提供 `Inc()`、`Dec()`、`Set(n)`、`Add(n)` 等操作。
- **Histogram（直方图）**：按 bucket 统计数值分布的指标，用于观测请求耗时、请求/响应大小等。提供 `Observe(v)` 记录采样值，并自动维护 `_bucket`、`_sum`、`_count` 时间序列。
- **线程安全**：所有指标底层为 `prometheus.Counter`、`prometheus.Gauge`、`prometheus.Histogram`，内部均由 `sync` 原语保护，支持高并发读写。
- **Label 基数（cardinality）防护**：中间件对 HTTP 方法、路由路径等用户可控的 label 取值进行归一化，非标准方法映射为 `unknown`、未匹配路由映射为 `unmatched`、空字符串映射为 `unknown`，避免 label cardinality 爆炸。
- **Gin 中间件与 Handler**：开箱即用的 `MetricsMiddleware()` 自动采集 HTTP 指标，`Handler()` 暴露标准的 Prometheus 抓取端点。

## 用法示例

### 接入 HTTP 中间件与抓取端点

```go
import "github.com/kasuganosora/community/metrics"

r := gin.Default()

// 注册 Prometheus 抓取端点（通常为 /metrics）
r.GET("/metrics", metrics.Handler())

// 全局 HTTP 指标中间件，自动采集请求数、耗时、大小等
r.Use(metrics.MetricsMiddleware())

// 业务路由
r.GET("/api/v1/timeline", timelineHandler)
```

### 记录数据库查询指标

```go
start := time.Now()
// ... 执行数据库查询 ...
metrics.RecordDbQuery("select", "statuses", time.Since(start))
```

### 记录缓存命中 / 未命中

```go
val, err := cache.Get(key)
if err == nil {
    metrics.RecordCacheHit("timeline")
    return val
}
metrics.RecordCacheMiss("timeline")
// ... 回源并回填缓存 ...
```

### 记录联邦事件

```go
// direction: "in" / "out"，eventType: "create" / "update" / "delete" 等，status: "success" / "failed"
metrics.RecordFederatedEvent("out", "create", "success")
```

### 直接操作业务 Gauge 指标

```go
// 活跃用户数（可增可减）
metrics.ActiveUsers.Inc()
// ...
metrics.ActiveUsers.Dec()

// 直接设置瞬时值
metrics.ActiveConnections.Set(128)
```

### 直接操作带 Label 的 Counter / Histogram

所有指标均为导出变量，可在业务代码中直接使用 Prometheus API：

```go
// Counter：带 label 的累计计数
metrics.FederatedEvents.WithLabelValues("in", "update", "success").Inc()

// Histogram：观测一次耗时采样
metrics.DbQueryDuration.WithLabelValues("insert", "notifications").Observe(0.034)
```

## 核心类型与函数

### 指标类型说明

本包所有指标均由 Prometheus client_golang 提供，统一下列三类：

| 类型 | 适用场景 | 主要操作 | 本包实例 |
|------|----------|----------|----------|
| Counter | 单调递增的累计值 | `Inc()`、`Add(n float64)` | `HttpRequestsTotal`、`DbQueryTotal`、`FederatedEvents`、`CacheHits`、`CacheMisses` |
| Gauge | 可增可减的瞬时值 | `Set(n)`、`Inc()`、`Dec()`、`Add(n)` | `ActiveConnections`、`ActiveUsers` |
| Histogram | 数值分布统计 | `Observe(v float64)` | `HttpRequestDuration`、`HttpRequestSize`、`HttpResponseSize`、`DbQueryDuration` |

> 带 label 的指标使用对应的 `...Vec` 变体（如 `prometheus.CounterVec`、`prometheus.HistogramVec`），通过 `WithLabelValues(...)` 获取具体实例后再操作。所有操作都是线程安全的。

### 内置指标

所有指标统一使用 `namespace = "community"`。

#### HTTP 指标

| 变量 | 类型 | 指标名 | Labels | 说明 |
|------|------|--------|--------|------|
| `HttpRequestsTotal` | Counter | `community_http_requests_total` | `method`, `path`, `status` | HTTP 请求总数 |
| `HttpRequestDuration` | Histogram | `community_http_request_duration_seconds` | `method`, `path` | HTTP 请求耗时（秒），默认 bucket |
| `HttpRequestSize` | Histogram | `community_http_request_size_bytes` | `method`, `path` | HTTP 请求体大小（字节），指数 bucket（100B ~ 1GB） |
| `HttpResponseSize` | Histogram | `community_http_response_size_bytes` | `method`, `path` | HTTP 响应体大小（字节），指数 bucket（100B ~ 1GB） |

#### 数据库指标

| 变量 | 类型 | 指标名 | Labels | 说明 |
|------|------|--------|--------|------|
| `DbQueryTotal` | Counter | `community_db_queries_total` | `operation`, `table` | 数据库查询总数 |
| `DbQueryDuration` | Histogram | `community_db_query_duration_seconds` | `operation`, `table` | 数据库查询耗时（秒） |

#### 业务指标

| 变量 | 类型 | 指标名 | Labels | 说明 |
|------|------|--------|--------|------|
| `ActiveConnections` | Gauge | `community_active_connections` | - | 当前活跃连接数 |
| `ActiveUsers` | Gauge | `community_active_users` | - | 当前活跃用户数 |
| `FederatedEvents` | Counter | `community_federated_events_total` | `direction`, `type`, `status` | 联邦事件总数 |

#### 缓存指标

| 变量 | 类型 | 指标名 | Labels | 说明 |
|------|------|--------|--------|------|
| `CacheHits` | Counter | `community_cache_hits_total` | `cache_name` | 缓存命中总数 |
| `CacheMisses` | Counter | `community_cache_misses_total` | `cache_name` | 缓存未命中总数 |

### 包级函数

| 函数 | 说明 |
|------|------|
| `MetricsMiddleware() gin.HandlerFunc` | Gin 中间件，自动采集 HTTP 请求总数、耗时、请求/响应大小，并维护 `ActiveConnections`；自动对 `method`/`path` 等 label 归一化以防止 cardinality 爆炸 |
| `Handler() gin.HandlerFunc` | 返回封装 `promhttp.Handler()` 的 Gin handler，用于暴露 `/metrics` 抓取端点 |
| `RecordDbQuery(operation, table string, duration time.Duration)` | 记录一次数据库查询（自增计数 + 观测耗时），空 label 取值归一化为 `unknown` |
| `RecordCacheHit(cacheName string)` | 记录一次缓存命中 |
| `RecordCacheMiss(cacheName string)` | 记录一次缓存未命中 |
| `RecordFederatedEvent(direction, eventType, status string)` | 记录一次联邦事件，空 label 取值归一化为 `unknown` |

### Label 归一化策略

为防止 label cardinality 爆炸（攻击者可构造无限多的方法或 URL 耗尽 Prometheus 内存），本包对用户可控的 label 取值进行归一化：

| 归一化函数 | 作用 | 异常取值的归一化结果 |
|------------|------|----------------------|
| `normalizeMethod` | 限定 HTTP method 为 RFC 9110 标准方法集合 | 非标准方法 → `unknown` |
| `normalizePath` | 限定 path 为已注册路由 | 未匹配路由（`FullPath` 为空）→ `unmatched` |
| `normalizeLabel` | 归一化通用 label 取值 | 空字符串 → `unknown` |

## 依赖关系

| 依赖 | 说明 |
|------|------|
| [github.com/prometheus/client_golang](https://github.com/prometheus/client_golang) | Prometheus 官方 Go client，提供 Counter / Gauge / Histogram 类型与 `promhttp` 抓取 handler |
| [github.com/gin-gonic/gin](https://github.com/gin-gonic/gin) | Web 框架，用于实现 HTTP 中间件与 `/metrics` handler |

> 本包通常配合社区项目的路由（`router.go`）接入，并在 `metrics` 模块内部完成所有内置指标的注册（`init()` 中调用 `prometheus.MustRegister`），业务代码无需手动注册。
