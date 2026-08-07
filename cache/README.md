# cache (缓存系统)

基于 Redis 的统一缓存模块，为 community 项目提供高性能、高可用的缓存能力。封装了 [go-redis v9](https://github.com/redis/go-redis) 客户端，内置 **缓存击穿（stampede）保护** 与 **缓存雪崩（avalanche）防御** 机制，并覆盖 Redis 的主要数据结构（String / Hash / List / Set / Sorted Set）。

## 核心功能

| 能力 | 说明 |
|------|------|
| **统一缓存接口** | 在 `*Cache` 之上封装所有 Redis 操作，自动附加 key 前缀（`community:`），与业务逻辑解耦 |
| **缓存击穿保护 (singleflight)** | `GetOrFetch` 通过 singleflight 合并同一 key 的并发回源请求，保证同一时刻只有一个 goroutine 执行 `fetch`，避免热点 key 失效瞬间打穿到数据库 |
| **缓存雪崩防御 (TTL 抖动)** | 所有写入操作（`Set` / `SetString` / `GetOrFetch` 回写）都会在 TTL 上叠加 0~10% 的随机抖动，避免大量 key 同时过期 |
| **统一错误语义** | `redis.Nil`（key 不存在）被转换为 `ErrCacheMiss`，业务侧用 `IsErrCacheMiss` 判断缓存未命中；空键校验返回 `ErrEmptyKey` |
| **全数据结构支持** | String、Hash、List、Set、Sorted Set 全覆盖，值通过 `encoding/json` 自动序列化/反序列化 |
| **安全删除** | `DeleteByPattern` 使用 `SCAN` 替代 `KEYS`，避免阻塞 Redis，并响应 `context` 取消 |
| **领域便捷函数** | 针对 Account / Status 等高频对象提供开箱即用的缓存读写与失效函数 |

## 架构与结构

```
cache/
├── cache.go          # 核心：Cache 类型、所有缓存操作、GetOrFetch、领域便捷函数、常量
├── singleflight.go   # singleflight 实现、TTL 抖动 (jitterTTL)、错误包装 (wrapRedisErr)
├── cache_test.go            # 基于 Redis 的集成测试
├── cache_review_test.go     # 基于 miniredis / mock 的单元测试（含 singleflight 并发测试）
└── cache_mock_test.go       # Redis mock 辅助
```

### 调用流程

```
业务代码
   │
   │  GetCache().Set / Get / GetOrFetch ...
   ▼
┌─────────────────────────────── Cache ───────────────────────────────┐
│  checkKey()  ──空键校验──▶ ErrEmptyKey                              │
│  jitterTTL() ──TTL 抖动──▶ 防雪崩                                   │
│  wrapRedisErr() ─错误归一──▶ redis.Nil → ErrCacheMiss               │
│  GetOrFetch() ──击穿保护──▶ flightGroup.do() ──去重并发回源         │
│  prefix ("community:") ──────────▶ 自动加前缀                       │
└────────────────────────────────────┬────────────────────────────────┘
                                     ▼
                        github.com/redis/go-redis/v9
                                     ▼
                                  Redis Server
```

> Cache 复用 [`db/redis`](../db/redis) 包管理的全局 Redis 连接（`redis.GetRedis()`），不单独持有连接池，生命周期由 redis 包统一管理。

## 快速开始

### 1. 读写缓存（基本用法）

```go
import (
    "context"
    "time"

    "github.com/kasuganosora/community/cache"
)

type UserProfile struct {
    ID   uint64
    Name string
}

func example() error {
    ctx := context.Background()
    c := cache.GetCache() // 获取全局缓存实例（自动完成 Init）

    // 写入缓存（TTL 自动叠加随机抖动）
    profile := &UserProfile{ID: 1, Name: "alice"}
    if err := c.Set(ctx, "user:1", profile, cache.LongExpiration); err != nil {
        return err
    }

    // 读取缓存
    var result UserProfile
    if err := c.Get(ctx, "user:1", &result); err != nil {
        if cache.IsErrCacheMiss(err) {
            // 缓存未命中，回源数据库...
        }
        return err
    }

    // 缓存命中，使用 result
    _ = result
    return nil
}
```

### 2. GetOrFetch —— 缓存击穿保护（推荐读取模式）

`GetOrFetch` 是处理「缓存 + 回源」场景的推荐方式。先查缓存，命中直接返回；未命中则通过 singleflight 合并同一 key 的并发回源，只执行一次 `fetch` 并回写缓存。

```go
ctx := context.Background()
c := cache.GetCache()

type Status struct {
    ID      uint64 `json:"id"`
    Content string `json:"content"`
}

var status Status
hit, err := c.GetOrFetch(ctx, "status:42", &status, cache.LongExpiration, func() (any, error) {
    // 仅在缓存未命中时执行（同一 key 的并发请求只执行一次）
    return loadStatusFromDB(42) // 返回 *Status
})
if err != nil {
    return err
}

fmt.Println("缓存命中:", hit) // true=命中缓存，false=触发了回源
```

**关键设计点：**

- **singleflight 合并**：同一 key 的并发回源请求会被合并，只有一个 goroutine 执行 `fetch`，其余调用者共享其结果（结果在回调内只序列化一次，避免重复序列化）。
- **独立回写 context**：回写缓存使用带超时的独立 context（`writeBackTimeout = 3s`），与调用方的 ctx 解耦。即使调用方请求被取消（超时/客户端断开），缓存仍会被填充，从而维持击穿保护——这是本函数的核心目的。
- **`expiration <= 0` 不回写**：传入非正过期时间表示不缓存回源结果。

### 3. 领域便捷函数

针对 Account、Status 等高频对象，包提供了开箱即用的便捷函数，内部已处理 key 格式化与默认过期时间：

```go
ctx := context.Background()

// 账户缓存（使用 KeyAccount = "account:%d"，TTL = LongExpiration）
if err := cache.CacheAccount(ctx, accountID, account); err != nil {
    return err
}

var account Account
if err := cache.GetCachedAccount(ctx, accountID, &account); err != nil {
    if cache.IsErrCacheMiss(err) {
        // 回源...
    }
}

// 失效缓存（更新账户后调用）
if err := cache.InvalidateAccount(ctx, accountID); err != nil {
    return err
}

// 嘟文缓存（同理：CacheStatus / GetCachedStatus / InvalidateStatus）
_ = cache.CacheStatus(ctx, statusID, &status)
```

## 核心 API

### 包级函数

| 函数 | 说明 |
|------|------|
| `Init()` | 初始化全局缓存实例（使用 `sync.Once`，幂等；复用 `redis.GetRedis()`） |
| `GetCache() *Cache` | 获取默认缓存实例（内部保证已 `Init`） |
| `Close()` | 释放缓存资源（复用全局 Redis 连接，无需实际关闭） |
| `IsErrCacheMiss(err error) bool` | 判断是否为缓存未命中（支持 `errors.Is` 语义） |
| `IsErrEmptyKey(err error) bool` | 判断是否为空键错误 |

### `Cache` 类型

线程安全的缓存服务，通过 `GetCache()` 获取单例。所有方法均自动附加前缀 `community:`。

#### String 操作

| 方法 | 签名 | 说明 |
|------|------|------|
| `Set` | `(ctx, key string, value any, expiration time.Duration) error` | 写入任意可序列化值（JSON），TTL 带抖动 |
| `Get` | `(ctx, key string, dest any) error` | 读取并反序列化到 `dest`；未命中返回 `ErrCacheMiss` |
| `SetString` | `(ctx, key string, value string, expiration time.Duration) error` | 写入原始字符串 |
| `GetString` | `(ctx, key string) (string, error)` | 读取原始字符串 |

#### Key 管理

| 方法 | 说明 |
|------|------|
| `Delete(ctx, keys ...string) error` | 删除一个或多个 key |
| `DeleteByPattern(ctx, pattern string) error` | 按模式删除（使用 `SCAN`，响应 context 取消） |
| `Exists(ctx, key string) (bool, error)` | 检查 key 是否存在 |
| `Expire(ctx, key string, expiration time.Duration) error` | 设置/更新过期时间 |
| `TTL(ctx, key string) (time.Duration, error)` | 获取剩余过期时间 |

#### 计数器

| 方法 | 说明 |
|------|------|
| `Increment(ctx, key string) (int64, error)` | 自增 1 |
| `IncrementBy(ctx, key string, value int64) (int64, error)` | 指定增量自增 |
| `Decrement(ctx, key string) (int64, error)` | 自减 1 |

#### Hash 操作

| 方法 | 说明 |
|------|------|
| `HSet(ctx, key, field string, value any) error` | 设置哈希字段（值经 JSON 序列化） |
| `HGet(ctx, key, field string, dest any) error` | 获取哈希字段并反序列化；不存在返回 `ErrCacheMiss` |
| `HGetAll(ctx, key string) (map[string]string, error)` | 获取全部字段 |
| `HDel(ctx, key string, fields ...string) error` | 删除哈希字段 |

#### List 操作

| 方法 | 说明 |
|------|------|
| `LPush(ctx, key string, values ...any) error` | 左侧插入 |
| `RPush(ctx, key string, values ...any) error` | 右侧插入 |
| `LRange(ctx, key string, start, stop int64) ([]string, error)` | 获取范围 |
| `LLen(ctx, key string) (int64, error)` | 获取列表长度 |

#### Set 操作

| 方法 | 说明 |
|------|------|
| `SAdd(ctx, key string, members ...any) error` | 添加集合成员 |
| `SMembers(ctx, key string) ([]string, error)` | 获取所有成员 |
| `SIsMember(ctx, key string, member any) (bool, error)` | 检查是否为成员 |

#### Sorted Set 操作

| 方法 | 说明 |
|------|------|
| `ZAdd(ctx, key string, score float64, member string) error` | 添加有序集合成员 |
| `ZRange(ctx, key string, start, stop int64) ([]string, error)` | 按索引范围获取 |
| `ZRangeByScore(ctx, key, min, max string, offset, count int64) ([]string, error)` | 按分数范围获取 |
| `ZRem(ctx, key string, members ...any) error` | 删除有序集合成员 |

#### 高级读取模式

| 方法 | 说明 |
|------|------|
| `GetOrFetch(ctx, key string, dest any, expiration time.Duration, fetch func() (any, error)) (hit bool, err error)` | 带缓存击穿保护的读取模式 |

## 常量

### 缓存键模板

建议使用预定义的 key 模板，结合 `fmt.Sprintf` 生成最终 key：

| 常量 | 值 | 用途 |
|------|----|------|
| `KeyAccount` | `"account:%d"` | 账户（按 ID） |
| `KeyAccountByAcct` | `"account:acct:%s"` | 账户（按用户名） |
| `KeyStatus` | `"status:%d"` | 嘟文 |
| `KeyTimeline` | `"timeline:%d:%s"` | 时间线 |
| `KeyNotifications` | `"notifications:%d"` | 通知 |
| `KeyInstanceStats` | `"instance:stats"` | 实例统计 |
| `KeyTrendingTags` | `"trending:tags"` | 热门标签 |
| `KeyTrendingStatus` | `"trending:statuses"` | 热门嘟文 |
| `KeySearchResults` | `"search:%s"` | 搜索结果 |

### 默认过期时间

| 常量 | 值 | 适用场景 |
|------|----|---------|
| `ShortExpiration` | `1 * time.Minute` | 变化频繁、一致性要求高的数据 |
| `DefaultExpiration` | `5 * time.Minute` | 通用默认 |
| `LongExpiration` | `30 * time.Minute` | 变化较少（如账户、嘟文） |
| `VeryLongExpiration` | `24 * time.Hour` | 很少变化的静态数据 |

> 以上过期时间在写入时会自动叠加 0~10% 的随机抖动（`jitterTTL`），防止缓存雪崩。

## 错误处理

模块定义了两个语义化错误，建议始终通过辅助函数判断：

```go
var (
    ErrCacheMiss = errors.New("cache: key not found")
    ErrEmptyKey  = errors.New("cache: empty key")
)
```

```go
err := c.Get(ctx, key, &dest)
switch {
case cache.IsErrCacheMiss(err):
    // 缓存未命中 —— 正常情况，回源即可
case cache.IsErrEmptyKey(err):
    // 编程错误：传入了空 key
case err != nil:
    // 真正的 Redis 错误（连接失败等）
default:
    // 缓存命中
}
```

> `wrapRedisErr` 会将 go-redis 的 `redis.Nil` 统一转换为 `ErrCacheMiss`，业务侧无需感知底层 Redis 错误细节。

## 数据结构示例

### Hash

```go
ctx := context.Background()
c := cache.GetCache()

// 写入（值自动 JSON 序列化）
settings := map[string]int{"a": 1, "b": 2}
_ = c.HSet(ctx, "user:1:settings", "prefs", settings)

// 读取并反序列化
var result map[string]int
if err := c.HGet(ctx, "user:1:settings", "prefs", &result); err != nil {
    return err // 未命中返回 ErrCacheMiss
}

// 读取全部字段
all, _ := c.HGetAll(ctx, "user:1:settings")

// 删除字段
_ = c.HDel(ctx, "user:1:settings", "prefs")
```

### Sorted Set

```go
ctx := context.Background()
c := cache.GetCache()

_ = c.ZAdd(ctx, "leaderboard", 100.0, "alice")
_ = c.ZAdd(ctx, "leaderboard", 80.0,  "bob")
_ = c.ZAdd(ctx, "leaderboard", 95.0,  "carol")

// 按分数升序排名
ranked, _ := c.ZRange(ctx, "leaderboard", 0, -1)
// ["bob", "carol", "alice"]

// 取分数在 90~100 之间的成员
top, _ := c.ZRangeByScore(ctx, "leaderboard", "90", "100", 0, 10)
// ["carol", "alice"]

_ = c.ZRem(ctx, "leaderboard", "bob")
```

## 配置说明

Cache 本身无需配置，它复用 [`db/redis`](../db/redis) 包的全局 Redis 连接。Redis 连接参数从配置文件 / 环境变量读取：

```bash
# 默认 Redis 实例（cache 使用）
REDIS_0_HOST=127.0.0.1
REDIS_0_PORT=6379
REDIS_0_PASSWORD=
REDIS_0_DB=0

# 可选的额外 Redis 实例（按序号递增）
REDIS_1_HOST=127.0.0.1
REDIS_1_PORT=6380
REDIS_1_PASSWORD=
REDIS_1_DB=0
```

> Cache 默认使用第 0 个 Redis 实例（`redis.GetRedis()`），所有 key 自动附加前缀 `community:` 以便在共享 Redis 中隔离命名空间。

### 调优参数（源码内常量）

| 常量 | 值 | 位置 | 说明 |
|------|----|------|------|
| `jitterRatio` | `0.1` | `singleflight.go` | TTL 抖动上限比例（0~10%） |
| `writeBackTimeout` | `3 * time.Second` | `cache.go` | `GetOrFetch` 回写缓存的独立 context 超时 |

## 缓存击穿保护原理 (singleflight)

`GetOrFetch` 内部使用自实现的 `flightGroup`（等价于 `golang.org/x/sync/singleflight`，但不引入额外依赖）：

```
   请求A ─┐
   请求B ─┼─▶ flightGroup.do(key) ──▶ 仅 leader(A) 执行 fetch + 回写缓存
   请求C ─┘                              │
                                         ▼
                              结果序列化为 []byte（共享）
                                         │
                   ┌─────────────────────┼─────────────────────┐
                   ▼                     ▼                     ▼
                A 反序列化            B 反序列化            C 反序列化
```

- 同一 key 的并发调用被去重，`fetch` 只执行一次。
- 结果在回调内只 `json.Marshal` 一次，各调用者各自做一次 `json.Unmarshal`，避免重复序列化。
- `defer c.wg.Done()` 保证即使 leader 的 `fetch` panic，也不会永久阻塞等待者。

## 依赖关系

### 外部依赖

| 依赖 | 用途 |
|------|------|
| [`github.com/redis/go-redis/v9`](https://github.com/redis/go-redis) | Redis 客户端 |
| Go 标准库 `encoding/json` | 值的序列化/反序列化 |
| Go 标准库 `math/rand/v2` | TTL 抖动随机数 |
| Redis Server | 缓存后端 |

### 项目内依赖

| 依赖 | 说明 |
|------|------|
| [`github.com/kasuganosora/community/db/redis`](../db/redis) | 提供 `redis.GetRedis()` 全局 Redis 连接 |

> 该包**无第三方 singleflight 依赖**，`flightGroup` 为等价自实现，避免引入 `golang.org/x/sync`。

### 测试依赖

| 依赖 | 用途 |
|------|------|
| [`github.com/alicebob/miniredis/v2`](https://github.com/alicebob/miniredis) | 进程内 Redis，用于 singleflight 并发测试 |
| [`github.com/stretchr/testify`](https://github.com/stretchr/testify) | 测试断言 |
| [`go-redis/v9`](https://github.com/redis/go-redis) 的 `redismock` | 单元测试中的 Redis mock |
