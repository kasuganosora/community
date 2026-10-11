# Matrix 房间版本

Community 支持稳定房间版本 1–12。客户端可以在 `POST /_matrix/client/v3/createRoom` 中指定 `"room_version": "12"`；联邦 `make_join` 协商也包含版本 12。`/capabilities` 公布支持的版本。

没有指定版本时，创建房间仍沿用现有默认版本 10。已有房间不自动升级；版本决定事件身份与授权规则，不能直接修改数据库中的版本号。

## 版本 12

- 房间 ID 从签名后的创建事件派生，不含服务器域名；创建事件的原始联邦 JSON 不带 `room_id`。
- 创建者及创建事件列出的 `additional_creators` 拥有隐式最高权限，不能在 `m.room.power_levels.users` 中为其指定权限值。旧版本不获得这些特权。
- 撤回目标放在 `content.redacts` 中，收发双方均执行对应版本的授权规则。
- 事件验签、认证链与状态冲突解析使用版本感知的 gomatrixserverlib 实现；v12 使用 State Resolution v2.1。
- 加入没有域名后缀的房间时，使用房间别名或提供 `via` 服务器提示，不能从房间 ID 推断目标服务器。

回归覆盖创建事件身份、远端状态导入、事件重放、创建者和普通成员权限、撤回，以及分叉合并后封禁状态的有效性。公开实例仍可能因邀请规则、封禁或连接失败拒绝加入；支持房间版本不绕过这些限制。

## 协议库补丁

当前锁定的 gomatrixserverlib 上游提交在 v12 状态冲突处理中缺少创建者初始化，且授权回退选错事件。`go.mod` 临时引用仓库内 `third_party/gomatrixserverlib`；来源、最小补丁和恢复上游依赖的条件见其 `COMMUNITY_PATCHES.md`。保留原许可证及测试。

## 远端加入与签名密钥

`MATRIX_JOIN_TOTAL_TIMEOUT`（默认 25 秒）统一覆盖别名解析、服务器尝试、密钥获取、验证和持久化。最多尝试 `MATRIX_JOIN_MAX_ATTEMPTS` 个候选服务器（默认 5 个），全站同时最多处理 `MATRIX_JOIN_MAX_CONCURRENT` 个加入（默认 3 个）。

先验证加入授权链和响应携带的当前安全策略，包括权限、服务器 ACL、历史可见性与加密状态；这些事件畸形、验证失败或缺少必要授权祖先时不能降级为默认权限。其他历史事件的密钥预取最多使用 2 秒且不超过剩余预算的四分之一；随后只用可信缓存验证，无法验证的事件及其依赖后代不会写入数据库。

`send_join` 响应默认限制为 64 MiB，可用 `MATRIX_FEDERATION_SEND_JOIN_MAX_MB` 调整（1–200 MiB）；状态与认证链合计最多 250,000 个事件。字节和事件数量上限并不等于进程 RSS 上限，解析后的事件、验证索引和并发加入仍会占用额外内存。

状态与认证链事件数上限可用 `MATRIX_MAX_JOIN_STATE_EVENTS` 调整（默认 250000）；超过时在读取响应过程中立即拒绝加入，返回 `M_TOO_LARGE`（HTTP 413）和可读说明，不会写入任何数据。

### 分阶段持久化（partial state）

加入响应验证通过后分两步写入：第一步在同一个事务、同一个 `MATRIX_JOIN_TOTAL_TIMEOUT` 预算内写入房间记录、本地用户的 join 事件、create/power_levels/join_rules/名称/主题等关键状态、本地用户自己的成员关系及其认证祖先，然后立即响应客户端；其余事件（主要是其他成员）由后台按批写入。剩余事件不超过 `MATRIX_JOIN_SYNC_STATE_EVENTS`（默认 2000）时直接在第一步内写完，不产生 partial 状态。

后台写入全部是“不存在才插入”，可安全重试和重入，不会覆盖更新的本地状态。设置：`MATRIX_JOIN_BG_WORKERS`（并发房间数，默认 2）、`MATRIX_JOIN_BG_MAX_HELD`（同时在内存中保留剩余事件的房间数，默认 4，超出则之后从远端重新拉取）、`MATRIX_JOIN_BG_BATCH`（每批事件数，默认 500，另有约 4 MiB 字节上限）、`MATRIX_JOIN_BG_RETRIES`（整体重试次数，默认 5）、`MATRIX_JOIN_BG_RETRY_DELAY`（首次重试间隔，默认 5s，指数增长，上限 5 分钟）、`MATRIX_JOIN_BG_FETCH_TIMEOUT`（恢复时拉取并验证 /state 的超时，默认 10m）、`MATRIX_JOIN_BG_MAX_PENDING_PDUS`（partial 房间内存中暂存的入站 PDU 数，默认 1000）。

partial 标记复用 `matrix_rooms_hs.current_state_id`（形如 `partial|<join 事件 ID>|<服务器>`），无需数据库迁移，崩溃后仍保留。标记存在期间：federation 的 state、state_ids、event、make_join、send_join、make_leave、send_leave 返回 404 `M_NOT_FOUND`；收到的 PDU 暂存并在完成后重放；本地发送的事件同时发给提供加入的服务器；客户端读取基于已有数据（成员列表可能暂时不全）。启动时以及用户再次加入该房间时，会通过 federation `/state`（以 join 事件为基准）从记录的服务器重新拉取并继续写入。

离线服务器的公钥只通过管理员信任的密钥服务器补取。`MATRIX_TRUSTED_KEY_SERVERS` 默认是 `matrix.org`；可用逗号分隔的列表覆盖，显式设为空则关闭备用查询。加入目标和客户端的 `via` 参数不会自动获得公钥担保权限。

升级后，缺少当前验证策略标记的旧密钥缓存不再被信任，会重新获取验证。历史退役密钥仍保存在数据库，仅接受严格早于 `expired_ts` 的事件（包括房间版本 1–4）。锁等待、HTTP 与数据库操作均使用调用方的取消期限。
