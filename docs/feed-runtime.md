# Feed 运行与扩容

时间线使用同一个 Community 程序、SQL 数据库和 Redis。帖子与关系变更在业务事务内写入 `feed_events`，Worker 分批展开受众并写入 `feed_batches`，投递成功后确认。每批默认 256 人，不为每个粉丝创建一行任务。Redis 只存候选 ID；HTTP 与第三方实时流仍根据当前 SQL 重新检查内容、成员关系和权限。

设计与验收目标见 [实施计划](tasks/feed_scaling_plan.md)，实测结果见 [本次验收记录](tasks/feed-validation-20261011.md)。

## 运行方式

未配置 `FEED_MODE` 时默认为 `shadow`：持久任务照常保存和影子投影，但不由新 Worker 发布客户端提示，读取使用当前 SQL，旧投递路径仍保留。不会因升级自动启用新分发。

启用步骤：先完成单实例迁移，以默认影子模式核对 SQL oracle、任务收敛和资源预算；验收后，在参与同一部署的实例上明确设置 `FEED_MODE=durable`，统一停止旧模式实例后启动新模式，避免两种提示路径混跑。开发预览也须显式设置该值。环境变量的非空值优先于配置文件；空白环境值回退到配置文件，双方都为空则使用 `shadow`。回退与未通过的性能门槛见验收记录。

默认 `COMMUNITY_ROLE=all`：HTTP、Feed Worker 及原有后台工作在同一进程运行。扩容时可以拆成两个相同二进制的进程，使用同一数据库和 Feed Redis：

```bash
COMMUNITY_ROLE=api FEED_MODE=durable ./community
COMMUNITY_ROLE=feed-worker FEED_MODE=durable ./community
```

所有角色启动时都会检查并执行数据库 schema 迁移；升级应先由单个新版本实例完成迁移，再扩容其他 API/Worker 副本，避免并发执行尚未完成的迁移。`feed-worker` 完成 schema 迁移后只执行 Feed 工作，不监听业务 HTTP，也不启动 ActivityPub、Matrix、搜索或导入/导出恢复等应用后台任务。`api` 仍负责原有应用功能与其后台任务。副本使用相同 `APP_BASE_URL` 和配置；角色不是单独的账号身份。数据库迁移需要先成功完成，不允许忽略缺失任务表继续接受写入。

每条 Feed 事件的 metadata JSON 上限为 256 KiB，按实际 UTF-8／转义后的序列化字节数计算。超限返回固定错误并使调用方业务事务回滚，不截断收件人。限制位于应用类型的 JSON 序列化边界，覆盖正常 Create／Save／类型化 Updates；手工 SQL 或绕过该类型的原始 JSON 写入不属于受支持的应用入口。

| 配置 | 默认值 | 作用 |
| --- | --- | --- |
| `COMMUNITY_ROLE` | `all` | `all`、`api` 或 `feed-worker` |
| `FEED_MODE` | `shadow` | `durable`、`shadow` 或 `legacy` |
| `FEED_SCAN_CONCURRENCY` | `2` | 每进程扫描槽，范围 1–16 |
| `FEED_DELIVERY_CONCURRENCY` | `4` | 每进程投递槽，范围 1–32 |
| `FEED_BATCH_SIZE` | `256` | 受众批大小，范围 128–1024 |
| `FEED_METRICS_LISTEN` | 空 | 独立 Worker 可选监控地址，如 `127.0.0.1:9091`，仅提供 `/metrics` |
| `FEED_MAX_PENDING_EVENTS` | `100000` | 待处理、处理中及失败事件的软容量预算 |
| `FEED_MAX_PENDING_BATCHES` | `1000000` | 同类批次的软容量预算 |
| `REDIS_FEED_CLUSTER_ADDRS` | 空 | 可选 Cluster 节点地址，逗号分隔 |

以上 Feed 设置支持配置文件及环境变量，环境变量优先。数据库连接池使用既有 `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS`；给 API 留出连接，并按实例数计算数据库总连接预算。先测量队列年龄与依赖负载，再增加 Worker 副本或槽数。

写 API 在队列达到预算或预算查询不可用时返回 `503` 与 `Retry-After: 5`。每个 API 进程最多每秒检查一次，这是背压而非全局硬配额；并发副本可能在观察间隔内超出预算。事务内任务保存失败仍必须回滚业务写入。收件联邦队列保留其原有独立重试语义。

## 租约、重建与故障

扫描和投递分别领取 SQL 租约。任务每次领取增加 generation 并获得随机 token；工作请求有 20 秒时限，租约为 30 秒，进程退出后的任务可被其他 Worker 接管。每轮扫描最多生成四批，再让出执行机会。Redis 成功但 SQL 确认失败会重放，因此投影要求幂等，不承诺跨数据库与 Redis 的 exactly-once。

v3 缓存使用同一时间线 hash tag：`feed:v3:{home:123}:items` 及其 `:meta`、`:versions`、`:rebuild` 等派生键。Lua 只访问一个槽；跨受众操作通过 pipeline 发出。元数据记录 epoch、generation 和完整性。冷批次可以登记一个不完整的新 epoch，随后重新读取当前 SQL；只有领取快照租约、读取 SQL 并通过 CAS 发布的重建才会标记完整。

重建查询期间发生投递或失效时，旧快照不能发布；Delete 的版本记录阻止迟到 Create 复活内容。缺失正确性元数据按不完整处理，不把 ZSet 存在等同于有效缓存。每条时间线最多保存 512 个热候选，深分页继续使用 SQL。

Redis 使用 **`noeviction`**，监测内存并预留容量。Redis 不可用时 Worker 保留任务退避；认证的可选缓存共用 200 毫秒预算，Feed 活跃记录、候选读取及冷重建共用 500 毫秒预算。缓存超时不取消原 HTTP 请求，随后有界回源 SQL（每进程最多 16 个并发，5 秒时限），过载返回明确 `503`，不伪造空列表。重建最多 8 个并发。自动重放窗口为 7 天；过期任务需要按当前 SQL 重新协调。

默认仍使用单机 `REDIS_0_*`。可选 Cluster 仅替换 Feed 的候选、活动标记和相关流客户端；其他组件仍使用原单机 Redis。Cluster 使用 DB0，密码沿用 `REDIS_0_PASSWORD`。不要把其他组件未经验证地一起迁到 Cluster，也不要使用 `FLUSHALL` 切换版本。

## 网页和兼容流

网页通过认证的 `GET /api/v1/streaming/feed` 接收 `feed.changed`。提示仅包含 epoch/revision，客户端合并 250ms 内的变化，再通过 HTTP 重校验当前窗口。初次连接、重连、页面恢复和账号切换均有重校验；新帖由用户点击提示后展开。旧 Mastodon SSE/WebSocket 协议保留，但输出前重新加载 SQL，检查当前流成员关系、屏蔽和可见性。

网页正文缓存最多 8 页 / 160 条；虚拟列表正常最多挂载 60 张卡片，并可额外保留最多 8 张正在交互的卡片。高度与导航元数据也有上限。离开缓存范围的历史内容重新通过 HTTP 加载，不把浏览器中的旧卡片视为仍获授权。

保留现有共享列表的内容规则：列表时间线只选取成员的公开与未列出帖子。即使读者已经关注作者、能单独读取其仅关注者可见帖子，该帖子也不进入列表；私信同样不进入。列表自身的公开／私有设置控制列表访问权限，不改变帖子候选范围。

## 观测与后台

管理后台的作业队列可查看 `feed-event` / `feed-batch`，按权限安全重试或取消。取消某个未完成批次会取消整个未完成任务组，避免父任务被错误标为成功。活跃租约下的管理操作返回冲突。超过重放窗口的重试创建当前状态的失效协调任务，不原样重放旧 Create。任务详情不公开受众、私信正文或租约 token。

API 的 `/metrics` 保留；独立 Worker 通过 `FEED_METRICS_LISTEN` 绑定内部监控地址。每个实例分别采集，停止时监控监听器一同关闭。Prometheus 指标使用固定结果标签，不包含账号和帖子 ID：

- `community_feed_tasks_total`、`community_feed_delivery_seconds`、`community_feed_oldest_pending_seconds`：执行结果、提交至批次确认耗时与队列最老年龄。批次延迟不等同于作者全部受众完成延迟，后者通过写入压测单独测量。
- `community_feed_queue_depth`：按扫描/投递和固定任务状态统计队列深度。
- `community_feed_cache_reads_total`、`community_feed_rebuild_total`、`community_feed_sql_fallback_total`：缓存完整性、重建和回源压力。
- `community_feed_filtered_candidates_total`：被当前成员、静音、可见性或响应过滤规则剔除的候选。
- `community_feed_redis_pipeline_seconds` / `community_feed_redis_pipeline_errors_total`：批量 Redis 调用耗时与错误。

热点脚本先使用 `EVALSHA`，仅对明确的 `NOSCRIPT` 项在原超时预算内回退完整脚本。冷启动、清除脚本缓存或副本晋升时，底层 pipeline 错误计数可能包含已成功恢复的 `NOSCRIPT`；它不等于任务失败或批次重试次数，应结合任务状态判断。

同时观测现有 HTTP、Go 运行时、数据库连接等待及 Redis 内存/操作数。失败任务与积压不能只看总数量，应结合最老年龄和重试原因处理。

## 影子运行与回退

`FEED_MODE=shadow` 保留旧分发，Worker 只写 v3 候选、不发客户端提示，HTTP 使用 SQL 读取，用于比较授权结果。`legacy` 不启动新 Worker，也使用 SQL 读取；保留事务事件和任务历史，队列 soft-budget 背压仍然生效。因此它仅适合短时诊断回退，不是解除队列容量故障的方法；积压或 failed 任务需要恢复 durable 处理或按当前 SQL 协调，不能清空队列。不要把 legacy 的内存分发视为持久确认。

切换时统一调整参与的 API/Worker 模式，停止旧模式进程，再启动新模式；不混合运行 durable 和 legacy 的客户端提示路径。回退不删除新表或清空队列。重新启用 durable 后，未超过窗口的任务继续处理，过期任务标记失败，需要通过管理员重试触发当前 SQL 状态协调，而非自动重放旧事件。大作者 push/pull 混合策略仍须达到计划中的实测收益门槛后才启用。
