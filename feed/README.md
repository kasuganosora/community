# Feed 时间线

SQL 是状态、关系和可见性的权威来源。Redis v3 缓存保存最多 512 个候选状态 ID；读取仍检查当前权限、过滤条件和转发聚合，深分页通过 SQL 补足。缓存不是授权依据，也不能用空结果掩盖 Redis 故障。

运行配置、部署与回滚步骤见 [Feed 运行指南](../docs/feed-runtime.md)。

## 运行模式

`ConfigureRuntime` 支持三种模式：

- `durable`：使用持久事件及投递 worker 维护 v3 投影，读取可使用 v3 缓存。
- `shadow`：用于投影验证；用户读取强制走 SQL。
- `legacy`：回滚模式，保留旧事件监听路径；用户读取同样强制走 SQL，避免读取旧缓存遗留数据。

旧 `AddToTimelineItem`、事件监听及 Pub/Sub 辅助代码仅供兼容路径使用。新 worker 必须使用下述投影协议；不能直接写 Sorted Set。v3 不依赖旧全局 `timelines` 集合做正确性判断或垃圾回收。

## 文件与职责

| 文件或目录 | 职责 |
| --- | --- |
| `protocol.go` | key、epoch、投递版本、子批次 fencing、批量 Lua 投影 |
| `rebuild.go` | SQL 快照发布的 CAS 与聚合索引构建 |
| `invalidate.go` | 原子失效；保留版本与投递 fence |
| `get_timeline_content.go` | 当前权限过滤、分页、有限 SQL 回源 |
| `redis.go`、`metadata.go` | UniversalClient 适配、逐主节点元数据扫描 |
| `runtime.go`、`activity.go` | 模式切换、活跃用户记录 |
| `worker/` | 持久队列消费、领取子批次、加载当前 SQL 状态并投递 |
| `home/`、`public/`、`mention/`、`list/`、`tag/` | 各类时间线的候选 SQL 与重建 |
| `direct/`、`notification/` | 私信与通知入口；HTTP 读取另有直接 SQL 路径 |

## v3 key 与生命周期

使用 `TimelineKey(t)` 取得规范 key。例如主页为：

```text
feed:v3:{home:123}:items
feed:v3:{home:123}:items:meta
feed:v3:{home:123}:items:versions
feed:v3:{home:123}:items:version_expiry
feed:v3:{home:123}:items:rebuild
feed:v3:{home:123}:items:fence:<batch-id>
```

同一时间线的 key 共用 Redis Cluster hash tag。列表按 ID 标识，旧 `feed:list:v2:<id>` 会规范化为 `{list:<id>}`，不再使用可重名的标题。状态 ID 同时作为成员与 score；转发聚合保留 `REBLOG_FALLOFF` 范围内的既有语义。

元数据包含随机 `epoch`、递增 `generation` 与 `ready`。缺失或不完整的元数据不能视为有效空缓存。缓存、对象版本和删除 tombstone 使用七天保留期限；活跃版本表按有界批次清理过期对象。Redis 必须使用 `noeviction`，不能把正确性元数据当可任意淘汰的缓存。

## 重建与投递协议

重建必须先调用 `BeginRebuild`，再执行候选 SQL，最后 `PublishRebuild`，并始终 `defer ReleaseRebuild`。发布仅在 epoch、generation 和重建 token 都未变化时成功。因此重建期间的低 ID 迟到事件、删除或失效不会被旧 SQL 快照覆盖。各时间线的 `RebuildContext` 已封装该流程。

worker 对缺少元数据的冷时间线调用 `PrepareProjectionEpochs`：它只创建 `ready=false` 的元数据，不发布空快照，也不为每个受众查询完整时间线。取得 epoch 后必须重新加载当前 SQL 对象与版本，再执行投递。旧尝试不能改用新 epoch 继续投递旧快照。

| API | 用途 |
| --- | --- |
| `ReadTimelineEpoch` / `ReadTimelineEpochs` | 只读检查，不创建元数据 |
| `PrepareProjectionEpochs` | 批量初始化冷时间线，随后必须重新查询 SQL |
| `ApplyProjection` / `BatchApplyProjections` | 受 epoch、对象版本和子批次租约保护的投递 |
| `InvalidateTimelineContext` | 管理、导入等维护路径的原子失效 |
| `ScanTimelineMetadata` | 单实例或 Cluster 各主节点扫描；回调可能并发执行 |

`Projection` 携带子批次 ID、领取 generation/token、剩余租期、对象版本与目标 epoch。批量接口用 pipeline 执行每个时间线的同槽 Lua，逐项返回错误，不使用跨槽事务。相同领取 generation 的重放不会延长原截止时间。

删除记录 tombstone，较旧的创建不能复活对象。聚合投递需要权威重建时标记 `ready=false`。`Invalidate=true` 也遵守租约 fencing；同批次同 epoch 的失效重放不会再次清除其后已重建的快照。

错误必须区分处理：

- `ErrSuperseded`：已有更新对象版本覆盖当前旧事件，可以确认旧投递。
- `ErrStaleProjection`：子批次租约、token 或 generation 无效，不能确认投递。
- `ErrNeedsRebuild`：元数据缺失或 epoch 不匹配，不能给旧 SQL 快照换 epoch 后重试。
- Redis 传输错误：不能确认成功，按持久队列重试。

## 读取与故障边界

读取使用请求 context，只有元数据完整且 `ready=true` 才使用候选缓存。冷缓存重建限制为进程内最多 8 个并发；竞争或 Redis 故障走有界 SQL 回源，最多 16 个并发、每次最多 5 秒。容量不足返回 `ErrFeedUnavailable`，不会返回假空页。取消会传递给 Redis 与 SQL。

`ForceDatabaseReads()` 使 legacy/shadow 模式跳过候选缓存。`RecordActivity` 在已登录读取及新实时连接路径记录活跃状态；其失败不直接阻止用户读请求。

## Redis 与 Cluster

Feed 使用同一个 `redis.UniversalClient`。`REDIS_FEED_CLUSTER_ADDRS` 非空时选择 Cluster，使用逗号分隔节点地址、数据库 0 和 `REDIS_0_PASSWORD`；未设置时复用单实例客户端。具体运行约束以运行指南为准。

Cluster 元数据扫描必须使用 `ScanTimelineMetadata`，不能把单节点 SCAN 游标当全 Cluster 游标。pipeline 遇到传输故障时请求刷新 Cluster 拓扑，仍将原错误返回上层；刷新不是成功投递的证明。

## 验证

常规定向回归：

```sh
go test -p 1 ./feed/... ./cmd/tootctl/commands
```

真实 Cluster 集成测试默认跳过，设置 `FEED_CLUSTER_TEST_ADDRS` 后运行：

```sh
go test -p 1 ./feed -run TestFeedRealClusterProtocol -v
go test -p 1 ./cmd/tootctl/commands -run TestFeedStatsRealCluster -v
```

额外设置 `FEED_CLUSTER_TEST_FAILOVER=1` 会主动关闭一个主节点，仅可用于专门的可丢弃测试 Cluster。测试覆盖跨槽批量投递、版本与删除 tombstone、重建 CAS、实际 CLI 扫描及有界故障转移重试；不代表大规模容量验收。
