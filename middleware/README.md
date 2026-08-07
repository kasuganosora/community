# middleware (HTTP 中间件)

基于 [Gin](https://github.com/gin-gonic/gin) 的 HTTP 中间件集合，为社区平台提供认证鉴权、OAuth scope 校验、限流、压缩、安全头部、CORS、超时控制、请求日志、自毁模式等横切关注点（cross-cutting concerns）。

所有中间件均为 `gin.HandlerFunc` 工厂函数（返回闭包），在 [`router.go`](../router.go) 中通过 `RouterGroup.Use()` 或路由级参数挂载到对应的路由组与端点上。

## 核心功能

| 类别 | 中间件 | 说明 |
|------|--------|------|
| 认证 | `AuthMiddleware()` | 解析 `Authorization: Bearer` / `app-token` Cookie，校验 token 有效性并注入 `token_data` / `user` / `account`；无 token 时放行 |
| 认证 | `CheckAuthMiddleware()` | 强制要求已认证（检查 `token_data` 是否存在），未认证返回 401 |
| 鉴权 | `CheckScopeMiddleware(scopes ...Scope)` | OAuth scope 校验，要求传入 scope 全部满足；Web token 仅校验 admin scope |
| 访问控制 | `InternalApiMiddleware()` | 限制仅 Web 登录 token（`AuthTokenTypeWeb`）可访问内部 API |
| 限流 | `RateLimitMiddleware(limiter RateLimiter)` | 基于自定义 `RateLimiter` 接口的限流 |
| 限流 | `MultiLevelRateLimitMiddleware(redisClient)` | 全局 + IP + 用户三级限流，Redis 不可用时降级为内存 |
| 限流 | `APIRateLimitMiddleware(redisClient, pathPrefix, rpm)` | 针对特定 API 路径前缀的限流 |
| 限流 | `RateLimit(limit, window)` | 基于内存的简易固定窗口限流（按 IP） |
| 压缩 | `Gzip()` | gzip 响应压缩，自动跳过 SSE 流与已压缩响应 |
| 缓存 | `ResponseCache(duration)` | 为 GET 请求设置 `Cache-Control` / `Expires` 响应头 |
| 安全 | `SecurityHeadersMiddleware()` | 注入 XSS / CSP / HSTS / COOP 等安全响应头 |
| 安全 | `CORSMiddleware(allowOrigins)` | CORS 跨域处理（基于 `gin-contrib/cors`） |
| 安全 | `RequestSizeLimitMiddleware(maxSize)` | 限制请求体大小，支持 chunked transfer |
| 安全 | `IPWhitelistMiddleware(whitelist)` | IP 白名单（支持 CIDR 与单个 IP） |
| 超时 | `TimeoutMiddleware(timeout)` | 请求处理超时控制，超时返回 504 并取消下游 |
| 超时 | `DatabaseTimeoutMiddleware(timeout)` | 将 DB 查询超时注入 context |
| 日志 | `RequestLogMiddleware()` | 结构化请求日志，生成 / 透传 `X-Request-ID`，按状态码分级 |
| 日志 | `PerformanceLogMiddleware(threshold)` | 仅记录超过阈值的慢请求 |
| 错误处理 | `HandleErrorsMiddleware()` | 捕获 `c.Errors` 末尾错误，统一返回 500（开发模式附带堆栈） |
| 运维 | `SelfDestructMiddleware()` | 自毁模式下仅放行只读与数据导出操作 |
| 运维 | `SelfDestructInfoMiddleware()` | 自毁模式下注入 `X-Self-Destruct-Mode` 响应头 |

## 架构 / 结构

中间件按职责拆分为多个文件，统一属于 `package middleware`：

```
middleware/
├── auth_middleware.go        # 认证（token 解析、Redis 缓存校验）
├── check_auth_middleware.go  # 强制登录检查
├── check_scope_middleware.go # OAuth scope 校验
├── internal_api_middleware.go# 内部 API 访问限制
├── ratelimit.go              # 限流（内存/Redis/滑动窗口）+ Redis 客户端单例
├── compression.go            # gzip 压缩、响应缓存、简易限流
├── security.go               # 安全头、CORS、请求体限制、IP 白名单
├── timeout.go                # 请求超时、数据库超时
├── request_log.go            # 请求日志、性能日志
├── handle_error_middleware.go# 统一错误处理
└── self_destruct.go          # 自毁模式
```

### 认证流程

`AuthMiddleware` 与 `CheckAuthMiddleware` 配合使用，职责分离：

- **`AuthMiddleware()`**：尽力解析 token。成功时把 `token_data` / `user` / `account` / `application` 写入 `gin.Context`；**无 token 或解析失败均放行**，由后续 `CheckAuthMiddleware` 决定是否强制登录。token 校验优先查 Redis 缓存（`token_cache:<token>`），命中可避免重复查库，未命中再查 DB 并回写缓存（有效 token TTL 30s，已撤销/过期标记为 `revoked` 且 TTL 10 分钟）。
- **`CheckAuthMiddleware()`**：仅检查 `token_data` 是否存在，不存在则返回 401。

```
请求 → AuthMiddleware (解析/注入) → CheckAuthMiddleware (强制登录) → CheckScopeMiddleware (scope) → Handler
```

### 限流架构

限流通过 `RateLimiter` 接口抽象，提供三种实现，按部署形态选择：

```go
type RateLimiter interface {
    Allow(key string) bool
    AllowN(key string, n int) bool
}
```

| 实现 | 算法 | 适用场景 |
|------|------|----------|
| `MemoryRateLimiter` | 令牌桶（`golang.org/x/time/rate`） | 单机部署；后台定期清理空闲 limiter |
| `RedisRateLimiter` | 令牌桶（Lua 脚本原子操作） | 多实例分布式部署；Redis 异常时降级放行 |
| `SlidingWindowRateLimiter` | 滑动窗口（Redis Sorted Set） | 需要更精确限流的场景 |

`MultiLevelRateLimitMiddleware` 在单次请求中依次执行 **全局 → IP → 用户** 三级检查，任一级别超限即返回 429。当传入的 `redisClient` 为 `nil` 时自动降级为 `MemoryRateLimiter`。

> 通过 `GetRedisClient()` 获取复用 `db` 包连接的 Redis 单例（`sync.Once` 初始化），未配置 Redis 时返回 `nil` 而非 panic。

### 超时机制

`TimeoutMiddleware` 采用 goroutine + select 模式实现真正的请求级超时：

- 超时后立即返回 `504 Gateway Timeout`；
- 通过 `context.WithTimeout` 的取消信号通知下游 handler 终止；
- 使用 `timeoutResponseWriter` 包装 writer，超时后丢弃后续写入，避免并发写冲突；panic 会被捕获并重新抛出以触发 Gin 的 recovery。

## 用法示例

### 注册全局与分组中间件

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/kasuganosora/community/middleware"
)

r := gin.New()

// 全局中间件：请求日志、错误处理、安全头、超时、gzip、自毁模式
r.Use(
    middleware.RequestLogMiddleware(),
    middleware.HandleErrorsMiddleware(),
    middleware.SecurityHeadersMiddleware(),
    middleware.TimeoutMiddleware(30*time.Second),
    middleware.Gzip(),
    middleware.SelfDestructInfoMiddleware(),
    middleware.SelfDestructMiddleware(),
)

// CORS（生产环境需显式配置允许来源）
r.Use(middleware.CORSMiddleware([]string{"https://app.example.com"}))

// 多级限流（Redis 不可用时自动降级为内存限流）
r.Use(middleware.MultiLevelRateLimitMiddleware(middleware.GetRedisClient()))
```

### 认证 + scope 校验（路由组级）

对应 `router.go` 中的真实用法——解析 token 后强制登录，并对发布嘟文校验 `write:statuses` scope：

```go
// 解析 token（无 token 放行） + 强制要求已登录
authGroup := v1.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware())

// 发布嘟文：校验 write:statuses scope
authGroup.Group("", middleware.CheckScopeMiddleware(
    middleware.Scope{Type: models.AppScopeTypeWrite, Value: models.AppScopeWriteStatuses},
)).POST("/statuses", status.PostStatus)

// 管理员接口：校验 admin:read scope（Web token 会额外校验 IsAdmin）
authGroup.Group("", middleware.CheckScopeMiddleware(
    middleware.Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminRead},
)).GET("/admin/reports", admin.ListReports)
```

### 内部 API 限制

```go
// 仅允许 Web 登录 token 访问（如站内信、通知等内部端点）
internalGroup := authGroup.Group("", middleware.InternalApiMiddleware())
internalGroup.GET("/conversations", conversation.List)
```

### 针对 API 路径前缀限流

对应 `router.go` 中对 `inbox` / `media` 等高负载端点的限流：

```go
// 收件箱：100 rpm
app.Group("", middleware.APIRateLimitMiddleware(
    middleware.GetRedisClient(), "/inbox", 100,
)).POST("/inbox", inbox.Receive)

// 媒体上传：30 rpm，并叠加请求体大小限制
mediaGroup := v1.Group("", middleware.AuthMiddleware(), middleware.CheckAuthMiddleware(),
    middleware.APIRateLimitMiddleware(middleware.GetRedisClient(), "/media", 30),
    middleware.RequestSizeLimitMiddleware(20<<20), // 20 MiB
)
mediaGroup.POST("/media", media.Upload)
```

### 安全 / 访问控制

```go
// IP 白名单（管理后台，支持 CIDR）
adminGroup := r.Group("/admin")
adminGroup.Use(middleware.IPWhitelistMiddleware([]string{"10.0.0.0/8", "192.168.1.100"}))
```

## 核心类型与函数

### 类型

#### `Scope`

OAuth scope 描述，用于 `CheckScopeMiddleware`：

| 字段 | 类型 | 说明 |
|------|------|------|
| `Type` | `uint8` | scope 类型，对应 `models.AppScopeType*`（Read / Write / Admin） |
| `Value` | `uint64` | scope 具体值，对应 `models.AppScope*`（如 `AppScopeWriteStatuses`） |

#### `RateLimiterConfig` / `APILimit`

限流配置：

| 字段 | 类型 | 说明 |
|------|------|------|
| `GlobalRPS` | `float64` | 全局每秒请求数 |
| `GlobalBurst` | `int` | 全局突发请求数 |
| `PerUserRPM` | `int` | 每用户每分钟请求数 |
| `PerIPRPM` | `int` | 每 IP 每分钟请求数 |
| `APILimits` | `map[string]APILimit` | API 特定限流配置（key 为路径前缀） |

`APILimit` 包含 `RPS float64` 与 `Burst int`。`DefaultRateLimiterConfig()` 返回默认配置（全局 1000 RPS / 1500 burst，用户 300 RPM，IP 600 RPM）。

#### `TimeoutConfig`

| 字段 | 类型 | 说明 |
|------|------|------|
| `ReadTimeout` | `time.Duration` | 读取请求超时 |
| `WriteTimeout` | `time.Duration` | 写入响应超时 |
| `IdleTimeout` | `time.Duration` | 连接空闲超时 |
| `HandlerTimeout` | `time.Duration` | 处理程序超时 |

`DefaultTimeoutConfig()` 返回默认配置（Read 10s / Write 30s / Idle 120s / Handler 30s）。

#### 限流器实现

| 类型 | 说明 |
|------|------|
| `MemoryRateLimiter` | 内存令牌桶限流器，`NewMemoryRateLimiter(config)` 创建 |
| `RedisRateLimiter` | Redis 分布式令牌桶限流器，`NewRedisRateLimiter(client, config)` 创建 |
| `SlidingWindowRateLimiter` | Redis 滑动窗口限流器，`NewSlidingWindowRateLimiter(client, windowSize, maxRequests)` 创建 |

### 函数

#### 认证与鉴权

| 函数 | 说明 |
|------|------|
| `AuthMiddleware()` | token 解析与注入，无 token 放行 |
| `CheckAuthMiddleware()` | 强制要求已登录 |
| `CheckScopeMiddleware(scopes ...Scope)` | OAuth scope 校验 |
| `InternalApiMiddleware()` | 限制仅 Web token 访问 |

#### 限流

| 函数 | 说明 |
|------|------|
| `RateLimitMiddleware(limiter RateLimiter)` | 基于限流器接口的通用限流 |
| `MultiLevelRateLimitMiddleware(redisClient)` | 全局 / IP / 用户三级限流 |
| `APIRateLimitMiddleware(redisClient, pathPrefix, rpm)` | API 路径前缀限流 |
| `RateLimit(limit, window)` | 简易固定窗口限流（按 IP） |
| `GetRedisClient()` | 获取 Redis 客户端单例（复用 `db` 连接，未配置返回 `nil`） |

#### 压缩与缓存

| 函数 | 说明 |
|------|------|
| `Gzip()` | gzip 响应压缩 |
| `ResponseCache(duration)` | 设置响应缓存头（秒） |

#### 安全

| 函数 | 说明 |
|------|------|
| `SecurityHeadersMiddleware()` | 安全响应头 |
| `CORSMiddleware(allowOrigins)` | CORS 跨域 |
| `RequestSizeLimitMiddleware(maxSize)` | 请求体大小限制 |
| `IPWhitelistMiddleware(whitelist)` | IP 白名单 |

#### 超时

| 函数 | 说明 |
|------|------|
| `TimeoutMiddleware(timeout)` | 请求处理超时（返回 504） |
| `DatabaseTimeoutMiddleware(timeout)` | 注入 DB 查询超时到 context |

#### 日志与错误

| 函数 | 说明 |
|------|------|
| `RequestLogMiddleware()` | 请求日志（含 `X-Request-ID`） |
| `PerformanceLogMiddleware(threshold)` | 慢请求日志 |
| `HandleErrorsMiddleware()` | 统一错误处理（500） |

#### 自毁模式

| 函数 | 说明 |
|------|------|
| `SelfDestructMiddleware()` | 自毁模式下阻止写操作 |
| `SelfDestructInfoMiddleware()` | 注入自毁模式响应头 |
| `IsSelfDestructEnabled()` | 查询自毁模式是否启用 |
| `RefreshSelfDestructMode()` | 强制从 DB 刷新自毁模式状态 |

## 依赖关系

| 依赖 | 用途 |
|------|------|
| [`github.com/gin-gonic/gin`](https://github.com/gin-gonic/gin) | 中间件基础类型 `gin.HandlerFunc` |
| [`github.com/gin-contrib/cors`](https://github.com/gin-contrib/cors) | `CORSMiddleware` 的 CORS 实现 |
| [`github.com/redis/go-redis/v9`](https://github.com/redis/go-redis) | `RedisRateLimiter` / `SlidingWindowRateLimiter` 分布式限流 |
| [`golang.org/x/time/rate`](https://pkg.go.dev/golang.org/x/time/rate) | `MemoryRateLimiter` 令牌桶算法 |
| [`github.com/google/uuid`](https://github.com/google/uuid) | `RequestLogMiddleware` 生成请求 ID |

### 内部包依赖

| 包 | 用途 |
|----|------|
| [`config`](../config/) | `HandleErrorsMiddleware` 读取 `DevMode` 控制错误回显 |
| [`db`](../db/) | `GetRedisClient()` 复用 Redis 连接；`AuthMiddleware` / `SelfDestructMode` 查询数据库 |
| [`models`](../models/) | `AuthMiddleware` token / 用户 / 应用模型；`CheckScopeMiddleware` scope 定义 |
| [`log`](../log/) | 请求日志、限流降级、错误记录 |

## 使用方

中间件在 [`router.go`](../router.go)（`package community`）中集中挂载，典型组合包括：

- 全局：`RequestLogMiddleware` → `HandleErrorsMiddleware` → `SecurityHeadersMiddleware` → `TimeoutMiddleware` → `Gzip` → `SelfDestructInfoMiddleware` → `SelfDestructMiddleware`
- 路由组：`AuthMiddleware` → `CheckAuthMiddleware` → `CheckScopeMiddleware`
- 内部 API：`InternalApiMiddleware`
- 高负载端点：`APIRateLimitMiddleware` + `RequestSizeLimitMiddleware`
