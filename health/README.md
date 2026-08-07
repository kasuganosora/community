# health (健康检查)

提供应用健康检查与存活/就绪探针能力，支持对数据库、Redis、存储、Matrix、ActivityPub 等依赖项进行并发检测，并对外暴露符合 Kubernetes 探针规范的 HTTP 端点。

## 核心功能

- **依赖检查**：内置 Database、Redis、Storage、Matrix、ActivityPub 等检查器，可检测各依赖项的连通性与状态。
- **存活探针（Liveness）**：`LivenessHandler` 返回进程是否存活，仅判断进程运行时间，不检查依赖。
- **就绪探针（Readiness）**：`ReadinessHandler` 基于完整检查结果判断服务是否可对外提供流量。
- **详细健康检查**：`Handler` 返回每个检查项的状态、延迟与元数据，状态为 `unhealthy` 时返回 HTTP 503。
- **并发执行与超时控制**：所有检查项并发执行，单检查项超时 5 秒，整体调用超时 10 秒，单项 panic 不影响整体。
- **goroutine 安全**：`Checker` 使用读写锁，支持并发注册/注销/启停检查项。
- **自定义检查器**：可通过 `HealthCheckerFunc` 注册任意业务检查逻辑。

## 健康状态

| 状态 | 含义 | 对应 HTTP |
|------|------|-----------|
| `healthy` | 健康 | 200 |
| `degraded` | 降级（部分检查项异常但非致命） | 200 |
| `unhealthy` | 不健康（存在关键检查项失败） | 503 |

整体状态按优先级聚合：任一检查项为 `unhealthy` 则整体为 `unhealthy`；否则任一为 `degraded` 则整体为 `degraded`；其余为 `healthy`。

## 后端使用

```go
import "github.com/kasuganosora/community/health"
```

### 获取默认检查器并注册到路由

```go
import "github.com/gin-gonic/gin"

// 获取默认检查器（惰性初始化，自动注册内置检查项）
checker := health.DefaultChecker()

router := gin.Default()

// 详细健康检查端点
router.GET("/health", checker.Handler())

// Kubernetes 存活探针
router.GET("/health/live", checker.LivenessHandler())

// Kubernetes 就绪探针
router.GET("/health/ready", checker.ReadinessHandler())
```

### 带 Redis 的检查器初始化

```go
// 显式初始化检查器并注册 Redis 检查
checker := health.InitChecker(redisClient)

router.GET("/health", checker.Handler())
```

### 包级便捷函数

```go
// 简单健康检查（仅返回 status: healthy）
router.GET("/ping", health.SimpleHealthHandler())

// 详细健康检查（使用默认检查器）
router.GET("/health", health.DetailedHealthHandler())

// 存活探针
router.GET("/health/live", health.LivenessHandler())

// 就绪探针
router.GET("/health/ready", health.ReadinessHandler())
```

### 自定义检查项

```go
checker := health.NewChecker("1.0.0")

// 注册自定义检查项
checker.Register("payment_service", func(ctx context.Context) health.ServiceHealth {
    start := time.Now()
    // 执行依赖检查逻辑...
    return health.ServiceHealth{
        Status:  health.StatusHealthy,
        Latency: time.Since(start).String(),
        Message: "payment service reachable",
    }
})

// 临时禁用某检查项
checker.Disable("payment_service")

// 重新启用
checker.Enable("payment_service")

// 注销检查项
checker.Unregister("payment_service")
```

### 带超时执行单个检查

```go
result := health.CheckWithTimeout(
    health.DatabaseChecker(db.GetDB()),
    3*time.Second,
)
fmt.Println(result.Status, result.Latency)
```

### 记录健康状态日志

```go
response := checker.Check(context.Background())
health.LogHealthStatus(response)
// healthy 状态记为 Info，异常项记为 Warn
```

## 内置检查器

| 检查器函数 | 检查项 | 判定逻辑 |
|------------|--------|----------|
| `DatabaseChecker(db)` | Database | `PingContext` 数据库连通性；连接池 `WaitCount > 100` 判为 `degraded` |
| `RedisChecker(client)` | Redis | `PING` 连通性 + `INFO` 元数据；客户端为 `nil` 视为 `healthy`（未配置） |
| `StorageChecker(type)` | Storage | `local` 检查 `uploads/media/cache/logs` 目录可访问；缺失记为 `degraded`；云存储（`s3/oss/cos`）需额外配置 |
| `MatrixHomeserverChecker(name)` | Matrix | 返回 server_name 元数据，可扩展为实际 API 调用 |
| `ActivityPubChecker()` | ActivityPub | 检查 `AppBaseURL` 是否配置，未配置记为 `degraded` |

`DefaultChecker()` 与 `InitChecker(redisClient)` 默认注册的检查项：

| 名称 | 检查器 | 触发条件 |
|------|--------|----------|
| `database` | `DatabaseChecker` | `db.GetDB() != nil` |
| `redis` | `RedisChecker` | `InitChecker` 且 `redisClient != nil` |
| `storage` | `StorageChecker` | 始终注册 |
| `matrix` | `MatrixHomeserverChecker` | 始终注册 |
| `activitypub` | `ActivityPubChecker` | 始终注册 |

## API 端点

### 响应结构（详细健康检查）

```json
{
  "status": "healthy",
  "timestamp": "2025-01-01T00:00:00Z",
  "version": "1.0.0",
  "uptime": "2h30m10s",
  "services": {
    "database": {
      "status": "healthy",
      "latency": "2.3ms",
      "metadata": {
        "open_connections": 5,
        "in_use": 2,
        "idle": 3,
        "wait_count": 0,
        "wait_duration_ms": 0,
        "max_idle_closed": 0,
        "max_lifetime_closed": 0
      }
    },
    "redis": {
      "status": "healthy",
      "latency": "0.8ms",
      "metadata": {
        "version": "7.0.0",
        "mode": "standalone"
      }
    },
    "storage": {
      "status": "healthy",
      "latency": "0.1ms",
      "metadata": {
        "checked_dirs": ["uploads", "media", "cache", "logs"]
      }
    }
  }
}
```

### 存活探针响应

```json
{
  "status": "alive",
  "uptime": "2h30m10s"
}
```

### 就绪探针响应

就绪（HTTP 200）：

```json
{
  "status": "ready",
  "services": { ... }
}
```

未就绪（HTTP 503）：

```json
{
  "status": "not_ready",
  "reason": "critical services unavailable",
  "services": { ... }
}
```

## API 参考

### 核心类型

```go
// HealthStatus 健康状态
type HealthStatus string

const (
    StatusHealthy   HealthStatus = "healthy"
    StatusDegraded  HealthStatus = "degraded"
    StatusUnhealthy HealthStatus = "unhealthy"
)

// HealthCheckerFunc 健康检查函数类型
type HealthCheckerFunc func(ctx context.Context) ServiceHealth

// ServiceHealth 单项服务健康状态
type ServiceHealth struct {
    Status   HealthStatus
    Latency  string
    Message  string
    Metadata any
}

// HealthResponse 健康检查整体响应
type HealthResponse struct {
    Status    HealthStatus
    Timestamp time.Time
    Version   string
    Uptime    string
    Services  map[string]ServiceHealth
}
```

### Checker 方法

| 方法 | 说明 |
|------|------|
| `NewChecker(version string) *Checker` | 创建检查器实例 |
| `Register(name string, checker HealthCheckerFunc)` | 注册检查项 |
| `Unregister(name string)` | 注销检查项 |
| `Enable(name string)` / `Disable(name string)` | 启用 / 禁用检查项 |
| `Check(ctx context.Context) HealthResponse` | 执行全部已启用检查 |
| `Handler() gin.HandlerFunc` | 详细健康检查 HTTP 处理器 |
| `LivenessHandler() gin.HandlerFunc` | 存活探针处理器 |
| `ReadinessHandler() gin.HandlerFunc` | 就绪探针处理器 |

### 包级便捷函数

| 函数 | 说明 |
|------|------|
| `DefaultChecker() *Checker` | 获取默认检查器（惰性初始化，自动注册内置检查项） |
| `InitChecker(redisClient *redis.Client) *Checker` | 初始化检查器并注册 Redis 检查 |
| `SimpleHealthHandler() gin.HandlerFunc` | 简单健康检查处理器（仅返回 `status: healthy`） |
| `DetailedHealthHandler() gin.HandlerFunc` | 详细健康检查处理器（基于默认检查器） |
| `LivenessHandler() gin.HandlerFunc` | 存活探针处理器（基于默认检查器） |
| `ReadinessHandler() gin.HandlerFunc` | 就绪探针处理器（基于默认检查器） |
| `CheckWithTimeout(checker HealthCheckerFunc, timeout time.Duration) ServiceHealth` | 带超时执行单个检查 |
| `LogHealthStatus(health HealthResponse)` | 记录健康状态日志 |

## 超时与并发策略

| 参数 | 值 | 说明 |
|------|----|------|
| `defaultCheckTimeout` | 5s | 单个检查项超时 |
| `defaultOverallTimeout` | 10s | 单次 `Check` 调用整体超时 |
| 并发模型 | per-checker goroutine | 每个检查项独立 goroutine 并发执行 |
| panic 隔离 | `recover()` | 单项 panic 转为该项 `StatusUnhealthy`，不影响其他检查 |

## 依赖关系

### 外部依赖

| 依赖 | 用途 |
|------|------|
| `github.com/gin-gonic/gin` | HTTP 处理器与路由 |
| `github.com/redis/go-redis/v9` | Redis 客户端（`RedisChecker`） |
| `gorm.io/gorm` | 数据库 ORM（`DatabaseChecker`） |

### 内部依赖

| 包 | 用途 |
|----|------|
| `config` | 版本号、`StorageType`、`AppBaseURL` 等配置 |
| `db` | 数据库连接（`db.GetDB()`） |
| `log` | 健康状态日志输出（`LogHealthStatus`） |
