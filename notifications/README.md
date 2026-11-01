# notifications (通知系统)

基于事件驱动（event-driven）的 Mastodon 风格通知系统模块。

本包**不对外暴露任何函数**，而是通过 [`init()`](#safe-go) 隐式注册一组事件处理器（event handlers），在各业务模块（account、status、poll、list、report 等）触发相应事件时自动创建 `models.Notification` 记录。通知数据最终经由 [`serializer.ToNotification`](../serializer/notification.go) 序列化为 Mastodon API 兼容格式返回给客户端。

> 设计目标：业务逻辑与通知发送**完全解耦**，新增一种通知类型只需新增一个事件处理器，无需修改被通知的业务代码。

## 核心功能

- **事件订阅**：监听 `account`、`status`、`favourite`、`unfavourite`、`poll`、`list`、`report` 等多个事件池（event pool）。
- **本地优先**：所有通知只发送给本地用户（`Account.Domain == nil`），自动跳过远程（federated）账号。
- **去重与幂等**：关注、收藏等场景内置去重逻辑，避免重复事件产生重复通知；取消收藏/取消转发时会同步删除对应通知。
- **安全并发**：所有通知 goroutine 通过 [`safeGo`](#safe-go) 启动，自动捕获 panic，单个通知失败不会拖垮主进程。
- **数据竞态保护**：在 goroutine 中查询数据库时一律使用局部变量，不修改传入的共享 model 指针，避免 data race。

## 支持的通知类型

通知类型常量定义于 `models` 包，本包负责写入其中以下几种：

| 常量 | 字符串 | 说明 |
|------|--------|------|
| `NotificationTypeMention` | `mention` | 有人 `@` 提及了你 |
| `NotificationTypeReblog` | `reblog` | 有人转发了你的 status |
| `NotificationTypeFavourite` | `favourite` | 有人收藏了你的 status |
| `NotificationTypeFollow` | `follow` | 有人关注了你 / 关注了你创建的列表 |
| `NotificationTypeFollowRequest` | `follow_request` | 有人发起了关注请求 |
| `NotificationTypePoll` | `poll` | 你参与的投票已结束 |
| `NotificationTypeStatusUpdate` | `update` | 不再发送，也不出现在通知箱。帖子保存（含只刷新计数或表情的更新）不是编辑通知 |
| `NotificationTypeAdminReport` | `admin_report` | 你提交的举报已被处理 |
| `NotificationTypeAddList` | `add_list` | 有人把你加入了他的列表 |

## 架构 / 结构

本包为单层扁平结构（无子包），按业务域拆分为多个 `.go` 文件，每个文件在 `init()` 中注册其负责的事件处理器。

```
notifications/
├── account.go   # follow / follow-request 通知
├── status.go    # mention / reblog / favourite 通知（文件最大，逻辑最复杂）
├── poll.go      # 投票结束通知
├── list.go      # 列表相关通知（被加入列表 / 列表被关注）
├── report.go    # 举报处理结果通知
├── safe.go      # safeGo：panic 安全的 goroutine 启动器（基础设施）
```

### 各文件说明

#### `account.go` — 关注通知

| 监听事件池 | 事件 | 处理函数 |
|-----------|------|---------|
| `account` | `follow` | `followNotification` |
| `account` | `follow-request` | `followRequestNotification` |

- 通知被关注（或被请求关注）的本地用户。
- `followNotification` 会先 `Count` 同 `from/to` 的既有 `follow` 通知，存在则不再创建，防止 `Follow.AfterUpdate` 重复触发产生重复通知。

#### `status.go` — 帖子与互动通知

| 监听事件池 | 事件 | 处理函数 |
|-----------|------|---------|
| `status` | `*`（通配） | `after_create` → `creatReblogNotification` / `creatMentionNotification`<br>`after_update` → `updateReblogNotification` / `updateMentionNotification`<br>`after_delete` → `removeReblogNotification` / `removeMentionNotification` |
| `favourite` | `favourite` | `favouriteNotification` |
| `unfavourite` | `favourite` | `unfavouriteNotification` |

- **Mention**：编辑 status 时会 diff 新旧提及列表，对新增账号创建通知、对移除账号删除通知。已经提及过的人不会再收到编辑通知。
- **Reblog**：通知原始 status 的作者（解析 reblog 链，使用原始 status id）。之后的保存不会给转发者发送编辑通知。
- **Favourite**：通知原始 status 的作者；取消收藏时按 `from_account_id` 精确删除对应通知，避免误删他人通知。

#### `poll.go` — 投票结束通知

| 监听事件池 | 事件 | 处理函数 |
|-----------|------|---------|
| `poll` | `finish` | `finishPollNotification` |

- 投票到期后给所有参与投票的本地用户发送结果通知。
- 通过 `map[uint64]bool` 对投票者 ID 去重（处理多选投票场景）。

#### `list.go` — 列表通知

| 监听事件池 | 事件 | 处理函数 |
|-----------|------|---------|
| `list` | `add-to-list` | `listAddToListNotification` |
| `list` | `follow-list` | `listFollowListNotification` |

- 被加入列表时通知被添加者；列表被关注时通知列表创建者。

#### `report.go` — 举报通知

| 监听事件池 | 事件 | 处理函数 |
|-----------|------|---------|
| `report` | `after_update` | `reportProcessedNotification` |

- 仅当 `report.ActionTakenAt != nil`（管理员已处理）时触发，通知举报发起者。

#### `safe.go` — 并发基础设施

```go
func safeGo(name string, fn func())
```

- 在新 goroutine 中执行 `fn`，并通过 `defer recover()` 捕获 panic，输出调用栈。
- 通知系统的所有 goroutine 都应通过 `safeGo` 启动，**这是整个通知系统稳定性的基础**。

## 工作流程

以"用户 A 关注用户 B"为例，完整链路如下：

```
Follow.AfterCreate (gorm hook)
        │  event.Trigger("account", {Name:"follow", Data: follow})
        ▼
notifications/account.go init() 注册的 Handler
        │  type-assert 出 *models.Follow
        ▼
safeGo("followNotification", ...)        ── 异步、panic 安全
        │
        ├─ isLocalAccount(B)  → 查询是否本地账号
        ├─ 去重检查 (Count)    → 避免重复通知
        └─ db.Create(&Notification{Type: Follow, AccountId: B, FromAccountId: A})
                                                  │
                                                  ▼
                          Notification.AfterCreate hook
                          event.Trigger("notification", "after_create", nf)
                                                  │
                          ▼ (如接入实时推送则可在此消费)
```

## 用法示例（真实 API）

本包**无需手动调用**，只要 `import _ "github.com/kasuganosora/community/notifications"` 即可完成所有事件处理器注册。在主程序中通过空导入引入即可：

```go
package main

import (
    _ "github.com/kasuganosora/community/notifications" // 注册通知事件处理器
)

func main() {
    // ... 启动 server
}
```

此后任何业务模块只要触发对应事件，通知即自动生成。例如用户发起关注时，`models.Follow` 的 gorm hook 会触发 `account/follow` 事件，本包自动创建通知记录。

### 客户端读取通知（Mastodon API）

通知记录由 [`serializer.ToNotifications`](../serializer/notification.go) 序列化后通过如下 API 返回：

```
GET /api/v1/notifications
```

响应示例（节选）：

```json
[
  {
    "id": "1024",
    "type": "follow",
    "created_at": "2024-01-01T00:00:00.000Z",
    "account": {
      "id": "1",
      "username": "alice",
      "acct": "alice@example.com"
    }
  },
  {
    "id": "1023",
    "type": "mention",
    "created_at": "2024-01-01T00:01:00.000Z",
    "account": { "id": "2", "username": "bob", "acct": "bob@example.com" },
    "status": { "id": "999", "content": "..." }
  }
]
```

### 新增一种通知类型

1. 在 `models` 包中新增 `NotificationTypeXxx` 常量及 `GetTypeString()` 分支；
2. 在本包新建一个 `.go` 文件，在 `init()` 中 `event.Register` 对应事件池；
3. 在 handler 内通过 `safeGo` 启动通知逻辑，写入 `models.Notification` 即可。

```go
func init() {
    event.Register("xxx", "action", &event.Handler{Exec: func(event event.Event) bool {
        data, ok := event.Data.(*models.Xxx)
        if !ok {
            return true
        }
        safeGo("xxxNotification", func() { xxxNotification(data) })
        return true
    }})
}

func xxxNotification(data *models.Xxx) {
    // 本地账号检查 / 去重 / db.Create(&models.Notification{...})
}
```

## 依赖关系

| 依赖 | 用途 |
|------|------|
| [`event`](../event) | 注册与触发事件处理器（`event.Register` / `event.Handler` / `event.Event`） |
| [`models`](../models) | 数据模型（`Notification`、`Account`、`Status`、`Follow` 等）及 `GetLocalAccounts` |
| [`db`](../db) | `db.GetDB()` 获取 gorm 连接，读写通知记录 |
| [`log`](../log) | 错误日志（`log.ErrorV`） |
| [`utils`](../utils) | `GetExistsMap` 等集合辅助函数（mention diff 等） |
| `github.com/pkg/errors` | 错误包装（`errors.Wrap` / `errors.Is`） |
| `gorm.io/gorm` | `gorm.ErrRecordNotFound` 判定 |

> 被依赖方（上游）：本包被主程序通过空导入引入；生成后的通知记录由 [`serializer`](../serializer/notification.go) 序列化，再由 [`controllers/api/timelines`](../controllers/api/timelines/notification.go) 等控制器返回给客户端。
