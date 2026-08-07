# serializer (响应序列化)

将内部数据模型（`models.*`）转换为 Mastodon API 兼容 JSON 结构的序列化层。

本包是 HTTP 响应返回给客户端前的最后一道处理环节：把 `uint64` 主键转为字符串、补全 `acct`/`url`/`uri` 等 Mastodon 约定的派生字段、注入"当前用户视角"的互动状态（`favourited`/`reblogged`/`muted`/`bookmarked` 等），并应用屏蔽（block）、静音（mute）、过滤（filter）等可见性规则。

> 命名约定：所有转换函数以 `To` 开头，单个对象为 `ToXxx`，列表为 `ToXxxs`（复数）。

## 核心功能

- **Mastodon API 格式序列化**：覆盖 Account、Status、Notification、Poll、Attachment、Conversation、Report 等全部主流实体。
- **当前用户视角**：`ToStatus`/`ToStatuses`/`ToPoll`/`ToNotification` 等函数接受 `currentAccount *models.Account`，据此计算 `favourited`、`reblogged`、`voted`、`own_votes`、`filtered` 等用户相关字段。
- **批量查询优化**：`ToStatuses` / `ToNotifications` / `ToReports` / `ToConversations` 等复数函数采用"先聚合 ID、批量查询、再 map 回填"模式，避免 N+1 查询。
- **可见性过滤**：在列表序列化时自动剔除被当前用户 block 的账号，并标记被 mute 的内容。
- **本地域自动补全**：通过 `systemsettings.KeyDomain` 自动为 `acct`、`url`、`uri` 等字段补全本站域名。
- **空值安全**：所有 `ToXxx` 函数对 `nil` 输入做了防御处理，避免 panic。

## 序列化器一览

本包为扁平单包结构，按实体拆分文件：

| 文件 | 主要导出类型 | 主要转换函数 | 说明 |
|------|-------------|-------------|------|
| `account.go` | `Account`、`Mention` | `ToAccount` / `ToAccounts` / `ToMention` / `ToMentions` | 账号资料（含 `acct`/`url`/`uri`、emoji、fields） |
| `status.go` | `Status` | `ToStatusBase` / `ToStatus` / `ToStatuses` | 帖子（核心，含互动状态与过滤） |
| `notification.go` | `Notification` | `ToNotification` / `ToNotifications` | 通知（聚合 account/status/report/list） |
| `poll.go` | `Poll`、`PollOption`、`Counts` | `ToBasePoll` / `ToPoll` / `ToPollsMapByStatusId` | 投票（含 `voted`/`own_votes`） |
| `attachment.go` | `Attachment` | `ToAttachment` / `ToAttachments` | 帖子附件 |
| `media.go` | `MediaAttachmentResponse`、`MediaAttachmentMeta` 等 | `ToMediaAttachment` / `ToMediaAttachmentList` | 媒体附件（v2 上传接口格式） |
| `conversation.go` | `Conversation` | `ToConversation` / `ToConversations` | 会话（私信用） |
| `report.go` | `Report` | `ToReport` / `ToReports` | 举报（含目标账号/处理人） |
| `filter.go` | `Filter`、`FilterKeyword` | `ToFilter` / `ToFilterKeywords` / `ToFilterKeyword` | 内容过滤器 |
| `list.go` | `List` | `ToList` / `ToLists` | 列表 |
| `tag.go` | `Tag`、`TagHistory` | `ToTag` / `ToTags` / `ToTagWithHistory` | 标签（含使用历史） |
| `emoji.go` | `Emoji` | `ToEmoji` / `ToEmojis` | 自定义表情 |
| `field.go` | `Field` | `ToFields` | 账号资料字段（含验证时间） |
| `mention` | （在 `account.go`） | （见上） | @ 提及的精简账号信息 |
| `application.go` | `Application` | `ToApplication` | 发帖应用（nil 时回退为 `web`） |
| `scheduled_status.go` | `ScheduledStatus`、`ScheduledStatusParams`、`ScheduledStatusPollParams` | `ToScheduledStatus` / `ToScheduledStatuses` | 定时发布帖子 |
| `history.go` | `HistoryStatus` | `ToHistoryStatusBase` / `ToHistoryStatus` / `ToHistoryStatuses` | 帖子编辑历史 |
| `source.go` | `Source` | `ToSource` | 帖子纯文本源（Markdown 源码） |
| `rule.go` | `Rule` | `ToRule` / `ToRules` | 实例规则 |
| `follow_status.go` | `FollowStatus` | `ToFollowStatus` | 账号关系状态（following/blocking/muting 等） |
| `card.go` | `Card`（`map[string]any`） | — | 链接预览卡片 |

## 架构 / 结构

```
        models.*  (内部模型, uint64 主键)
            │
            ▼
   serializer.ToXxx(model, currentAccount)
            │
            ├── 字段映射 (uint64 → string)
            ├── 域名补全 (systemsettings.KeyDomain)
            ├── 派生字段 (acct / url / uri)
            ├── 当前用户视角 (favourited / reblogged / voted ...)
            └── 可见性过滤 (block / mute / filter)
            │
            ▼
   serializer.Xxx  (Mastodon JSON 结构)
            │
            ▼
   controllers → c.JSON(200, ...) → 客户端
```

### 批量序列化的回填模式

`ToStatuses`、`ToNotifications`、`ToReports` 等复数函数普遍采用如下模式以规避 N+1：

```go
// 1. 聚合所有需要预加载的 ID
statusIds   := ...
accountIds  := ...
// 2. 一次性批量查询
db.Where("id in ?", accountIds).Find(&accounts)
// 3. 序列化为 Mastodon 结构
accsMap := utils.MapByAnyFiled(accounts, func(a *Account) uint64 { return a.intId })
// 4. 按源数据顺序从 map 回填，保证顺序与去重
for _, n := range ns {
    nf.Account = accsMap[n.FromAccountId]
}
```

> `Account.intId`、`Status.intId`、`Rule.intId` 等为未导出字段（`json:"-"`），专门用于上述批量回填时的内部映射，不输出到 JSON。

## 用法示例（真实 API）

### 单个 Status 序列化

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/kasuganosora/community/controllers"
    "github.com/kasuganosora/community/serializer"
)

func ShowStatus(c *gin.Context) {
    status, err := getStatusFromDB(id)
    controllers.AbortIfError(c, err)

    account := controllers.MustGetAccount(c)   // 当前登录用户（可为 nil）

    ret, err := serializer.ToStatus(status, account)
    controllers.AbortIfError(c, err)

    c.JSON(200, ret)
}
```

响应示例：

```json
{
  "id": "999",
  "created_at": "2024-01-01T00:00:00.000Z",
  "content": "<p>hello world</p>",
  "account": { "id": "1", "username": "alice", "acct": "alice@example.com" },
  "favourited": true,
  "reblogged": false,
  "bookmarked": false,
  "replies_count": 3,
  "media_attachments": [],
  "mentions": [],
  "tags": [],
  "emojis": []
}
```

### 批量通知序列化（N+1 优化）

```go
func NotificationTimeline(c *gin.Context) {
    account := controllers.MustGetAccount(c)

    nfs := make(models.Notifications, 0)
    db.GetDB().Model(&models.Notification{}).
        Where("account_id = ?", account.Id).
        Order("id DESC").Limit(20).Find(&nfs)

    // 一次批量加载所有关联 account / status / report / list，无 N+1
    ret, err := serializer.ToNotifications(nfs, account)
    controllers.AbortIfError(c, err)

    c.JSON(200, ret)
}
```

### Account 与 Mention

```go
// 完整账号资料（用于 /api/v1/accounts/:id）
acc := serializer.ToAccount(account)

// @ 提及的精简结构（用于 status.mentions 字段）
mentions := serializer.ToMentions(status.ToAccounts)
```

### Attachment 与 Media

```go
// 帖子内嵌附件（v1 API）
atts := serializer.ToAttachments(status.Attachments)

// 媒体上传响应（v2 /api/v2/media，含 meta.original/small 尺寸信息）
resp, err := serializer.ToMediaAttachment(media)
```

## 核心类型说明

### `Status`（最复杂的序列化器）

| 类别 | 字段 | 说明 |
|------|------|------|
| 标识 | `Id`、`CreatedAt`、`EditedAt`、`Uri`、`Url` | `Id` 为字符串，`Uri/Url` 自动补全域名 |
| 关系 | `InReplyToId`、`InReplyToAccountId` | 回复链（字符串或 null） |
| 内容 | `Content`、`SpoilerText`、`Sensitive`、`Language`、`Visibility` | |
| 计数 | `RepliesCount`、`ReblogsCount`、`FavouritesCount`、`QuotesCount` | |
| 互动状态 | `Favourited`、`Reblogged`、`Muted`、`Bookmarked`、`Pinned`、`Filtered` | 依赖 `currentAccount`，未登录时为默认值 |
| 嵌套 | `Reblog`、`Account`、`MediaAttachments`、`Mentions`、`Tags`、`Emojis`、`Card`、`Poll`、`Application` | 递归调用其它 `ToXxx` |

> `ToStatusBase` 只做基础字段映射（无用户视角），`ToStatus` 在其基础上补充互动状态与过滤；处理 reblog 时会递归调用 `ToStatus(status.Reblog, currentAccount)`。

### `Notification`

聚合 `Account`（触发者）、`Status`、`Report`、`List` 四类可选载荷，`type` 由 `models.Notification.GetTypeString()` 映射为 Mastodon 字符串（`mention`/`reblog`/`favourite`/`follow` 等）。

## 依赖关系

| 依赖 | 用途 |
|------|------|
| [`models`](../models) | 源数据模型、`StatusLoadAllQuery`、`GetFilters`、`GetLocalAccounts` 等 |
| [`db`](../db) | `db.GetDB()` 获取 gorm 连接（`ToNotification`/`ToReport`/`ToConversation` 等需回查关联） |
| [`systemsettings`](../systemsettings) | `KeyDomain`、`KeyMediaAttachmentsBaseURL` 等站点配置（补全 url/uri） |
| [`config`](../config) | `config.C.AppBaseURL`（`media.go` 构建媒体 URL） |
| [`utils`](../utils) | `MapByAnyFiled`、`GetExistsMap`、`GetStructsMap`、`NumberArrayToStingArray`、`Max` 等集合工具 |
| `gorm.io/gorm` / `gorm.io/gorm/clause` | `*gorm.DB` 参数与 `clause.Associations` 预加载 |
| 标准库 | `strconv`（uint64→string）、`fmt`、`time` |

> 被依赖方（上游）：本包被 [`controllers`](../controllers) 各控制器、[`feed`](../feed) 推送管道等大量调用，是整个 API 响应链路的统一出口。
