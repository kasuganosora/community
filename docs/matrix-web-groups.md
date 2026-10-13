# 网页 Matrix 群组：接口与运维

本文描述当前网页群组实现。设计依据见[实施计划](tasks/matrix-web-groups-plan.md)，已执行的检查、性能环境及未覆盖项见[验收报告](tasks/matrix-web-groups-validation.md)。接口为 Community 网页提供，不代替通用 Matrix 客户端 API。

## 功能边界

网页登录使用本站 Community 身份，对应同一个 Matrix 用户，不额外登录外部账号或向浏览器签发 Matrix token。群聊使用原生 Matrix 房间、成员和事件；不写 ActivityPub Status，不接入本站 AP 私信桥接，不邀请本站 AP ghost 或桥接控制账号。外部 Matrix 服务器的桥接成员按普通 Matrix 成员处理。

网页创建的群不启用端到端加密，创建时必须明确确认。公开群允许公开加入并展示在本站目录；仅邀请群默认不公开。两者的新群历史均默认仅加入后可见，禁止访客。目录是否展示、加入规则、历史可见性是独立设置。

支持文本、链接、成员提及、回复、图片、普通文件、Unicode／自定义表情及消息回应。网页的“编辑”执行删除后重发；有效的外部客户端编辑聚合为最新内容。本期不提供音视频通话、会议、录音、视频播放或转码；外部音视频消息作为附件下载或转到客户端处理。

加入外部群时可能要加入后才知道加密状态。`encryption` 为 `unknown`、`unencrypted` 或 `encrypted`；未知及加密状态均不允许网页发送／上传。已加入加密群时准确显示已加入，并提供客户端入口，不自动退群。原生客户端以后启用加密时，本地写入与加密状态共享房间事务锁，包括重试在内的明文发送都重新检查；远端状态尚未传到本站前仍存在联邦传播窗口。

普通群退出后移出活跃列表，新消息不会自动把用户拉回；明确的新邀请单独展示。退出不删除房间或所有历史。Spaces 与已被升级替代的房间不作为可继续发送的普通群处理。

## API 契约

所有短路径的前缀均为 `/api/v1/chat`，使用现有 Community 登录鉴权。读取需要 `read:statuses`，修改需要 `write:statuses`；`POST /group-sync` 和 `POST /groups/resolve` 属于读取。服务器校验本地有效账号及每次操作的成员身份、房间状态和权限，不能通过提交发送者 ID 或前端能力标记越权。

群 ID、消息 ID、上传 ID、目录预览引用和游标均视为不透明字符串：保留原值，作为路径段时正确编码，不依赖其内部格式或推导 Matrix 地址。成员 ID 用于选择明确成员；客户端入口采用服务端提供的 `client_url`，不携带 token。

| 操作 | 方法与路径 | 主要参数／结果 |
| --- | --- | --- |
| 群列表 | `GET /groups` | `cursor`、`limit`；分页加载本人已加入、邀请及申请状态 |
| 创建 | `POST /groups` | `transaction_id`、`name`、`topic`、`visibility`、`invite`、`plaintext_consent`；返回群详情 |
| 详情／修改 | `GET /groups/:id`、`PATCH /groups/:id` | 修改只接受 `name`、`topic`、`avatar_upload_id` |
| 搜索 | `GET /groups/search` | `q`、`scope=joined\|local\|remote`、`server`、`cursor`、`limit` |
| 只读解析 | `POST /groups/resolve` | `target`、`via` 或目录 `preview_ref`；返回预览／客户端入口，不自动加入 |
| 加入／申请 | `POST /groups/join` | `target`／`preview_ref`、`via`、`action=join\|knock`；返回状态及可能的 `operation_id` |
| 查询加入进度 | `GET /groups/join/:operationID` | 查询远端操作结果；请求超时不等于远端已拒绝 |
| 退出／拒绝／取消申请 | `POST /groups/:id/leave` | 按当前成员状态执行 |
| 成员 | `GET /groups/:id/members` | `cursor`、`limit`；分页加载 |
| 成员操作 | `POST /groups/:id/members` | `user_id`、`action=invite\|approve\|kick\|ban\|unban`、可选 `reason` |
| 成员权限 | `POST /groups/:id/permissions` | `user_id`、`power_level`；不能超过操作者自身权限 |
| 历史 | `GET /groups/:id/messages` | `before`、`limit`；返回 `{messages,next_cursor}` |
| 发送 | `POST /groups/:id/messages` | 见下方示例；返回 `{status,messages,event_ids}` |
| 删除 | `DELETE /groups/:id/messages/:messageID` | 按本人消息／房间删除权限执行 |
| 删除并重发 | `POST /groups/:id/messages/:messageID/replace` | 与发送相同的请求体；两步可能部分成功 |
| 添加／撤销回应 | `POST`／`DELETE /groups/:id/messages/:messageID/reactions` | 添加体 `{key,transaction_id}`；撤销查询参数 `key` |
| 上传 | `POST /groups/:id/uploads` | multipart 单个 `file`；返回 `{id,name,type,size}`，随后将 `id` 用于发送 |
| 文件 | `GET /groups/:id/messages/:messageID/media/:mediaID` | 从消息 DTO 获取 URL；再次检查消息可见性 |
| 群头像／成员头像 | `GET /groups/:id/avatar`、`GET /groups/:id/members/:memberID/avatar` | 使用 DTO 中提供的受控 URL |
| 已读 | `POST /groups/:id/read` | `{event_id}` |
| 输入状态 | `POST /groups/:id/typing` | `{typing:true}`／`{typing:false}`；有效期 25 秒 |
| 静音 | `POST /groups/:id/preferences` | `{muted:true}`／`{muted:false}` |
| 增量同步 | `POST /group-sync` | `{since,timeout}`，详见下一节 |

分页 `limit` 最大 100。各接口的游标不能混用。搜索分“我的群组”、本站公开目录、指定远端公开目录；不是全网搜索。选择远端目录会将查询发到该服务器。精确别名、`matrix.to` 链接和 `matrix:` URI 可独立解析，不能把 `user@domain` 当作 Matrix 成员 ID；邀请支持本站用户名及完整 Matrix ID。

群详情主要字段为 `id`、`name`、`topic`、`avatar_url`、`membership`、`encryption`、`join_rule`、`listed`、`member_count`、`unread_count`、`muted`、`typing_users`、`current_user_id`、`client_url`、`capabilities`、`limits`，可能附有 `replacement_id` 和 `room_type`。`capabilities` 决定界面可展示的操作，服务端仍在提交时重新授权。

创建示例：

```json
{
  "transaction_id": "create-team-20261013-01",
  "name": "项目讨论",
  "topic": "日常交流",
  "visibility": "private",
  "invite": ["alice", "@bob:example.org"],
  "plaintext_consent": true
}
```

创建事务 ID 长度为 8–128 字节，名称最多 255 个字符，介绍最多 512 个字符，一次最多邀请 50 人。创建成功与所有邀请成功是不同结果，客户端需要展示逐项结果并允许失败项重试。

发送示例（`reply_to`、附件及提及可省略）：

```json
{
  "transaction_id": "send-20261013-01",
  "body": "收到 :wave:",
  "reply_to": "<message-id>",
  "upload_ids": ["<upload-id>"],
  "mentions": ["@bob:example.org"],
  "custom_emojis": ["wave"]
}
```

消息正文上限 32,000 字节，最多 8 个附件、50 个提及、50 个自定义表情引用。自定义表情从可用的本站资源查找并转换成 Matrix 媒体，不能传任意图片 URL。回应 `key` 支持 Unicode 或 `:shortcode:`；不同客户端对自定义图片回应扩展的显示可能不同。

消息 DTO 包含 `id`、`sender`、`body`、可选 `formatted_body`、`kind`、`created_at`、`own`、`redacted`、`edited`、`reply_to`、`reply_unavailable`、`attachments`、`reactions`、`custom_emojis`。`created_at` 为毫秒时间戳，不能用它代替服务端返回顺序。旧消息发生编辑／删除／回应变化时，同一个 `id` 的新 DTO 替换原内容。格式化正文经过净化；未知类型用占位显示，不直接输出事件 JSON。

发送事务按用户、房间及操作隔离；同事务同内容重试复用成功事件，同事务不同内容返回 409。多附件可能产生多条事件，客户端保留原始事务 ID 和请求体重试，由服务器跳过已成功的子事务。`partial_failure` 响应包含已成功的 `messages`／`event_ids`、失败的 `failed_upload_ids`，删除并重发还会给出 `deleted`；不能把部分失败显示成全部失败，也不能换新事务重新提交整批。群设置多字段更新也可能返回 `partial_update`，应刷新并只重试剩余字段。发送成功表示本站已经接受，不表示所有远端服务器已确认。

## 同步、历史与跨端状态

每个网页会话使用一条 `POST /group-sync` 长轮询，`timeout` 的单位是毫秒，范围 0–25,000。响应为：

```json
{
  "next_cursor": "<opaque-cursor>",
  "groups": [],
  "changed_group_ids": [],
  "left_group_ids": [],
  "messages_by_group": {},
  "typing_by_group": {},
  "has_more": false,
  "reset": false
}
```

首次不传 `since`，返回当前群快照、水位及 `reset:true`，不把全部消息历史塞进快照。先建立初始同步水位，再加载当前群历史，持续消费增量。后续把 `next_cursor` 原样作为 `since`。`messages_by_group` 包括新增消息以及旧消息的更新，按消息 ID 合并；`left_group_ids` 从活跃列表移除。每轮每群最多补 100 个事件，总计最多 500 个，`has_more:true` 时立即继续消费，不等待下一次长轮询。

游标距最近生成超过 7 天会触发 `reset:true`，客户端重建列表并重新加载当前群历史。用户不匹配、格式错误等无效游标返回错误，应重新初始化。当前最多覆盖 2,000 个已加入／邀请／申请中的群，且编码游标不能超过 256 KiB；房间 ID 较长时可能先达到字节上限。超限返回 HTTP 422、`code: sync_limit_exceeded`、`retryable:false`，不静默忽略多余房间。网页停止自动同步重试并提示使用 Matrix 客户端；列表和选中群历史仍可加载，允许手动刷新。POST 请求体上限 300 KiB；保留的 GET 形式只适合短游标，网页固定使用 POST，避免代理 URL／请求头上限。

每个房间在写事务内锁定自身行、递增持久化流序号，与事件一同提交或回滚，没有全站单行序号锁。不同房间不因序号分配而互相阻塞。同进程通知用于立即唤醒；多实例或丢失通知时，每 2 秒复查共享数据库，游标提供可靠补偿，不依赖通知必达。

历史接口每页按服务端顺序由旧到新返回，加载更早页时前插；使用 `next_cursor` 继续，即使某页因历史权限过滤为空。新增事件按房间流序分页，旧事件的序号保持 0，以本地入库时间和事件 ID 作稳定后备排序，**无需全表历史回填**。消息、回复上下文、编辑及附件分别遵循历史可见性规则。退出群后可读的历史由协议权限决定，不是一律抹除全部历史，也不能读取离开后的受限新内容。

已读接口更新 Matrix `m.read` 和 `m.fully_read`；未读统计同时参考原生客户端的 `m.read.private`。未读排除本人消息、编辑事件和已删除消息，并要求有历史状态证明当时用户已加入。早期缺少状态快照的导入历史可能保守少计，不能把此计数当成所有 Matrix 客户端通知规则的完全复刻。静音设置复用 Matrix 房间推送规则；取消静音删除此房间规则，不覆盖其他客户端建立的其他规则。输入状态是短期提示，可过期或合并，不保证逐次历史送达。

## 媒体与反向代理

| 限制 | 当前值／配置 |
| --- | --- |
| 单文件默认上限 | 50 MiB（52,428,800 字节） |
| 降低单文件上限 | `MATRIX_WEB_GROUP_UPLOAD_MAX_BYTES`，有效值 1–52,428,800；未设置、无效或超出范围均使用默认值 |
| 每次发送附件数 | 8，每个文件独立上传 |
| 图片像素上限 | 16,000,000 |
| 上传并发 | 每个进程最多 4 个，忙时返回 429 并给出 `Retry-After` |
| 未引用上传租约 | 24 小时；清理任务每 5 分钟检查，不删除仍被消息引用的媒体 |
| 群媒体应用处理预算 | 300 秒；连接截止时间留有 5 秒余量 |
| 群同步应用处理预算 | 60 秒；正常长轮询最大等待 25 秒 |

反向代理与负载均衡需同步配置：上传正文至少允许 **51 MiB**（包含 multipart 开销）；上传正文接收、群媒体读写超时至少 **305 秒**；群同步代理读写超时至少 **65 秒**。这些配置应仅针对相应路由，不必放宽全站普通 API 的限制。Nginx 对应配置项为 `client_max_body_size`、`client_body_timeout`、`proxy_read_timeout`、`proxy_send_timeout`；上传可配置 `proxy_request_buffering off`，避免代理先完整落临时文件。其他代理按等效配置调整。

媒体范围包括 `/groups/:id/uploads`、群头像、成员头像、消息媒体及 `/groups/directory-avatar`；同步是精确路径 `/api/v1/chat/group-sync`。保留现有鉴权转发及 HTTPS 配置，不对这些受保护响应做公共 CDN 缓存。媒体 URL 需要用户鉴权，网页通过带鉴权的请求获取 Blob；不能把它当作匿名公开图片链接。

上传会使用受限临时文件，持久媒体复用现有 Matrix 存储后端；需预留临时磁盘、对象存储空间及网络预算。扩容多个实例时共享同一数据库和媒体存储，不能使用互不可见的本地上传目录。类型检测、图片像素限制、安全文件名、`nosniff` 和下载响应限制可执行内容；上传完成不等于发送成功，发送时再次检查用户、群、租约和权限。

网页文件入口按所属事件及历史权限授权。消息删除后，相应网页附件入口拒绝继续读取；本站无法收回已经下载的文件、截图或远端服务器已保存的副本。

## 原生联邦投递与多实例

无需增加独立消息服务或搜索集群。沿用现有 Matrix 服务、共享数据库及共享媒体存储。新增读路径不要求 sticky session，可靠同步使用数据库水位及 2 秒复查。MySQL 部署使用支持事务及行锁的存储引擎。

原生房间事件和联邦投递记录在同一事务中持久化。实现复用名为 `BridgeFederationDelivery` 的既有队列表，并用 `Native=true` 区分原生投递；此处投递的是 **Matrix PDU**，不生成 ActivityPub 任务，worker 启动也不依赖 AP 桥接开关。

worker 每 2 秒取到期任务，一轮最多选择 16 个远端服务器，总计不超过 100 项，各服务器公平分配且最多 25 项，最多并行处理 4 个服务器，同一服务器内顺序发送。服务器选择按名称游标轮转，避免前 16 个离线服务器的旧任务反复挤占后续服务器。某服务器首项失败即释放 worker 处理其他服务器；一轮总预算 20 秒。共享数据库租约避免多实例同时领取同一任务。单次远端请求有 8 秒预算；HTTP 成功后还需确认对应 PDU 结果，不能只依据 HTTP 200 标记成功。重试更换联邦事务 ID，避免复用远端缓存的失败结果，但保持 PDU 和事件 ID 不变，由事件 ID 防止重复接收。退出／踢人事件也会投递给相关的最后一个远端成员所在服务器。

原生投递失败按指数退避重试，间隔最多 1 小时；创建时间超过 7 天仍失败的任务转为 `failed`，不无限重试。监控 `pending`／`processing` 积压、最早未送达时间、`failed` 数量、尝试次数与下次重试时间；恢复故障后按现有运维流程核查并重排失败任务。队列表保留投递状态，不等于自动删除已发送房间历史。多个实例扩大的是整体处理能力，不承诺所有远端实时在线或所有客户端扩展表现一致。

## 上线与回退

1. 按现有流程备份数据库、媒体存储及服务器配置／签名材料，确认可恢复且有足够迁移空间。
2. 在生产数据量的副本评估启动迁移。新增房间流序字段、事件流序／关系／幂等字段与索引，以及上传租约、邀请任务和原生队列标记等均由模型迁移管理。特别是大 `matrix_events` 表新增 `idx_matrix_events_web_history(room_id,stream_position,created_at,event_id)` 和关系索引，可能建表索引、持锁及消耗临时空间；不承诺无锁即时完成。
3. 控制迁移执行者，避免多个实例同时做 DDL。切换期间暂停旧版本事件写入，完成迁移并更新所有写入实例后再恢复；旧版本不分配新流序，不能把长期混用新旧写入器当作安全滚动升级。
4. 检查媒体代理限制、数据库连接池、共享存储、Matrix 公网发现／联邦连通性；启动后验证登录、创建、邀请、收发、同步及队列消费，再开放网页入口。
5. 出现问题优先关闭网页新群入口／相关流量，同时保留兼容的新后端处理现有房间和投递任务，按诊断向前修复。必须回退二进制时，先暂停写入并核对旧版本对新增队列／流序的兼容，保留所有新增字段、表、房间、消息和媒体。**不使用删表、删索引、清空事件或媒体作为回退步骤**；回退后再次升级需要重置网页同步状态并核对期间写入。

旧数据序号为 0 是有意兼容路径，不要求运维对全部历史生成序号。不要为清理已退出群或恢复同步去直接删 Matrix 事件。

## 重跑定向检查

在仓库根目录，按仓库开发环境说明激活 Go 工具链后运行：

```sh
go test ./matrix/homeserver -run '^TestWebGroup' -count=1
go test ./middleware -run 'WebGroup' -count=1
```

默认 SQLite 测试不能证明 MySQL 行锁行为。可在**独立可丢弃的 MySQL 测试服务器**运行真实事务检查：从本地密钥管理或安全终端读入 `COMMUNITY_GROUPS_MYSQL_DSN`，不要写入命令历史、仓库、报告或 CI 明文日志。交互终端示例：

```sh
read -r -s -p 'Disposable MySQL test DSN: ' COMMUNITY_GROUPS_MYSQL_DSN
export COMMUNITY_GROUPS_MYSQL_DSN
go test ./matrix/homeserver -run '^TestWebGroupMySQLStreamTransactions$' -count=1 -v
unset COMMUNITY_GROUPS_MYSQL_DSN
```

此条件测试需要创建／删除数据库权限，在指定服务器建立随机 `community_groups_stream_qa_*` 测试库并清理；不复用 DSN 指定数据库中的业务表。未设置环境变量时明确跳过。它验证同房间提交顺序、回滚序号、不同房间互不阻塞、10 个同事务并发仅产生 1 条消息，以及加密状态提交后阻止排队的明文发送。测试账号仅授予实验服务器权限，不能用生产数据库执行压测。

真实外部服务器互联、浏览器行为和性能结果的范围以[验收报告](tasks/matrix-web-groups-validation.md)为准；通过本地单元测试不代表所有 Matrix 客户端或远端房间均已兼容。
