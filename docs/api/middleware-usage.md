# 中间件使用文档

## 概述

本文档介绍 Community 服务器中新增的增强型中间件及其使用方法。

## 安全中间件

### SecurityHeadersMiddleware

添加安全 HTTP 头部，防止常见的 Web 攻击。

**功能：**
- `X-Content-Type-Options: nosniff` - 防止 MIME 类型嗅探
- `X-Frame-Options: DENY` - 防止点击劫持
- `X-XSS-Protection: 1; mode=block` - XSS 防护
- `Referrer-Policy: strict-origin-when-cross-origin` - 引用策略
- `Content-Security-Policy` - 内容安全策略

**使用方法：**
```go
app.Use(middleware.SecurityHeadersMiddleware())
```

### CORSMiddleware

处理跨域资源共享 (CORS) 请求。

**使用方法：**
```go
// 允许特定来源
app.Use(middleware.CORSMiddleware([]string{
    "https://example.com",
    "https://app.example.com",
}))

// 允许所有来源（开发环境）
app.Use(middleware.CORSMiddleware([]string{}))
```

## 超时控制中间件

### TimeoutMiddleware

为 HTTP 请求设置超时，防止长时间运行的请求占用资源。

**使用方法：**
```go
// 30 秒超时
app.Use(middleware.TimeoutMiddleware(30 * time.Second))
```

**注意：** 超时后会返回 504 Gateway Timeout 错误。

## 限流中间件

### RateLimitMiddleware

基于内存的简单限流，适合单机部署。

**使用方法：**
```go
limiter := middleware.NewMemoryRateLimiter(middleware.RateLimiterConfig{
    GlobalRPS:   100,
    GlobalBurst: 150,
})
app.Use(middleware.RateLimitMiddleware(limiter))
```

### MultiLevelRateLimitMiddleware

多级限流（全局 + IP + 用户），支持 Redis 分布式部署。

**使用方法：**
```go
// 使用 Redis
redisClient := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})
app.Use(middleware.MultiLevelRateLimitMiddleware(redisClient))

// 不使用 Redis（内存模式）
app.Use(middleware.MultiLevelRateLimitMiddleware(nil))
```

**限流规则：**
- 全局：1000 RPS
- 每 IP：600 RPM
- 每用户：300 RPM

## 日志中间件

### RequestLogMiddleware

记录 HTTP 请求详细信息，包括请求 ID、处理时间等。

**使用方法：**
```go
app.Use(middleware.RequestLogMiddleware())
```

## 监控指标中间件

### MetricsMiddleware

收集 Prometheus 格式的监控指标。

**使用方法：**
```go
app.Use(metrics.MetricsMiddleware())
```

**暴露的指标：**
- `community_http_requests_total` - HTTP 请求总数
- `community_http_request_duration_seconds` - 请求处理时间
- `community_active_connections` - 活跃连接数

**访问指标：**
```
GET /metrics
```

## 错误处理中间件

### ErrorHandler

统一的错误处理，将错误转换为标准格式。

**使用方法：**
```go
app.Use(errors.ErrorHandler())
```

## 健康检查端点

| 端点 | 说明 |
|------|------|
| `GET /health` | 简单健康检查 |
| `GET /health/detailed` | 详细健康检查 |
| `GET /live` | Kubernetes 存活探针 |
| `GET /ready` | Kubernetes 就绪探针 |
