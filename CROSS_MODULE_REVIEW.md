# 跨模块集成分析报告 (Cross-Module Integration Review)

审查范围：/data/community 整个后端项目，8 个维度。
本报告记录发现的所有跨模块集成问题及修复方案。

---

## 维度 7（事件/通知链路）— 发现 3 个问题

### ✅ 已修复 — ISSUE-1（严重）：Status 删除事件名不匹配，导致删除 status 后所有时间线和通知永不清理

**数据流断点：**
- `models/status.go` 的 `BeforeDelete`/`AfterDelete` 钩子触发的事件名为 `before_delete_status` / `after_delete_status`。
- 但所有下游消费者（6 个 feed + notifications/status.go）监听的事件名是 `before_delete` / `after_delete`。

**影响（7 处消费者全部失效）：**
| 消费者 | 文件 | 监听的 case |
|--------|------|------------|
| Home 时间线 | feed/home/home_timeline.go:47 | `after_delete` |
| List 时间线 | feed/list/list_timeline.go:38 | `after_delete` |
| Tag 时间线 | feed/tag/tag_timeline.go:55 | `after_delete` |
| Mention 时间线 | feed/mention/mention_timeline.go:53 | `after_delete` |
| Public 时间线 | feed/public/public_timeline.go:37 | `after_delete` |
| Direct 时间线 | feed/direct/direct_timeline.go:53 | `after_delete` |
| 通知清理 | notifications/status.go:30 | `after_delete` |

**后果：** 用户删除一条嘟文后，该嘟文永远不会从任何人的 Redis 时间线中移除，也不会清理 mention/reblog 通知 → 时间线出现幽灵内容、通知残留。

**修复：** 将 `models/status.go` 中 `before_delete_status`→`before_delete`、`after_delete_status`→`after_delete`，与 `Notification`/`Report`/`Follow` 等其它 model 采用的标准事件命名一致。

### ✅ 已修复 — ISSUE-2（严重）：缺少 `before_update` 事件，导致编辑 status 后时间线出现重复/陈旧内容

**数据流断点：**
- 所有 6 个 feed 在编辑 status 时依赖 `before_update` 事件先执行 `Unpush`（移除旧条目），再用 `after_update` 执行 `Push`（添加新条目）。
- 但 `models/status.go` 只有 `BeforeSave`（触发 `before_save`）和 `AfterUpdate`（触发 `after_update`），**从不触发 `before_update`**。
- `BeforeSave` 在 create+update 都会触发，不能复用为 `before_update`（否则新建时会误触发清理逻辑）。

**后果：** 编辑嘟文后，旧内容不从时间线移除，新内容又被添加 → 时间线出现重复嘟文；被取消 @mention 的用户的时间线也残留旧条目。

**修复：** 在 `models/status.go` 新增 `BeforeUpdate` 钩子，发布 `before_update` 事件（GORM 中 `BeforeUpdate` 仅在更新已存在记录时触发，语义正确）。

### ✅ 已修复 — ISSUE-5（Serializer↔Model）：`ToNotifications` 批量序列化遗漏 `ListId` 收集，列表类通知丢失 list 字段

**断点：** `serializer/notification.go` 的 `ToNotifications`（批量版，被通知时间线使用）声明了 `listsIds` 但在遍历通知时**从未 append `n.ListId`**，导致 `len(listsIds)` 恒为 0，列表预加载代码块永不执行，`NotificationTypeAddList`/`NotificationTypeFollow`(带 list) 的 `list` 字段始终为空。

**对比：** 单条版 `ToNotification`（line 76-83）正确处理了 List，只有批量版有此 bug。

**修复：** 在收集循环中补充 `if n.ListId != 0 { listsIds = append(listsIds, n.ListId) }`。

---

## 维度 8（数据流：计数器维护）— 发现 2 个问题

### ✅ 已修复 — ISSUE-3（严重）：直接关注（非申请态）从不维护 followers/following 计数

**数据流断点：**
- `serializer/account.go` 输出 `FollowersCount`/`FollowingCount`（来自 `AccountExtra`）。
- `models/follow.go` 的 `Follow.AfterCreate`/`AfterDelete` 只触发事件，**从不更新 `AccountExtra` 计数器**。
- 唯一维护计数器的地方是 `controllers/api/follow_request/follow_request.go` 的 `Authorize`（申请→批准路径）。
- 因此：**直接关注（目标账户未开启关注保护时）从不递增计数器，取消关注也从不递减**。

**后果：** serializer 输出的 `followers_count`/`following_count` 对绝大多数用户恒为 0（只有"申请制"账户被批准后才有非零值），且只增不减。

**修复：** 在 `Follow.AfterCreate`（仅 `FollowStatusFollowed`）递增、`Follow.AfterDelete`（仅原状态为 `FollowStatusFollowed`）递减，新增 `updateFollowCounters` 辅助函数。注意：
- `AfterCreate` 仅在 `Status==Followed` 时递增（Requested 态不计数，避免与 `Authorize` 的手动递增重复计数）。
- `AfterUpdate` **不**递增（Authorize 走的是 update 路径，其 controller 已手动递增，Hook 不再重复）。
- 使用 `gorm.Expr("col + ?", delta)` 纯算术（列为有符号 `int(11)`，不会下溢回绕），避免使用 `if()` 函数（测试用 TiDB 不支持 UPDATE...SET col=func()）。
- 计数器维护带 `tx.Migrator().HasTable(&AccountExtra{})` 守卫：当 `accounts_extra` 表尚未迁移（引导/最小化测试 schema）时跳过而非报错——计数器是派生的辅助数据，其失败不应阻断核心操作（关注/发帖/删帖）。生产环境该表始终存在。

### ✅ 已修复 — ISSUE-4（中等）：`statuses_count` 从不维护

**断点：** `AccountExtra.StatusesCount` 被 serializer 输出，但创建/删除 status 时无任何维护逻辑，恒为 0。

**修复：** 在 `Status.AfterCreate`（原创 status）递增、`Status.AfterDelete`（原创 status）递减。转发(reblog)不计入原作者计数。同样带 `HasTable` 守卫以保证在未迁移 schema 时不阻断核心操作。

---

## 维度 3（路由↔控制器 + 认证）— 发现 1 个问题

### ✅ 已修复 — ISSUE-6（严重）：`MuteAccounts.IsMuted` 比较错误字段，导致批量时间线中被静音账户的嘟文永不标记 muted

**断点：**
- `models/mute.go` 的 `MuteAccounts.IsMuted(accountId)` 比较 `m.AccountId == accountId`。
- 但 `mute_accounts` 表中 `account_id` 是执行静音的用户，`target_id` 是被静音的目标。
- 在 `serializer/status.go:232` 中，`muteAccounts` 是按 `Where("account_id = ?", currentAccount.Id)` 加载的，即每条记录的 `m.AccountId` 恒等于当前用户。
- 因此 `IsMuted(status.AccountId)` 只有在"嘟文作者==当前用户"时才为 true → **被当前用户静音的账户的嘟文在批量时间线中永远不会被标记 muted**。

**对比：** 单条版 `Status.IsMuted`（status.go:418）使用正确的 `Where("account_id = ? AND target_id = ?", account.Id, s.AccountId)`，只有批量优化版有此 bug。

**修复：** 将 `IsMuted` 改为比较 `m.TargetId == accountId`，并修正对应的单元测试（原测试断言了错误行为）。

---

## 维度 6（配置依赖）— 发现 1 个问题

### ✅ 已修复 — ISSUE-7（严重）：`STORAGE_TYPE` 配置永远不生效（读取了错误的配置位置）

**断点：**
- `config/config.go` 的 `Config` 结构体有标准字段 `StorageType string \`default:"local" env:"STORAGE_TYPE"\``。
- `loadMapToConfig` 会把带 `env` tag 的标准键加入 `standardConfigsKeyMap`，**只有非标准键才会放入 `C.Other`**。
- 因此 `STORAGE_TYPE` 的值被加载到 `config.C.StorageType`，**不会**出现在 `config.C.Other["STORAGE_TYPE"]`。
- 但 `storage/storage.go` 的 `getStorageType()` 读取的是 `config.C.Other["STORAGE_TYPE"]`（恒为空）→ 永远回退 `"local"`。

**后果：** 无论环境变量如何设置 `STORAGE_TYPE=s3`/`minio`，云存储永远不会启用（`isCloudStorageEnabled()` 恒为 false）。

**修复：** 改为读取 `config.C.StorageType`（与 `health` 模块的正确用法一致）。

---

## 其余维度（1/2/3/4）审查结论：未发现破坏性跨模块问题

### 维度 1（数据流：controller→service→model→DB）
追踪了关键链路（status create/delete/show、account register/follow/relationships、notification timeline、search、conversation、report 等），字段传递完整，无类型不匹配。
- `controllers.GetIdFromContext` / `MustGetAccount` / `AbortIfError` 辅助函数统一处理了 ID 解析与错误中止。
- serializer 的 `ToStatus`/`ToStatuses`/`ToAccount`/`ToNotifications` 与 model 字段一一对应。

### 维度 2（接口契约一致性）
跨包调用的函数签名与返回值匹配：
- `event.Register` / `event.Trigger` / `GetEventManager().Register` 两套 API 都正确映射到同一 EventManager。
- `models.GetFilters` / `GetStatusFilters` / `GetFilterActionsName` 链路完整。
- `feed.GetAccountRecentActiveLocalFollowersId` / `GetAccountFollowingAccounts` 等辅助函数均存在。

### 维度 3（路由注册 vs 控制器）
router.go 中所有注册的 handler 函数均已定义。中间件链正确：
- 公开接口（register/login/instance/captcha/trends/announcements）无认证 ✓
- `maybeAuth` 组（AuthMiddleware 单独，无 CheckAuth）正确用于可登录可不登录的端点 ✓
- 需认证组（AuthMiddleware + CheckAuthMiddleware）覆盖所有写操作 ✓
- Scope 中间件按 read/write/admin 正确分组 ✓
- InternalApiMiddleware 正确保护 /accounts/me、logout、change_password（仅 Web token）✓

### 维度 4（Model 字段 vs DB schema）
migrate 使用 `AutoMigrate`（由 model tag 驱动建表），故 model tag 即 schema 定义，不存在不一致。
- `AccountEmoji` 的 `varchar(64)` 类型与 `int64` 字段的不一致是历史遗留（该 model 仅被 tootctl 清理脚本使用，实际 account↔emoji 关系走 GORM many2many 隐式连接表），不影响功能，未修改。

### 维度 5（Serializer vs Model）
审查了 status/account/notification/report/conversation/list/filter/poll 等全部 serializer，字段映射一致。

---

## 修复文件清单

| 文件 | 修复内容 |
|------|---------|
| `models/status.go` | ISSUE-1/2/4：重命名删除事件为标准名；新增 `BeforeUpdate` 发布 `before_update`；`AfterCreate`/`AfterDelete` 维护 `statuses_count` |
| `models/follow.go` | ISSUE-3：`AfterCreate`/`AfterDelete` 维护 followers/following 计数（新增 `updateFollowCounters`） |
| `models/mute.go` | ISSUE-6：`IsMuted` 改为比较 `TargetId` |
| `models/mute_test.go` | ISSUE-6：修正断言为正确语义 |
| `serializer/notification.go` | ISSUE-5：`ToNotifications` 补充 `ListId` 收集 |
| `storage/storage.go` | ISSUE-7：`getStorageType` 改读 `config.C.StorageType` |

## 验证

- `go build ./...` ✅ 通过
- `go vet ./...` ✅ 通过
- `go test ./models/ ./serializer/ ./feed/... ./notifications/ ./event/ ./middleware/ ./storage/ ./config/ ./controllers/...` ✅ 全部通过
- `go test -race ./models/ ./notifications/ ./serializer/` ✅ 无数据竞争
