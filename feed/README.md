# feed (Feed 时间线系统)

社交平台 Feed 时间线与消息分发系统，基于 Redis Sorted Set 实现高性能的时间线缓存、转发聚合、分页查询与实时推送（Pub/Sub）。

支持 home（主页）、public（公共）、mention（提及）、direct（私信）、list（列表）、tag（话题）、notification（通知）等多种时间线类型，每个子模块通过 `init()` 自动监听 status/notification 事件，实现时间线的自动维护。

## 核心功能

- **多类型时间线**：home、public、mention、direct、list、tag、notification 七种时间线，覆盖主流社交场景
- **Redis Sorted Set 缓存**：以 status ID 作为 score，天然支持基于 `max_id` / `min_id` / `since_id` 的分页
- **转发聚合（Reblog Aggregation）**：同一原始状态在 `REBLOG_FALLOFF`（40）条范围内的多次转发自动聚合，避免时间线被刷屏
- **批量写入优化**：非转发状态使用 Redis Pipeline 批量写入多个时间线，显著降低网络开销
- **自动重建（Rebuild）**：时间线缓存缺失时自动从数据库重建，支持深度翻页回源数据库
- **实时推送**：基于 Redis Pub/Sub，仅在有客户端订阅时才推送（`PushUpdateRequired`），带 5 秒 TTL 订阅缓存
- **垃圾回收（GC）**：`GcTimelines()` 自动清理超过 `MAX_AGE_DAYS`（7 天）未更新的时间线键
- **事件驱动**：各子模块通过 `event.Register` 自动监听 `after_create` / `before_update` / `after_update` / `after_delete` 事件，无需手动调用

## 架构 / 结构

```
feed/
├── feed.go                  # 核心接口、类型、包级函数（向后兼容代理）
├── service.go               # FeedService - 封装 Redis 时间线操作
├── get_timeline_content.go  # GetStatusTimelineContent - 统一的分页读取入口
├── redis.go                 # RedisClient 接口定义
├── db_service.go            # DBService - 封装数据库查询（粉丝、关注等）
├── home/                    # 主页时间线
├── public/                  # 公共时间线
├── mention/                 # 提及时间线
├── direct/                  # 私信时间线
├── list/                    # 列表时间线
├── tag/                     # 话题时间线
└── notification/            # 通知时间线
```

### 子模块说明

| 子模块 | 包路径 | Redis Key 前缀 | Timeline ID | 说明 |
|--------|--------|---------------|-------------|------|
| `home` | `feed/home` | `feed:home:%s` | 账号 ID | 关注的人 + 自己 + 提及自己的状态 |
| `public` | `feed/public` | `feed:public:%s` | `all` / `media` / `local` / `remote` 等组合 | 公开可见的状态，支持 media/local/remote 过滤 |
| `mention` | `feed/mention` | `feed:mention:%s` | 账号 ID | 提及当前用户的状态（仅本地用户） |
| `direct` | `feed/direct` | `direct:%s` | 账号 ID | 私信通知，不缓存内容，仅推送 conversation 事件 |
| `list` | `feed/list` | `feed:list:%s` | 过滤条件 + 列表标题 | 自定义列表，仅含 30 天内有访问记录的列表 |
| `tag` | `feed/tag` | `feed:hashtag:%s` | 过滤条件 + 话题名 | 话题时间线，支持 local/remote/media 过滤 |
| `notification` | `feed/notification` | `feed:notification:%s` | 账号 ID | 通知列表，监听 `notification` 表的 `after_create` 事件 |

> 每个子模块均实现 `feed.Timeline` 接口，并在 `init()` 中注册事件监听器，自动维护对应的时间线缓存。

## 用法示例

### 读取时间线内容

所有时间线的读取统一通过子模块的 `GetTimelineContent` / `GetTimeLineContent` 函数，内部调用 `feed.GetStatusTimelineContent`：

```go
import (
    "github.com/kasuganosora/community/feed"
    "github.com/kasuganosora/community/feed/home"
    "github.com/kasuganosora/community/feed/public"
    "github.com/kasuganosora/community/feed/tag"
)

// 1. 读取主页时间线（分页）
params := &feed.ListParam{}
params.SetLimit(20)
params.SetMaxId(1000) // 向下翻页：取 id <= 1000 的状态

statuses, err := home.GetTimeLineContent(account, params)

// 2. 读取公共时间线（全部）
statuses, err = public.GetTimelineContentAll(&feed.ListParam{Limit: 20})

// 3. 读取公共时间线（仅含媒体）
statuses, err = public.GetTimelineContentMedia(&feed.ListParam{Limit: 20})

// 4. 读取话题时间线（带 local/media 过滤）
tagParams := &feed.ListParam{
    Limit: 20,
    Other: map[string]any{
        "local":      true,
        "only_media": true,
    },
}
statuses, err = tag.GetTimelineContent("golang", account, tagParams)
```

### 手动向时间线添加 / 移除状态

通常事件监听器会自动处理，但也可以手动调用：

```go
import (
    "github.com/kasuganosora/community/feed"
    "github.com/kasuganosora/community/feed/home"
)

// 手动推送状态到主页时间线（并推送到订阅的客户端）
err := home.PushToHome(account, status, false /* update */, true /* pushToClient */)

// 手动从主页时间线移除状态
err = home.UnPushFromHome(account, status, true)

// 手动触发主页时间线重建
homeTimeline := &home.HomeTimeline{Account: account}
err = homeTimeline.Rebuild()
```

### 直接操作 feed 核心服务

```go
import (
    "github.com/kasuganosora/community/feed"
    "github.com/kasuganosora/community/feed/mention"
)

// 向提及时间线添加状态（aggregateReblogs = true 启用转发聚合）
mentionTimeline := &mention.MentionTimeline{Account: account}
err := feed.AddToTimelineItem(mentionTimeline, status, true)

// 裁剪时间线到 MAX_ITEMS 上限
err = feed.TrimTimeline(mentionTimeline)

// 检查是否有客户端正在订阅该时间线
if feed.PushUpdateRequired(mentionTimeline) {
    // 推送实时更新到订阅的客户端
    err = feed.PushToClient(mentionTimeline, "update", status)
}

// 获取该时间线的 Redis Pub/Sub channel 名称
channel := feed.GetRedisChannelName(mentionTimeline)

// 清理指定 feed ID 列表的时间线缓存
err = feed.CleanFeeds(mentionTimeline, []string{"123", "456"})

// 回收超过 MAX_AGE_DAYS 的过期时间线
err = feed.GcTimelines()
```

### 自定义 FeedService 实例（测试场景）

```go
import (
    "github.com/kasuganosora/community/feed"
)

// 创建自定义 FeedService（注入 mock Redis）
service := feed.NewFeedService(mockRedisClient)

// 设置为全局默认实例（用于测试，覆盖延迟初始化）
feed.SetDefaultService(service)

// 创建自定义 DBService
dbService := feed.NewDBService(testDB)
feed.SetDefaultDBService(dbService)

// 重置订阅缓存（用于测试）
feed.ResetSubscriptionCache()
```

## 核心类型与函数

### 接口

#### `Timeline`（核心接口）

所有时间线类型的基础接口：

```go
type Timeline interface {
    GetKeyFormat() string   // 返回 Redis key 格式模板，如 "feed:home:%s"
    GetTimelineId() string  // 返回时间线唯一标识，如账号 ID
    Rebuild() error         // 从数据库重建时间线缓存
}
```

#### `RedisClient`

feed 模块所需的 Redis 操作接口，封装了 ZSet / Set / Pub/Sub / Pipeline / Scan 等命令：

```go
type RedisClient interface {
    ZAdd(ctx, key, members) *redis.IntCmd
    ZRem(ctx, key, members) *redis.IntCmd
    ZRange(ctx, key, start, stop) *redis.StringSliceCmd
    // ... 共 24 个方法，覆盖 ZSet/Set/Pub/Sub/Scan/Pipeline
}
```

#### `DBClient`

数据库操作接口，封装 GORM 链式调用方法（`Model` / `Where` / `Find` / `Preload` / `Order` / `Limit` / `Offset` / `Association`）。

### 类型

#### `FeedService`

封装 feed 相关的 Redis 操作，线程安全。

| 方法 | 说明 |
|------|------|
| `AddToTimelineItem(timeline, status, aggregateReblogs)` | 将状态添加到时间线，支持转发聚合 |
| `BatchAddToTimelineItems(timelines, status, aggregateReblogs)` | 批量向多个时间线添加状态（Pipeline 优化） |
| `RemoveTimelineItem(timeline, status, aggregateReblogs)` | 从时间线移除状态 |
| `TrimTimeline(timeline)` | 裁剪时间线到 `MAX_ITEMS`（512）上限，清理过期 reblog 集合 |
| `CleanFeeds(timeline, feedIds)` | 清理指定 feed ID 列表的所有 Redis 键 |
| `PushToClient(timeline, eventType, eventPayload)` | 向时间线的 Redis channel 推送消息 |
| `PushUpdateRequired(timeline)` | 检查是否有客户端订阅（带 5 秒 TTL 缓存） |
| `GcTimelines()` | 回收超过 `MAX_AGE_DAYS`（7 天）未更新的时间线 |

#### `DBService`

封装 feed 相关的数据库查询操作。

| 方法 | 说明 |
|------|------|
| `GetAccountFollowersId(accountId)` | 获取账号的粉丝 ID 列表 |
| `GetAccountRecentActiveLocalFollowersId(accountId)` | 获取账号最近 10 天活跃的本地粉丝 ID |
| `GetAccountFollowingAccounts(accountId)` | 获取账号关注的所有账号 |
| `GetAccountRemoteFollowers(accountId)` | 获取账号的远程粉丝 |

#### `ListParam`

分页查询参数：

| 字段 | 类型 | 说明 |
|------|------|------|
| `MaxId` | `uint64` | 返回 ID <= 此值的状态（向下翻页） |
| `MinId` | `uint64` | 返回 ID >= 此值的状态（向上翻页） |
| `SinceId` | `uint64` | 返回 ID > 此值的状态（获取新增） |
| `Limit` | `uint64` | 返回数量上限 |
| `Other` | `map[string]any` | 额外参数（如 `local` / `remote` / `only_media`） |

> 链式 Setter：`SetMaxId()` / `SetMinId()` / `SetSinceId()` / `SetLimit()`

#### `FilterCondition` / `AfterFilter`

`GetStatusTimelineContent` 的过滤回调类型：

```go
type FilterCondition func(tx *gorm.DB) (query *gorm.DB, err error)  // 数据库查询过滤（回源时使用）
type AfterFilter func(statuses models.Statuses) models.Statuses     // 内存后置过滤（读取后使用）
```

### 常量

| 常量 | 值 | 说明 |
|------|----|------|
| `MAX_ITEMS` | `512` | 每条时间线最大缓存条数 |
| `REBLOG_FALLOFF` | `40` | 转发聚合范围（前 40 条内同源转发自动聚合） |
| `MAX_AGE_DAYS` | `7` | 时间线过期天数（GC 清理阈值） |

### 包级函数

以下函数为向后兼容的代理函数，内部委托给全局默认的 `FeedService` / `DBService` 实例（延迟初始化，使用 `sync.Once`）：

| 函数 | 说明 |
|------|------|
| `AddToTimelineItem(timeline, status, aggregateReblogs)` | 代理 `FeedService.AddToTimelineItem` |
| `BatchAddToTimelineItems(timelines, status, aggregateReblogs)` | 代理 `FeedService.BatchAddToTimelineItems` |
| `RemoveTimelineItem(timeline, status, aggregateReblogs)` | 代理 `FeedService.RemoveTimelineItem` |
| `TrimTimeline(timeline)` | 代理 `FeedService.TrimTimeline` |
| `CleanFeeds(timeline, feedIds)` | 代理 `FeedService.CleanFeeds` |
| `PushToClient(timeline, eventType, eventPayload)` | 代理 `FeedService.PushToClient` |
| `PushUpdateRequired(timeline)` | 代理 `FeedService.PushUpdateRequired` |
| `GcTimelines()` | 代理 `FeedService.GcTimelines` |
| `GetRedisChannelName(timeline)` | 生成 `subscribed:<key>` 格式的 channel 名称 |
| `GetStatusTimelineContent(timeline, params, filter, afterFilter)` | 统一的时间线分页读取入口 |
| `GetAccountFollowersId(accountId)` | 代理 `DBService.GetAccountFollowersId` |
| `GetAccountRecentActiveLocalFollowersId(accountId)` | 代理 `DBService.GetAccountRecentActiveLocalFollowersId` |
| `GetAccountFollowingAccounts(accountId)` | 代理 `DBService.GetAccountFollowingAccounts` |
| `GetAccountRemoteFollowers(accountId)` | 代理 `DBService.GetAccountRemoteFollowers` |
| `SetDefaultService(service)` | 设置全局默认 `FeedService`（用于测试） |
| `SetDefaultDBService(service)` | 设置全局默认 `DBService`（用于测试） |
| `ResetSubscriptionCache()` | 重置订阅状态缓存（用于测试） |

## 依赖关系

### 内部依赖

| 包 | 用途 |
|----|------|
| `github.com/kasuganosora/community/db` | 数据库与 Redis 连接（`GetDB()` / `GetRedis()`） |
| `github.com/kasuganosora/community/models` | 数据模型（`Status` / `Account` / `Notification` / `Follow` 等） |
| `github.com/kasuganosora/community/event` | 事件总线，子模块通过 `event.Register` 自动监听 status / notification 事件 |
| `github.com/kasuganosora/community/log` | 日志输出 |
| `github.com/kasuganosora/community/utils` | 工具函数（`UniqueSlice` / `Min` / `GetExistsMap` 等） |
| `github.com/kasuganosora/community/serializer` | 通知序列化（`serializer.ToNotification`，仅 `notification` 子模块） |

### 外部依赖

| 包 | 用途 |
|----|------|
| `github.com/redis/go-redis/v9` | Redis 客户端，Sorted Set / Set / Pub/Sub / Pipeline / Scan |
| `gorm.io/gorm` | ORM，数据库查询与关联预加载 |
| `github.com/pkg/errors` | 错误包装（`errors.Wrap`） |

### Redis 数据结构

| Key 模式 | 类型 | 说明 |
|----------|------|------|
| `feed:<type>:<id>` | Sorted Set | 时间线主集合，member = status ID，score = status ID（天然按时间排序） |
| `feed:<type>:<id>:reblogs` | Sorted Set | 转发聚合集合，member = 原始 status ID |
| `feed:<type>:<id>:reblogs:<reblogId>` | Set | 同源转发详情集合 |
| `timelines` | Sorted Set | 全局时间线索引，用于 GC，member = key，score = 更新时间戳 |
| `subscribed:feed:<type>:<id>` | Pub/Sub Channel | 实时推送通道 |
