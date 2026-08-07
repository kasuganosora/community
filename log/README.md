# log (日志系统)

基于 [logrus](https://github.com/sirupsen/logrus) 的结构化日志模块，集成 [lumberjack](https://github.com/natefinch/lumberjack) 实现文件轮转、磁盘容量监控与自动清理，并提供开箱即用的 Gin HTTP handler 用于日志统计与健康检查。

## 核心功能

- **多级别日志**：支持 `Debug` / `Info` / `Warn` / `Error` / `Fatal` / `Panic` 六个级别，并提供 `FatalIf`、`ErrorV` 等便捷函数。
- **结构化日志**：通过 `WithField` / `WithFields` / `WithError` 附加键值字段，支持 `text` 与 `json` 两种输出格式。
- **文件轮转**：基于 lumberjack 实现，可配置单文件大小上限、保留备份数、保留天数及 gzip 压缩。
- **磁盘容量守护**：可设置日志目录最大占用，超过阈值（80%）时按修改时间自动清理最旧的日志文件，避免磁盘写满。
- **HTTP Handler**：提供 `StatsHandler` 与 `HealthHandler`，可直接挂载到 Gin 路由，暴露日志统计与健康状态。
- **安全格式化**：`WriteLog` 仅在传入参数时才执行 `Sprintf`，避免消息中包含 `%` 字符（如 URL、百分比、用户输入）被误解析为格式化动词。
- **资源管理**：`InitLogger` 多次调用会自动关闭旧的文件写入器；`CloseLog` 用于程序优雅关闭时释放文件句柄。

## 目录结构

```
log/
├── log.go              # 核心：日志配置、初始化、轮转、多级别输出、结构化字段
├── handler.go          # Gin HTTP handler：日志统计与健康检查端点
├── log.go_test.go      # log.go 单元测试
├── log_rotation_test.go# 轮转/磁盘清理相关测试
└── handler_test.go     # handler 单元测试
```

> 所有功能均在 `log` 包内，无子包。

## 快速开始

### 1. 初始化日志

```go
import "github.com/kasuganosora/community/log"

func main() {
    // 使用默认安全配置（单文件 100MB、保留 10 个备份、30 天、启用压缩、目录上限 1GB）
    log.InitLogger(log.DefaultLogConfig())

    // 程序退出时释放文件句柄
    defer log.CloseLog()

    // ... 业务逻辑
}
```

### 2. 输出日志

```go
// 基础级别日志，支持 printf 风格格式化
log.Info("server started on %s:%d", host, port)
log.Warn("config item %q is deprecated", key)
log.Error("failed to connect database: %v", err)

// 便捷函数：仅当 err != nil 时记录
log.ErrorV(err)
log.FatalIf(err) // err != nil 时记录 Fatal 并 os.Exit(1)
```

### 3. 结构化日志

```go
// 附加单个字段
log.WithField("user_id", 12345).Info("user login")

// 附加多个字段
log.WithFields(map[string]any{
    "module": "auth",
    "ip":     "10.0.0.1",
}).Info("authentication succeeded")

// 附加错误字段
log.WithError(err).Error("request failed")
```

`json` 格式下的输出示例：

```json
{"level":"info","msg":"user login","time":"2024-05-01T12:00:00+08:00","user_id":12345}
```

## 配置

### `LogConfig` 字段

| 字段 | 类型 | 说明 | 默认值 |
|------|------|------|--------|
| `Level` | `string` | 日志级别：`debug` / `info` / `warn` / `error` | `info` |
| `Output` | `string` | 输出目标：`stdout` / `file` / `both` | `stdout` |
| `FilePath` | `string` | 日志文件路径（目录不存在时自动创建） | `logs/community.log` |
| `MaxSize` | `int` | 单个日志文件最大大小（MB），超过后触发轮转 | `100` |
| `MaxBackups` | `int` | 保留的旧日志文件个数 | `10` |
| `MaxAge` | `int` | 旧日志文件保留天数 | `30` |
| `Compress` | `bool` | 是否对轮转后的旧日志进行 gzip 压缩 | `true` |
| `Format` | `string` | 输出格式：`text` / `json` | `text` |
| `MaxDiskUsage` | `int64` | 日志目录最大磁盘使用量（MB），`0` 表示不限制 | `1024` |

> 所有 `<=0` 的轮转参数在 `InitLogger` 内会自动回退到默认值，确保日志不会无限增长占满磁盘。

### 输出模式

| `Output` | 行为 |
|----------|------|
| `stdout` | 仅输出到标准输出（默认） |
| `file` | 仅输出到文件（通过 lumberjack 轮转） |
| `both` | 同时输出到标准输出与文件（`io.MultiWriter`） |

### 配置示例

```go
config := log.LogConfig{
    Level:        "debug",
    Output:       "both",                // 同时输出到控制台和文件
    FilePath:     "/var/log/community/community.log",
    MaxSize:      50,                    // 单文件 50MB 后轮转
    MaxBackups:   7,                     // 保留 7 个备份
    MaxAge:       14,                    // 保留 14 天
    Compress:     true,                  // 压缩旧日志
    Format:       "json",                // JSON 结构化输出
    MaxDiskUsage: 512,                   // 日志目录上限 512MB
}
log.InitLogger(config)
```

### 运行时调整级别

```go
// 动态修改日志级别（支持 DEBUG/INFO/WARN/ERROR/FATAL/PANIC，不区分大小写）
log.SetLevel("warn")
```

## HTTP Handler

包内提供两个 Gin handler，可直接注册到路由用于运维监控。

### 日志统计

```go
router := gin.Default()
router.GET("/api/v1/admin/log/stats", log.StatsHandler())
```

响应示例：

```json
{
  "config": {
    "max_size_mb": 100,
    "max_backups": 10,
    "max_age_days": 30,
    "compress": true,
    "max_disk_usage_mb": 1024
  },
  "current_size_mb": 25,
  "current_size_bytes": 26214400,
  "log_file_count": 3,
  "disk_usage_percent": 2.44,
  "is_near_limit": false
}
```

### 健康检查

```go
router.GET("/api/v1/health/log", log.HealthHandler())
```

- 当日志磁盘占用超过 80%（`is_near_limit == true`）时返回 `status: "warning"`，提示清理；
- 否则返回 `status: "healthy"`。

响应示例（告警状态）：

```json
{
  "status": "warning",
  "message": "Log disk usage is above 80%, cleanup recommended",
  "stats": { "...": "同 StatsHandler" }
}
```

> 两个 handler 均通过 [`GetCurrentLogStats`](#函数) 读取全局配置（由 `InitLogger` 自动设置），无需手动传参。

## 文件轮转与磁盘清理

轮转能力由 lumberjack 提供并在本包内增强：

1. **大小轮转**：单个日志文件超过 `MaxSize`（MB）时，自动重命名归档（如 `community.log` → `community-2024-05-01T12-00-00.log`）并创建新文件。
2. **数量保留**：归档文件数量超过 `MaxBackups` 时，删除最旧的归档。
3. **时效保留**：归档文件超过 `MaxAge` 天后自动删除。
4. **压缩归档**：`Compress` 为 `true` 时，归档文件会被 gzip 压缩为 `.gz`。
5. **目录容量守护**：每次初始化写入器时检查日志目录总大小，超过 `MaxDiskUsage` 的 80% 时按修改时间从旧到新删除，直至降至阈值以下。

## 核心 API

### 函数

#### 配置与生命周期

| 函数 | 说明 |
|------|------|
| `DefaultLogConfig() LogConfig` | 返回安全的默认日志配置 |
| `InitLogger(config LogConfig)` | 初始化日志系统（设置级别、格式、输出、轮转）；多次调用会关闭旧的文件写入器 |
| `CloseLog()` | 释放日志资源（关闭文件写入器），应在程序优雅关闭时调用 |
| `SetLevel(level string)` | 运行时设置日志级别 |
| `SetGlobalLogConfig(config LogConfig)` | 设置全局日志配置（供统计 handler 使用） |

#### 日志输出

| 函数 | 说明 |
|------|------|
| `WriteLog(level logrus.Level, msg string, args ...any)` | 通用日志写入，按级别输出 |
| `Debug(msg string, args ...any)` | Debug 级别日志 |
| `Info(msg string, args ...any)` | Info 级别日志 |
| `Warn(msg string, args ...any)` | Warn 级别日志 |
| `Error(msg string, args ...any)` | Error 级别日志 |
| `Fatal(msg string, args ...any)` | Fatal 级别日志，记录后调用 `os.Exit(1)` |
| `Panic(msg string, args ...any)` | Panic 级别日志 |
| `FatalIf(err error)` | 当 `err != nil` 时记录 Fatal 并退出 |
| `ErrorV(err error)` | 当 `err != nil` 时记录 Error |

#### 结构化字段

| 函数 | 说明 |
|------|------|
| `WithField(key string, value any) *logrus.Entry` | 创建带单个字段的日志条目 |
| `WithFields(fields map[string]any) *logrus.Entry` | 创建带多个字段的日志条目 |
| `WithError(err error) *logrus.Entry` | 创建带 `error` 字段的日志条目 |

#### 统计与底层对象

| 函数 | 说明 |
|------|------|
| `GetLogObject() *logrus.Logger` | 获取底层 `*logrus.Logger` 实例（同一全局单例） |
| `GetLogStats(config LogConfig) map[string]any` | 获取指定配置下的日志统计（目录大小、文件数、磁盘使用率等） |
| `GetCurrentLogStats() map[string]any` | 获取当前全局配置下的日志统计 |

#### HTTP Handler

| 函数 | 说明 |
|------|------|
| `StatsHandler() gin.HandlerFunc` | 返回日志统计信息的 Gin handler |
| `HealthHandler() gin.HandlerFunc` | 返回日志系统健康状况的 Gin handler（磁盘占用 >80% 时告警） |

### 类型

#### `LogConfig`

日志配置结构体，字段说明见[配置](#logconfig-字段)。

## 完整示例

```go
package main

import (
    "errors"

    "github.com/gin-gonic/gin"
    "github.com/kasuganosora/community/log"
)

func main() {
    // 初始化日志（文件 + 控制台双输出，JSON 格式）
    log.InitLogger(log.LogConfig{
        Level:        "info",
        Output:       "both",
        FilePath:     "logs/community.log",
        MaxSize:      100,
        MaxBackups:   10,
        MaxAge:       30,
        Compress:     true,
        Format:       "json",
        MaxDiskUsage: 1024,
    })
    defer log.CloseLog()

    // 普通日志
    log.Info("application starting")

    // 结构化日志
    log.WithFields(map[string]any{
        "service": "api",
        "version": "1.0.0",
    }).Info("service initialized")

    // 错误处理
    err := errors.New("database connection refused")
    log.WithError(err).Error("startup failed")

    // 暴露日志统计与健康检查端点
    router := gin.Default()
    router.GET("/api/v1/admin/log/stats", log.StatsHandler())
    router.GET("/api/v1/health/log", log.HealthHandler())

    log.FatalIf(router.Run(":8080"))
}
```

## 依赖关系

| 依赖 | 用途 |
|------|------|
| [`github.com/sirupsen/logrus`](https://github.com/sirupsen/logrus) | 结构化日志核心引擎 |
| [`gopkg.in/natefinch/lumberjack.v2`](https://github.com/natefinch/lumberjack) | 日志文件轮转（大小/数量/时效/压缩） |
| [`github.com/gin-gonic/gin`](https://github.com/gin-gonic/gin) | 提供 `StatsHandler` / `HealthHandler` 所需的 `gin.HandlerFunc` |

> 本包仅在被引用 HTTP handler 功能时才需要 `gin`；纯日志使用（`InitLogger` / `Info` / `WithFields` 等）仅依赖 `logrus` 与 `lumberjack`。
