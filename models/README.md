# models (数据模型层)

社区平台的数据模型层，基于 [GORM](https://gorm.io) 定义去中心化社交平台（兼容 Mastodon/ActivityPub 协议）的完整数据结构，涵盖账号、动态、社交关系、消息会话、联邦中继、Matrix 集成等全部领域。

本包是整个系统的数据契约来源，所有 `controllers`、`services`、`feed`、`activitypub`、`matrix` 等模块都以这里的 GORM 模型作为唯一数据真相源（single source of truth）。

## 核心功能

- **完整的社交数据模型**：`Account`（账号）、`User`（本地用户）、`Status`（动态）、`Follow`（关注）、`Notification`（通知）等，覆盖 Mastodon API 全部核心实体。
- **去中心化联邦支持**：`Domain`/`OriginId`/`OriginUri` 字段天然区分本地与远程账号和内容，配合 `Relay`（中继）、`ProxyFollow`（代理关注）、`AccountKey`（HTTP Signature 密钥对）支撑 ActivityPub 联邦。
- **Matrix 集成**：`MatrixUser`/`MatrixRoom`/`MatrixMessage` 实现 Matrix 协议与本地账号、会话、动态的双向映射；`Group`/`Contact` 提供跨平台群组与联系人能力。
- **事件驱动钩子**：`Status`、`Notification`、`Follow`、`Report` 等模型在 `BeforeCreate`/`AfterCreate`/`AfterUpdate`/`AfterDelete` 等生命周期钩子中触发 `event` 事件，驱动 feed 时间线、通知、计数器维护等副作用。
- **自动化的领域逻辑**：计数器维护（`statuses_count`/`followers_count`）、`@提及`/`#话题`/`:emoji:` 自动解析绑定、会话（Conversation）自动归并、token 安全编解码等，全部封装在模型的钩子与辅助方法中。
- **安全的敏感数据存储**：`UserSecretData` 通过 `BeforeSave` 钩子对带 `encrypt:"true"` 标签的字段进行 AES 加密；密码使用 bcrypt 不可逆哈希；`AuthToken` 仅存 SHA256 摘要。
- **丰富的辅助查询与集合方法**：每个领域都提供类型化集合（如 `Statuses`、`Accounts`、`Follows`、`PollOptionVotes`）及过滤、分组、ID 提取等便捷方法。

## 架构与结构

本包按业务领域组织，每个 `.go` 文件（及其同名 `_test.go`）对应一个领域。共定义 **70+ 个 GORM 模型**，按领域分组如下。

### 1. 账号与身份 (Account & Identity)

| 模型 | 表名 | 说明 |
|------|------|------|
| `Account` | `accounts` | 账号实体（本地 + 远程），社交层的核心身份。`Domain == nil` 表示本地账号 |
| `AccountExtra` | `accounts_extra` | 账号计数器（`followers_count`/`following_count`/`statuses_count`）与自定义字段 |
| `AccountField` | - | 账号自定义资料字段（JSON 内嵌于 `AccountExtra.Fields`） |
| `AccountEmoji` | `account_emojis` | 账号 ↔ emoji 多对多关联 |
| `AccountKey` | `account_keys` | 本地账号的 RSA 密钥对，用于联邦 HTTP Signatures |
| `User` | `users` | 本地登录用户（含 `EmailHash`、`IsAdmin`、`Status`） |
| `UserSecretData` | `user_secret_data` | 用户敏感数据（密码 bcrypt、邮箱/手机 AES 加密） |
| `UserMeta` | `user_meta` | 用户 KV 元数据（`(user_id, key)` 联合索引） |
| `UserSetting` | `user_setting` | 用户设置（JSON `Data`：`aggregates_reblogs`/`follow_protect` 等） |
| `Role` / `RoleUser` | `roles` / `role_users` | 角色与权限（位掩码 `Permissions`），`RoleUser` 为用户↔角色关联 |

**用户状态枚举**（`user.go`）：

| 常量 | 值 | 含义 |
|------|----|------|
| `UserStatusNormal` | 0 | 正常 |
| `UserStatusUnapproved` | 1 | 未审批 |
| `UserStatusRejected` | 2 | 被拒绝 |
| `UserStatusUnverifiedMail` | 3 | 未验证邮箱 |

**权限位掩码**（`role.go`）：`PermissionAdministrator`、`PermissionManageReports`、`PermissionManageUsers`、`PermissionDeleteUserData` 等共 20 个 `uint64` 位掩码常量，通过 `Role.HasPermission()` / `SetPermission()` 做位运算。

### 2. 内容与动态 (Content & Statuses)

| 模型 | 表名 | 说明 |
|------|------|------|
| `Status` | `statuses` | 动态主体，聚合 `Account`/`Content`/`Attachments`/`Poll`/`Tags`/`Emojis`/`Reblog` 等关联 |
| `StatusContent` | `status_contents` | 动态正文文本（`content`/`spoiler_text`/`card`） |
| `StatusMediaAttachment` | `status_media_attachments` | Status ↔ Attachment 关联 |
| `StatusTag` / `StatusEmoji` / `StatusToAccount` | `status_tags`/`status_emojis`/`status_to_accounts` | 动态与话题/emoji/@提及的关联 |
| `StatusHistory` | `status_histories` | 动态编辑历史 |
| `FavoriteStatus` | `favorite_statuses` | 收藏 |
| `BookmarkStatus` | `bookmark_statuses` | 书签 |
| `PinStatus` | `pin_statuses` | 置顶 |
| `Tag` | `tags` | 话题标签（含使用计数） |
| `Emoji` | `emojis` | 自定义 emoji（`shortcode` 唯一） |
| `Poll` | `polls` | 投票 |
| `PollOption` | `poll_options` | 投票选项 |
| `PollOptionVote` | `poll_option_votes` | 投票记录 |

**动态类型枚举**（`status.go`）：`StatusTypeStatus`/`StatusTypeRelog`/`StatusTypeReply`/`StatusTypeCard`/`StatusTypePoll`。

**可见性枚举**（`status.go`）：

| 常量 | 值 | 含义 |
|------|----|------|
| `StatusVisibilityPublic` | 0 | 公开（全站可见） |
| `StatusVisibilityUnlisted` | 1 | 不出现在公共时间线 |
| `StatusVisibilityPrivate` | 2 | 仅关注者 |
| `StatusVisibilityDirect` | 3 | 仅 @提及的用户 |

> `Status` 通过 `BeforeSave`/`AfterCreate`/`AfterUpdate`/`BeforeUpdate`/`AfterDelete` 触发 `status` 事件管理器事件，`AfterCreate`/`AfterDelete` 自动维护作者的 `statuses_count`；`SetConversation()` 在 `AfterSave` 中自动归并会话。

### 3. 社交关系 (Social Relations)

| 模型 | 表名 | 说明 |
|------|------|------|
| `Follow` | `follows` | 关注关系（支持「已关注」与「申请中」两种状态） |
| `ProxyFollow` | `proxy_follows` | 系统代理账号关注远程账号的原因追踪（多原因引用计数） |
| `Block` | `blocks` | 屏蔽（账号级 + 域名级，`Type` 字段区分） |
| `DomainBlock` | `domain_blocks` | 实例级域名屏蔽（`suspend`/`silence`/`noop`） |
| `MuteAccount` | `mute_accounts` | 静音账号（支持过期时间） |
| `MuteStatus` | `mute_statuses` | 静音单条动态 |
| `List` | `lists` | 列表 |
| `ListAccount` | `list_accounts` | 列表成员 |
| `ListFollowAccount` | `list_follow_accounts` | 列表关注者 |

**关注状态枚举**（`follow.go`）：`FollowStatusFollowed`（已关注）、`FollowStatusRequested`（申请中）。`AfterCreate`/`AfterDelete` 钩子原子维护 `following_count`/`followers_count`。

**列表可见性/回复策略枚举**（`list.go`）：`ListVisibilityPublic`/`Private`/`Followers`，`ListRepliesPolicyList`/`Followed`/`None`。

### 4. 消息与会话 (Messaging & Conversations)

| 模型 | 表名 | 说明 |
|------|------|------|
| `Conversation` | `conversations` | 会话（聚合 `StatusesIds`/`AccountsIds`） |
| `AccountConversation` | `account_conversations` | 每账号的会话视图（含 `LastStatusId` 已读位） |
| `Notification` | `notifications` | 通知（mention/reblog/favourite/follow/poll 等 10 种类型） |
| `Announcement` | `announcements` | 公告（支持定时发布、@提及、emoji、tag） |
| `Marker` | `markers` | 时间线已读标记（`home`/`notifications` 等多个 timeline） |
| `MarkerTimelineItem` | - | 单个时间线的已读游标（JSON 内嵌） |

**通知类型枚举**（`notification.go`）：`NotificationTypeMention`/`Reblog`/`Favourite`/`Follow`/`Poll`/`FollowRequest`/`StatusUpdate`/`AdminReport`/`AddList`/`RemoveList`，支持 `GetTypeString()` 与 `NotificationTypeFromString()` 双向转换。

### 5. 媒体与附件 (Media & Attachments)

| 模型 | 表名 | 说明 |
|------|------|------|
| `Attachment` | `attachments` | 动态附件（image/video/audio/file/gifv） |
| `MediaAttachment` | `media_attachments` | 通用媒体附件（含尺寸/时长/blurhash） |
| `Thumbnail` | `thumbnails` | 缩略图记录 |
| `ThumbnailJob` | `thumbnail_jobs` | 缩略图生成任务（优先级/重试） |
| `ThumbnailStats` | `thumbnail_stats` | 缩略图统计 |
| `ThumbnailConfig` | `thumbnail_configs` | 缩略图 KV 配置 |

**附件类型枚举**（`attachment.go`）：`AttachmentTypeImage`/`Video`/`Audio`/`File`/`Gifv`/`Unknown`。

**媒体类型**（`media.go`）：`MediaType` 字符串类型，取值 `image`/`video`/`audio`/`unknown`，`GetMediaType(contentType)` 按 MIME 推断。

**缩略图状态/配置常量**（`thumbnail.go`）：`ThumbnailStatusPending`/`Processing`/`Completed`/`Failed`/`Skipped`；`AccessMode`（`proxy`/`redirect`）、`StorageType`（`local`/`s3`/`minio`/`aliyun_oss`/`tencent_cos`）。

### 6. 认证与应用 (Auth & Applications)

| 模型 | 表名 | 说明 |
|------|------|------|
| `Application` | `applications` | OAuth 应用 |
| `ApplicationScope` | - | 应用权限作用域（4 组 `uint64` 位掩码：read/write/admin/other） |
| `ApplicationUserAuth` | `application_user_auths` | 应用授权记录 |
| `AuthToken` | `auth_tokens` | 登录 token（仅存 SHA256 摘要，支持 Web 与 OAuth 两种类型） |
| `AuthTokenData` | - | token 内携带数据（AES 加密、MD5 校验、常量时间比较防时序攻击） |
| `InviteCode` | `invite_codes` | 邀请码（次数限制 + 过期 + 注册后动作） |

**应用权限**（`application.go`）：`AppScopeRead`/`AppScopeWrite`/`AppScopeAdmin`/`AppScopeFollow`/`AppScopePush`/`AppScopeCrypto` 等大量位掩码常量，分 `AppScopeTypeRead`/`Write`/`Admin`/`Other` 四类。`ApplicationScope` 实现 `MarshalJSON`/`UnmarshalJSON`/`Scan`/`Value`，将 4 个 `uint64` 序列化为 hex 字符串。

**Token 类型**（`auth_token.go`）：`AuthTokenTypeWeb`、`AuthTokenTypeOauth`。

### 7. 审核与管理 (Moderation & Admin)

| 模型 | 表名 | 说明 |
|------|------|------|
| `Report` | `reports` | 举报（关联被举报账号/状态/规则） |
| `Rule` | `rules` | 服务器规则 |
| `SystemSetting` | `system_settings` | 系统设置（KV，`Val` 为 JSON） |
| `Filter` | `filters` | 内容过滤器（账号级 + 全局 `account_id=0`） |
| `FilterKeyword` | - | 过滤关键词（`Keyword`/`WholeWord`） |

**举报分类枚举**（`report.go`）：`ReportCategoryOther`/`Spam`/`Legal`/`Violation`。

**过滤动作枚举**（`filter.go`）：`FilterActionNone`/`Warn`/`Hide`。

### 8. 联邦中继 (Federation & Relay)

| 模型 | 表名 | 说明 |
|------|------|------|
| `Relay` | `relays` | 联邦中继（广播公开内容到订阅实例） |

**中继状态枚举**（`relay.go`）：`RelayStateIdle`/`Pending`/`Accepted`/`Rejected`。提供 `GetEnabledRelays`/`CreateRelay`/`GetRelayByInboxURL` 等查询函数。

### 9. Matrix 集成 (Matrix Integration)

| 模型 | 表名 | 说明 |
|------|------|------|
| `MatrixUser` | `matrix_users` | 本地账号 ↔ Matrix 用户映射 |
| `MatrixRoom` | `matrix_rooms` | Matrix 房间 ↔ 本地 Conversation 映射 |
| `MatrixMessage` | `matrix_messages` | Matrix 事件 ↔ 本地 Status 映射（去重） |
| `Group` | `groups` | 群组（支持 Matrix 桥接） |
| `GroupMember` | `group_members` | 群组成员（本地 + Matrix） |
| `GroupMessage` | `group_messages` | 群组消息 |
| `Contact` | `contacts` | 联系人（本地 + Matrix） |
| `ContactRequest` | `contact_requests` | 好友请求（跨平台） |

**群组类型/成员角色枚举**（`group.go`）：`GroupTypeLocal`/`Bridged`/`Matrix`；`GroupMemberRoleMember`/`Admin`/`Owner`。

**联系人类型/状态枚举**（`contact.go`）：`ContactTypeLocal`/`Matrix`；`ContactStatusPending`/`Accepted`/`Blocked`。

## 用法示例

### 导入

```go
import "github.com/kasuganosora/community/models"
```

### 创建账号与动态

```go
// 创建本地账号
account := &models.Account{
    Username:    "alice",
    DisplayName: "Alice",
}
if err := db.Create(account).Error; err != nil {
    // BeforeSave 会自动设置 Acct = "alice@__LOCAL__"
    // AfterCreate 会自动创建关联的 AccountExtra
    return err
}

// 创建动态
status := &models.Status{
    AccountId:  account.Id,
    Visibility: models.StatusVisibilityPublic,
}
status.Content = models.StatusContent{
    Content: "Hello world! #hello @bob",
}
if err := db.Create(status).Error; err != nil {
    // AfterCreate 会触发 "status" 事件并递增作者的 statuses_count
    return err
}
// StatusContent 的 BeforeCreate 会自动解析并绑定 #hello 话题与 @bob 提及
```

### 查询与预加载

```go
// 完整预加载动态的所有关联（含 Reblog 嵌套）
var status models.Status
err := models.StatusLoadAllQuery(db).
    Where("id = ?", statusId).
    First(&status).Error

// 批量加载多条动态的扩展字段，避免 N+1 查询
var statuses []*models.Status
db.Find(&statuses)
if err := models.BatchLoadAllExtendFields(db, statuses); err != nil {
    return err
}
```

### 可见性判断

```go
// 字符串 <-> 枚举互转
status.SetVisibilityByString("private")
fmt.Println(status.GetVisibilityString()) // "private"

// 综合可见性判断（基于关注关系与 @提及）
canView := status.CanView(currentAccount, myFollows)
```

### 关注关系与计数

```go
// 建立关注（Followed 状态会自动维护计数）
follow := &models.Follow{
    AccountId: follower.Id,
    FollowId:  target.Id,
    Status:    models.FollowStatusFollowed,
}
db.Create(follow)
// 申请关注则使用 FollowStatusRequested，不计入计数

// GORM Scope 便捷查询
var follows models.Follows
db.Scopes(models.Followed).Where("account_id = ?", account.Id).Find(&follows)
// 查询申请中的关注
db.Scopes(models.FollowRequested).Where("account_id = ?", account.Id).Find(&follows)
```

### 通知

```go
// 创建通知
n := &models.Notification{
    Type:          models.NotificationTypeFollow,
    AccountId:     target.Id,
    FromAccountId: from.Id,
}
db.Create(n)

// 字符串互转
t := models.NotificationTypeFromString("favourite") // NotificationTypeFavourite
fmt.Println(n.GetTypeString())                      // "follow"

// 加载通知的扩展字段（Status/Account/FromAccount/Report/List）
n.LoadAllExtendFields(db)
```

### 令牌管理

```go
// 创建 token（返回数据库行和明文 token 字符串）
tokenRow, tokenStr := models.NewAuthToken(
    user.Id,
    models.AuthTokenTypeWeb,
    appId, 0, scopes, 3600, encKey,
)
db.Create(tokenRow)

// 解析 token
tokenData, err := models.GetAuthTokenData(tokenStr, encKey)

// 查找 token 记录
tokenRow, err := models.GetTokenByTokenStr(tokenStr)
```

### 角色与权限

```go
// 分配角色
models.AssignRole(db, user.Id, role.Id)

// 获取用户合并后的权限位掩码
permissions, _ := models.GetUserPermissions(db, user.Id)
if permissions&models.PermissionManageUsers != 0 {
    // 用户具有管理用户的权限
}
```

### 用户元数据

```go
// 保存（value 为 nil 时删除该 key）
models.SaveUserMeta(db, user.Id, "last_login_ip", "1.2.3.4")

// 读取（自动 JSON 反序列化到 outValue）
var ip string
models.GetUserMeta(db, user.Id, "last_login_ip", &ip)
```

### 缩略图查询

```go
// 生成 URL 哈希作为唯一键
hash := models.GenerateURLHash(originalURL)

// 按状态查询缩略图任务
db.Where("status = ?", models.ThumbnailStatusCompleted).Find(&thumbnails)
```

## 依赖关系

### 外部依赖

| 依赖 | 用途 |
|------|------|
| [`gorm.io/gorm`](https://gorm.io) | ORM 框架，定义模型、关联、钩子与查询 |
| `gorm.io/gorm/clause` / `schema` | GORM 子包，用于 `clause.Associations`、自定义序列化器 |
| [`github.com/google/uuid`](https://pkg.go.dev/github.com/google/uuid) | `User.UUID`、`Application.Uuid` 生成 |
| `golang.org/x/crypto/bcrypt` | 用户密码哈希（`EncryptPassword`/`CompareHashAndPassword`） |
| `github.com/pkg/errors` | 错误包装 |

### 项目内部依赖

| 内部包 | 依赖关系 |
|--------|----------|
| `config` | `Account.BeforeSave` 读取 `config.C.EncryptedKey`；`UserSecretData` 加密密钥；`GetEmailHash` |
| `storage` | `Account.SaveAvatar` 保存头像文件 |
| `utils` | `AesEncrypt`/`AesDecrypt`、`CheckIsImage`、`UniqueSlice`、`GetRandomString`、`Hash` 等工具函数 |
| `event` | `Status`/`Notification`/`Follow`/`Report`/`ListAccount` 等模型的钩子触发事件 |
| `db` | `auth_token.go`（`GetTokenByTokenStr`）、`list.go`（`ListCanView`）通过 `db.GetDB()` 获取连接 |

### 设计约定

- **表名**：所有模型通过 `TableName()` 显式指定表名，命名与 Mastodon/Go惯例一致（`accounts`/`statuses`/`notifications` 等蛇形复数）。
- **软删除**：核心模型（`Account`/`Status`/`User`/`Group` 等）使用 `gorm.DeletedAt` 实现软删除。
- **计数器维护**：`Status.AfterCreate`/`AfterDelete`、`Follow.AfterCreate`/`AfterDelete`、`FavoriteStatus` 等通过 GORM 钩子原子维护派生计数；当 `accounts_extra` 表不存在（引导/测试阶段）时优雅跳过，不阻断核心操作。
- **自动绑定**：`StatusContent.BeforeCreate`/`BeforeUpdate` 自动解析正文的 emoji/话题/@提及并绑定关联，维护标签使用计数。
- **类型化集合**：每个领域提供集合类型（如 `Statuses`、`Accounts`、`Follows`、`PollOptionVotes`、`Attachments`、`Tags`、`Emojis`），内置 `GetIds()`、`Filter()`、分组映射等便捷方法。
