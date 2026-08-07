# server (HTTP 服务器)

封装 HTTP 服务器生命周期的管理模块，提供启动、信号监听（SIGINT / SIGTERM / SIGHUP）、优雅关闭（带超时）以及关闭钩子（PreShutdown → 关闭 HTTP → PostShutdown）功能。关闭过程严格遵循组件依赖顺序释放资源：先停流量入口，再关闭 DB / Redis 等底层依赖。

## 核心功能

- **优雅关闭（Graceful Shutdown）**：收到关闭信号后，停止接收新连接并等待在途请求完成，最后释放底层资源。`Shutdown()` 基于 `atomic.Bool` + `CompareAndSwap` 保证幂等，重复调用返回 `ErrAlreadyShutdown`。
- **信号处理（Signal Handling）**：通过 `signal.Notify` 监听 `SIGINT`、`SIGTERM`、`SIGHUP`；`Start()` 在返回时通过 `signal.Stop` 解除通道绑定，避免包级 runtime 信号状态长期持有已失效的通道导致泄漏。
- **超时控制（Timeout Control）**：可配置 `ReadTimeout` / `WriteTimeout` / `IdleTimeout` 防御 Slowloris 等慢速攻击；`ShutdownTimeout` 控制优雅关闭的最长等待时间，对 0 / 负值做兜底（默认 30s），避免 `context` 立即取消导致关闭流程瞬间失败。
- **关闭钩子（Shutdown Hooks）**：支持 `PreShutdown`（HTTP 仍在运行前的准备）与 `PostShutdown`（HTTP 停止后才关闭 DB / Redis）两组钩子，单个钩子失败不会中断后续钩子，错误会被记录并汇总返回。

## 用法示例

### 最简启动（使用默认配置）

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/kasuganosora/community/server"
)

func main() {
    router := gin.Default()
    // 注册路由 ...

    // NewWithDefault 使用 DefaultOptions()：监听 :8080，超时分别为 10s/30s/120s
    srv := server.NewWithDefault(router)
    if err := srv.Start(); err != nil {
        log.Fatal("server exited: %v", err)
    }
}
```

> `Start()` 会阻塞，直到收到关闭信号或启动失败。

### 带自定义配置的优雅启动

通过 `Options` 自定义监听地址与各类超时：

```go
opts := server.Options{
    ListenAddr:      ":8080",
    ReadTimeout:     10 * time.Second,
    WriteTimeout:    30 * time.Second,
    IdleTimeout:     120 * time.Second,
    ShutdownTimeout: 30 * time.Second,
}

srv := server.New(router, opts)

// 注册关闭后钩子：HTTP 停止后释放自定义资源
srv.RegisterPostShutdown(func() error {
    return myResource.Close()
})

if err := srv.Start(); err != nil && !errors.Is(err, server.ErrAlreadyShutdown) {
    log.Fatal("server exited: %v", err)
}
```

### 一键式优雅启动（内置 DB / Redis 清理）

`GracefulStart` 内部已注册默认的资源清理钩子（`database.CloseDB()` 与逐个关闭 Redis 客户端），无需手动管理：

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/kasuganosora/community/server"
)

func main() {
    router := gin.Default()
    // 注册路由、初始化 DB / Redis ...

    opts := server.DefaultOptions()
    opts.ListenAddr = ":8080"

    // 内部会：New -> RegisterPostShutdown(DB/Redis) -> Start
    if err := server.GracefulStart(router, opts); err != nil {
        log.Fatal("server exited: %v", err)
    }
}
```

### 基于已有 *http.Server 的信号监听

如果你已经自行创建了 `*http.Server`，可使用 `SetupGracefulShutdown` 仅附加信号监听与清理逻辑：

```go
srv := &http.Server{
    Addr:         ":8080",
    Handler:      router,
    ReadTimeout:  10 * time.Second,
    WriteTimeout: 30 * time.Second,
    IdleTimeout:  120 * time.Second,
}

// 注册信号监听；收到信号后先 http.Server.Shutdown，再执行 cleanupFuncs
server.SetupGracefulShutdown(srv, 30*time.Second, func() {
    database.CloseDB()
})

log.Info("Server starting on %s", srv.Addr)
if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
    log.Fatal("Server start failed: %v", err)
}
```

### 简化版启动

`RunServer` 使用内置默认超时，适合不需要精细配置的场景：

```go
server.RunServer(router, ":8080", func() {
    database.CloseDB()
})
```

## 配置说明

### Options

服务器配置选项，可通过 `DefaultOptions()` 获取一组合理默认值，再按需覆盖。

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `ListenAddr` | `string` | `":8080"` | 监听地址 |
| `ReadTimeout` | `time.Duration` | `10s` | 读取请求超时，防御 Slowloris 等慢速攻击 |
| `WriteTimeout` | `time.Duration` | `30s` | 写响应超时 |
| `IdleTimeout` | `time.Duration` | `120s` | 空闲连接超时 |
| `ShutdownTimeout` | `time.Duration` | `30s` | 优雅关闭最长等待时间；设为 0 或负值时回退到 30s |
| `PreShutdown` | `[]func() error` | `[]` | HTTP 仍在运行前执行的关闭前钩子 |
| `PostShutdown` | `[]func() error` | `[]` | HTTP 停止后执行的关闭后钩子（用于释放 DB / Redis 等） |

### 监听的信号

| 信号 | 触发场景 |
|------|----------|
| `SIGINT` | `Ctrl+C` 中断 |
| `SIGTERM` | Kubernetes / systemd 等进程管理器发送的终止信号 |
| `SIGHUP` | 终端挂起（仅 `Server.Start()` 与 `GracefulStart` 监听） |

> `SetupGracefulShutdown` 与 `RunServer` 仅监听 `SIGINT` 与 `SIGTERM`。

### 关闭顺序

`Shutdown()` / `GracefulStart` / `SetupGracefulShutdown` 均严格遵循以下依赖顺序：

```
PreShutdown 钩子
      │
      ▼
http.Server.Shutdown(ctx)   ← 停止接收新请求，等待在途请求完成
      │
      ▼
PostShutdown 钩子           ← 关闭 DB / Redis 等底层资源
```

该顺序确保在途请求在 HTTP 关闭过程中仍可访问 DB / Redis，不会被提前释放的连接打断。

## 核心类型与函数

### 类型

#### `Server`

HTTP 服务器，线程安全（内部使用 `sync.Mutex` 保护 `httpServer` / `shutdownCh`，`atomic.Bool` 保证 `Shutdown` 幂等）。

#### `Options`

服务器配置选项，见 [配置说明](#配置说明)。

### 函数

#### 包级函数

| 函数 | 说明 |
|------|------|
| `New(router *gin.Engine, opts Options) *Server` | 使用指定配置创建服务器 |
| `NewWithDefault(router *gin.Engine) *Server` | 使用 `DefaultOptions()` 创建服务器 |
| `DefaultOptions() Options` | 返回默认配置（监听 `:8080`，超时 10s/30s/120s/30s） |
| `GracefulStart(router *gin.Engine, opts Options) error` | 一键优雅启动，内置 DB / Redis 清理钩子 |
| `SetupGracefulShutdown(server *http.Server, shutdownTimeout time.Duration, cleanupFuncs ...func())` | 为已有 `*http.Server` 附加信号监听与清理逻辑 |
| `RunServer(router *gin.Engine, addr string, cleanupFuncs ...func())` | 简化版启动（内置默认超时） |

#### `Server` 方法

| 方法 | 说明 |
|------|------|
| `Start() error` | 启动服务器并阻塞，直到收到关闭信号或启动失败；返回时通过 `signal.Stop` 解除信号绑定 |
| `Shutdown() error` | 优雅关闭，幂等；重复调用返回 `ErrAlreadyShutdown` |
| `RegisterPreShutdown(fn func() error)` | 注册关闭前钩子（HTTP 仍在运行前执行） |
| `RegisterPostShutdown(fn func() error)` | 注册关闭后钩子（HTTP 停止后执行，用于释放底层资源） |

#### 错误

| 变量 | 说明 |
|------|------|
| `ErrAlreadyShutdown` | 对已关闭（或正在关闭）的服务器重复调用 `Shutdown` 时返回 |

## 依赖关系

### 内部依赖

| 包 | 用途 |
|----|------|
| `github.com/kasuganosora/community/db/database` | `GracefulStart` 的 `PostShutdown` 钩子调用 `database.CloseDB()` 关闭数据库连接 |
| `github.com/kasuganosora/community/db/redis` | `GracefulStart` 的 `PostShutdown` 钩子通过 `GetRedises()` 逐个关闭 Redis 客户端 |
| `github.com/kasuganosora/community/log` | 记录启动、信号接收、关闭等生命周期日志 |

### 外部依赖

| 包 | 用途 |
|----|------|
| `github.com/gin-gonic/gin` | HTTP 路由引擎（`*gin.Engine` 作为 handler） |
| 标准库 `net/http` | `http.Server` 及其 `Shutdown` 方法实现优雅关闭 |
| 标准库 `os/signal` / `syscall` | 信号监听（`SIGINT` / `SIGTERM` / `SIGHUP`） |
| 标准库 `sync` / `sync/atomic` | 并发保护（`sync.Mutex`）与幂等关闭（`atomic.Bool`） |
| 标准库 `context` | 创建带超时的关闭上下文 |
| 标准库 `time` | 超时配置 |

> `GracefulStart` 通过 `cRedis.GetRedises()` 而非 `db.GetRedis()` 获取 Redis 客户端，因为后者在未配置 Redis 时会 panic，而前者仅返回空切片，可安全调用。
