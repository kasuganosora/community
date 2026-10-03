# matrix (Matrix 桥接)

实现基于 Matrix 协议的去中心化通信与 Application Service 桥接，支持将本地用户、私信、群组与 Matrix 房间/用户双向映射，并提供一个内置的 Matrix Homeserver 实现。

## 核心功能

- **Matrix 协议桥接**：通过 Application Service (AS) API 将本地用户映射为 Matrix 虚拟用户，实现跨平台消息互通。
- **双向消息同步**：本地私信（direct）/群组消息与 Matrix 房间消息双向转换与投递。
- **用户/房间/群组映射**：`UserMapper`、`RoomMapper`、`GroupMapper` 三层映射服务维护跨系统身份对应关系。
- **联系人服务**：`ContactService` 支持本地用户与 Matrix 用户之间添加好友、发起/接受/拒绝请求、屏蔽等。
- **AS Webhook 接入**：接收 Homeserver 推送的事件 Transaction，支持事务幂等性。
- **内置 Homeserver**：`homeserver` 子包实现 Client-Server API、Federation、E2EE 密钥交换、媒体、推送等能力；兼容范围以已实现接口和实际测试为准。

## 架构 / 结构

`matrix` 包由顶层桥接层和 `homeserver` 子包组成。顶层文件在同一个 `package matrix` 中协作：

| 文件 | 职责 |
|------|------|
| `config.go` | Bridge 配置（`Config`），从环境变量加载，管理 `GlobalConfig` |
| `init.go` | 全局生命周期管理：`Init()` / `Shutdown()` / `GetBridge()` |
| `bridge.go` | `Bridge` 核心服务，协调各组件，处理来自 HS 的 Transaction |
| `as_client.go` | `ASClient`：Application Service API 客户端（向 HS 认证/操作） |
| `client.go` | `Client`：Client-Server API 客户端 |
| `contact_service.go` | `ContactService`：跨平台联系人/好友服务 |
| `user_mapper.go` | `UserMapper`：本地用户 ↔ Matrix 虚拟用户映射 |
| `room_mapper.go` | `RoomMapper`：本地会话 ↔ Matrix 房间映射 |
| `group_bridge.go` | 群组消息桥接（`Bridge.SendGroupMessageToMatrix` / `SyncGroupToMatrix`） |
| `group_mapper.go` | `GroupMapper`：本地群组 ↔ Matrix 房间映射 |
| `message_bridge.go` | `MessageBridge`：消息格式转换与双向同步 |
| `webhook.go` | `WebhookHandler`：接收 HS 推送的 Gin HTTP 处理器 |
| `models.go` | Matrix 事件类型常量、`Event` / `RoomMessageContent` 等数据结构 |
| `homeserver/` | 内置 Matrix Homeserver 实现（见下文） |

### 顶层桥接层组件关系

```
                    ┌─────────────── Gin HTTP ────────────────┐
                    │              WebhookHandler              │
                    └───────────────────┬─────────────────────┘
                                        │ events
                                        ▼
                                     Bridge
              ┌───────────────┬────────┴────────┬─────────────────┐
              ▼               ▼                 ▼                 ▼
         UserMapper      RoomMapper      MessageBridge       ASClient ──► Matrix HS
        (本地↔虚拟用户)  (会话↔房间)    (消息双向转换)        (AS API)
```

### homeserver 子包

`matrix/homeserver` 由 `HomeServer` 结构体聚合多个服务：

```go
type HomeServer struct {
    Config       *HSConfig
    Auth         *AuthService         // 认证
    Room         *RoomService         // 房间
    Event        *EventService        // 事件
    Sync         *SyncService         // /sync 同步
    Account      *AccountService      // 账户
    AppService   *AppServiceService   // 应用服务
    Federation   // ...                // 联邦 (federation.go)
    KeyStore     *KeyStore            // E2EE 密钥
    Push         *PushService         // 推送通知
    Spaces       *SpacesService       // Spaces
    Media        // ...                // 媒体 (media.go)
    Admin        *AdminService        // 管理
    SyncNotifier *SyncNotifier        // 同步通知器
    // ...
}
```

主要能力包括：Client-Server API（`handlers.go`）、Federation 联邦（`federation.go`，端口 8448）、E2EE 端到端加密（`crypto_olm.go`、`e2ee_*.go`）、媒体服务（`media.go`）、状态解析（`state_resolution.go`）、Application Service 管理（`appservice.go`）等。通过 `RegisterRoutes(r)` 注册 Client-Server API 路由，`RegisterFederationRoutes(r)` 注册联邦路由。

## 联邦文件传输

媒体下载使用 Matrix 1.11 的认证媒体协议：客户端通过 `/_matrix/client/v1/media/` 携带令牌下载，服务器通过带联邦签名的 `/_matrix/federation/v1/media/` 交换 multipart 媒体。旧的匿名媒体下载地址不提供现代上传文件，避免绕过认证。

上传保留原始文件名，单文件上限为 50 MiB。加密附件作为原始密文字节传输，由客户端解密；服务器不应把密文当图片处理。远端 multipart 重定向目前不支持，避免未经验证的二次下载。

异步上传的完成与状态查询要求登录，并验证上传所有者。每次上传最多持续 5 分钟；进程中断后的占用租约在 5 分 30 秒后允许所有者重试，旧请求不能覆盖新的成功结果。

## 同步与性能验证

客户端必须保存并原样提交完整的 `next_batch`。设备消息按账号和设备分批保留，下一次提交相应游标时才确认删除；断线重试会重新返回尚未确认的批次。同步 GET 会产生投递和确认等副作用，压测应使用专用账号与设备。

历史事件的可见性依据事件发生时的状态，缺少可靠历史证据时拒绝读取。旧本地事件可以使用可信本地状态快照，但不会因此被伪造成已签名的联邦事件。`m.read.private` 仅用于自己的设备同步与未读位置，不发布给其他账号或服务器。

性能测试覆盖首次/增量同步、历史分页、发送、长轮询、设备消息重试和媒体传输。数据规模、前后结果及适用限制见 [Matrix 性能报告](../docs/matrix-performance.md)，复测命令见 [压测脚本说明](../scripts/benchmark-matrix.md)。

## 配置说明

### 环境变量配置

用户只需配置核心变量即可启用 Bridge：

```bash
# 是否启用 Matrix Bridge
MATRIX_ENABLED=true

# Matrix Homeserver 地址
MATRIX_HOMESERVER_URL=https://matrix.example.com

# Application Service Token（向 HS 认证）
MATRIX_AS_TOKEN=your-as-token

# Homeserver Token（验证来自 HS 的请求）
MATRIX_HS_TOKEN=your-hs-token

# 可选：服务器名称（默认使用系统设置的站点域名）
MATRIX_SERVER_NAME=example.com

# 可选：Bridge 机器人用户名（默认 _bridge）
MATRIX_BRIDGE_USERNAME=_bridge

# 可选：用户/房间命名空间前缀（默认 _c_）
MATRIX_USER_NAMESPACE=_c_
MATRIX_ROOM_NAMESPACE=_c_
```

> `Config.IsEnabled()` 要求 `Enabled=true` 且 `HomeserverURL`、`ServerName`、`ASToken`、`HSToken` 均非空。

### Homeserver 子包配置

`HSConfig`（`homeserver/HSConfig`）默认启用，`ServerName` 优先级：`SERVER_NAME` 环境变量 > 系统设置域名 > `localhost`；联邦端口默认 `8448`。

## 用法示例

### 基本用法：初始化与生命周期

```go
import "github.com/kasuganosora/community/matrix"

// 在应用启动时调用（内部使用 sync.Once，线程安全）
if err := matrix.Init(); err != nil {
    log.Fatal("Failed to init Matrix Bridge: %v", err)
}

// 检查运行状态
if matrix.IsRunning() {
    bridge := matrix.GetBridge()
    _ = bridge
}

// 应用关闭时
matrix.Shutdown()
```

> 当配置未启用时，`Init()` 会跳过初始化并安全返回。

### 发送本地消息到 Matrix

```go
bridge := matrix.GetBridge()
if bridge == nil || !bridge.IsStarted() {
    return
}

// 发送私信（返回 Matrix event_id）
eventID, err := bridge.SendToMatrix(conversationID, senderAccountID, "Hello Matrix!")
if err != nil {
    // 处理错误
}

// 发送群组消息
eventID, err = bridge.SendGroupMessageToMatrix(matrixRoomID, senderAccountID, "Group hello")

// 同步本地群组到 Matrix（创建或获取对应房间）
roomID, err := bridge.SyncGroupToMatrix(groupID)
```

### 注册 AS Webhook 路由

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/kasuganosora/community/matrix"
)

router := gin.Default()
bridge := matrix.GetBridge()

if bridge != nil {
    handler := matrix.NewWebhookHandler(bridge)
    // Matrix Homeserver 通过这些端点推送事件
    router.PUT("/_matrix/app/v1/transactions/:txnId", handler.HandleTransaction)
    router.GET("/_matrix/app/v1/users/:userId", handler.HandleUserQuery)
    router.GET("/_matrix/app/v1/rooms/:roomAlias", handler.HandleRoomQuery)
    router.GET("/_matrix/health", handler.HandleHealthCheck)
}
```

### 使用 ASClient 直接操作 Matrix

```go
// 使用全局配置创建客户端
client := matrix.NewASClient()

// 测试与 Homeserver 的连接
if err := client.PingHS(); err != nil {
    // 处理错误
}

// 确保虚拟用户存在（已存在则跳过）
err := client.EnsureVirtualUser("_c_alice")

// 发送简单文本消息
resp, err := client.SendRoomMessageSimple(roomID, senderID, "Hello!")

// 创建房间
roomID, err := client.CreateRoom(&matrix.CreateRoomConfig{
    Name:   "测试群组",
    Topic:  "群组描述",
    Invite: []string{"@bob:matrix.org"},
})

// 获取用户档案
profile, err := client.GetUserProfile("@user:matrix.org")
```

### 使用自定义配置创建 Bridge

```go
cfg := &matrix.Config{
    Enabled:       true,
    HomeserverURL: "https://matrix.example.com",
    ServerName:    "example.com",
    ASToken:       "your-as-token",
    HSToken:       "your-hs-token",
}

bridge := matrix.NewBridgeWithConfig(cfg)
if err := bridge.Start(); err != nil {
    // 处理错误
}
```

### 联系人服务

```go
client := matrix.NewASClient()
cs := matrix.NewContactService(client)

// 通过 Matrix 用户名查找本地用户（@_c_alice:example.com → alice）
account, err := cs.LookupLocalAccount("@_c_alice:example.com")

// 添加 Matrix 用户为好友
err = cs.AddContact(accountID, "@bob:matrix.org", "Bob")

// 创建好友请求（Matrix 用户请求添加本地用户）
req, err := cs.CreateContactRequest("@bob:matrix.org", toAccountID, "请加我")

// 接受 / 拒绝好友请求
err = cs.AcceptContactRequest(req.ID, toAccountID)
err = cs.RejectContactRequest(req.ID, toAccountID)

// 获取联系人列表 / 待处理请求
contacts, err := cs.GetContacts(accountID)
pending, err := cs.GetPendingContactRequests(accountID)

// 屏蔽 / 检查屏蔽
err = cs.BlockMatrixUser(accountID, "@spammer:matrix.org")
blocked, err := cs.IsBlocked(accountID, "@spammer:matrix.org")
```

### 群组桥接

```go
groupMapper := matrix.NewGroupMapper(matrix.NewASClient(), bridge.GetUserMapper())

// 创建与 Matrix 桥接的群组
group, err := groupMapper.CreateBridgedGroup(ownerID, "我的群组", "描述")

// 将已有群组桥接到 Matrix
err = groupMapper.BridgeExistingGroup(groupID)

// 添加成员（本地用户或 Matrix 用户）
err = groupMapper.AddLocalMemberToGroup(groupID, accountID, models.GroupMemberRoleMember)
err = groupMapper.AddMatrixMemberToGroup(groupID, "@bob:matrix.org", "Bob", models.GroupMemberRoleMember)

// 通过 Matrix 房间 ID 反查群组
group, err = groupMapper.GetGroupByMatrixRoom(roomID)
```

### 内置 Homeserver

```go
import "github.com/kasuganosora/community/matrix/homeserver"

// 初始化 Homeserver（sync.Once 保护，默认启用）
if err := homeserver.InitHomeServer(); err != nil {
    // 处理错误
}

// 获取全局实例
hs := homeserver.GetHomeServer()

// 注册 Client-Server API 路由（在 Gin router 上）
homeserver.RegisterRoutes(router)

// 注册 Federation 联邦路由
homeserver.RegisterFederationRoutes(router)
```

## 核心类型与函数

### 类型

#### `Config`（`config.go`）

Bridge 配置，从环境变量加载：

| 字段 | 类型 | 环境变量 | 默认值 | 说明 |
|------|------|----------|--------|------|
| `Enabled` | `bool` | `MATRIX_ENABLED` | `false` | 是否启用 |
| `HomeserverURL` | `string` | `MATRIX_HOMESERVER_URL` | `""` | Homeserver 地址 |
| `ServerName` | `string` | `MATRIX_SERVER_NAME` | 站点域名 | 服务器名称 |
| `ASToken` | `string` | `MATRIX_AS_TOKEN` | `""` | AS 认证 Token |
| `HSToken` | `string` | `MATRIX_HS_TOKEN` | `""` | 验证 HS 请求的 Token |
| `BridgeUsername` | `string` | `MATRIX_BRIDGE_USERNAME` | `_bridge` | Bridge 机器人用户名 |
| `UserNamespacePrefix` | `string` | `MATRIX_USER_NAMESPACE` | `_c_` | 用户命名空间前缀 |
| `RoomNamespacePrefix` | `string` | `MATRIX_ROOM_NAMESPACE` | `_c_` | 房间命名空间前缀 |

#### `Bridge`（`bridge.go`）

核心协调服务，聚合 `ASClient`、`UserMapper`、`RoomMapper`、`MessageBridge`，管理生命周期（`atomic.Bool` + `sync.Once` 保证线程安全）。

#### `ASClient`（`as_client.go`）

Application Service API 客户端，通过 AS Token 向 Homeserver 认证。

#### `Client`（`client.go`）

Client-Server API 客户端，与 `ASClient` 共享底层 HTTP 请求逻辑（`doMatrixRequest`）。

#### `WebhookHandler`（`webhook.go`）

接收 Homeserver 推送事件的 Gin 处理器，内置事务幂等性缓存（`processedTxns`，1 小时过期，10 分钟清理一次）。

#### 数据结构（`models.go`）

| 类型 | 说明 |
|------|------|
| `Event` | Matrix 事件，提供 `ToRoomMessageContent()` / `ToRoomMemberContent()` / `GetTimestamp()` 方法 |
| `RoomMessageContent` | `m.room.message` 事件内容 |
| `RoomMemberContent` | `m.room.member` 事件内容 |
| `Transaction` | AS Transaction 请求体（`Events []Event`） |
| `UserProfile` | Matrix 用户档案 |
| `SyncResponse` | `/sync` 响应 |
| `RoomCreateRequest` / `RoomCreateResponse` | 创建房间请求/响应 |
| `SendEventResponse` | 发送事件响应 |

#### 常量（`models.go`）

| 类别 | 常量 |
|------|------|
| 事件类型 | `EventTypeRoomMessage`、`EventTypeRoomMember`、`EventTypeRoomCreate` 等 |
| 成员状态 | `MembershipJoin`、`MembershipInvite`、`MembershipLeave`、`MembershipBan`、`MembershipKnock` |
| 消息类型 | `MsgTypeText`、`MsgTypeEmote`、`MsgTypeNotice`、`MsgTypeImage`、`MsgTypeFile` 等 |

### 函数

#### 包级函数（`init.go`）

| 函数 | 说明 |
|------|------|
| `Init() error` | 初始化 Bridge（`sync.Once`），注册 Status 事件监听器，未启用时跳过 |
| `GetBridge() *Bridge` | 获取全局 Bridge 实例（`sync.RWMutex` 保护） |
| `IsEnabled() bool` | 检查配置是否启用 |
| `IsRunning() bool` | 检查 Bridge 是否正在运行 |
| `Shutdown()` | 关闭 Bridge，移除事件监听器 |

#### `Config` 方法

| 方法 | 说明 |
|------|------|
| `IsEnabled() bool` | 检查启用且配置完整 |
| `GetUserNamespaceRegex() *regexp.Regexp` | 获取用户命名空间正则 |
| `GetBridgeUserID() string` | 获取 Bridge 机器人完整 Matrix ID |
| `FormatMatrixUserID(localpart) string` | 格式化 Matrix 用户 ID |

| 包级函数 | 说明 |
|----------|------|
| `LoadConfig()` | 从环境变量加载配置（`sync.Once`） |
| `ParseMatrixUserID(id) (localpart, server string, ok bool)` | 解析 Matrix 用户 ID |
| `ParseMatrixRoomID(id) (localpart, server string, ok bool)` | 解析 Matrix 房间 ID |

#### `Bridge` 方法

| 方法 | 说明 |
|------|------|
| `Start() error` | 启动 Bridge（Ping HS、初始化组件、确保 Bridge 用户存在） |
| `Stop()` | 停止 Bridge（`sync.Once` 关闭 `stopChan`） |
| `IsStarted() bool` | 是否已启动 |
| `HandleTransaction(txnID, events) error` | 处理来自 HS 的 Transaction |
| `SendToMatrix(convID, accountID, content) (string, error)` | 发送私信到 Matrix |
| `SendGroupMessageToMatrix(roomID, accountID, content) (string, error)` | 发送群组消息到 Matrix |
| `SyncGroupToMatrix(groupID) (string, error)` | 同步群组到 Matrix |
| `GetUserMapper() / GetRoomMapper() / GetMessageBridge()` | 获取内部组件 |
| `GetConfig() *Config` / `GetClient() *ASClient` | 获取配置/客户端 |

#### `ASClient` 方法

| 方法 | 说明 |
|------|------|
| `NewASClient() *ASClient` | 使用全局配置创建 |
| `NewASClientWithConfig(cfg) *ASClient` | 使用指定配置创建 |
| `ValidateHSToken(token) bool` | 常量时间比较验证 HS Token |
| `PingHS() error` | 测试与 Homeserver 的连接 |
| `RegisterVirtualUser(localpart) error` | 注册虚拟用户 |
| `EnsureVirtualUser(localpart) error` | 确保虚拟用户存在（已存在返回 nil） |
| `CreateDirectRoom(creator, invitee, isDirect) (*RoomCreateResponse, error)` | 创建私信房间 |
| `SendRoomMessage(roomID, sender, content) (*SendEventResponse, error)` | 发送房间消息 |
| `SendRoomMessageSimple(roomID, sender, body) (*SendEventResponse, error)` | 发送简单文本消息 |
| `InviteToRoom / InviteUser / CreateRoom / JoinRoom / LeaveRoom` | 房间管理 |
| `SetUserProfile / GetUserProfile` | 用户档案 |
| `GetRoomStateEvent / GetRoomMembers / SyncRoomEvents` | 房间状态/成员/同步 |

#### `UserMapper` 方法

| 方法 | 说明 |
|------|------|
| `GetOrCreateMatrixUserByLocal(accountID) (*models.MatrixUser, error)` | 本地用户 → Matrix 虚拟用户 |
| `GetOrCreateLocalUserByMatrix(matrixUserID) (*models.MatrixUser, error)` | Matrix 用户 → 本地虚拟账号 |
| `GetMatrixUserID(accountID) (string, error)` | 获取 Matrix 用户 ID |
| `GetLocalAccountID(matrixUserID) (uint64, error)` | 获取本地账号 ID |
| `GetLocalAccountFromMatrixUser(matrixUserID) (*models.Account, error)` | 获取本地账号 |
| `UpdateUserProfile(matrixUserID, name, avatar) error` | 更新档案 |
| `IsMatrixUser(matrixUserID) bool` | 是否为本系统映射用户 |
| `ClearCache()` | 清除缓存 |

#### `RoomMapper` 方法

| 方法 | 说明 |
|------|------|
| `GetOrCreateRoomByConversation(convID, participants) (*models.MatrixRoom, error)` | 会话 → Matrix 房间 |
| `GetOrCreateConversationByRoom(roomID, participantIDs) (*models.MatrixRoom, error)` | Matrix 房间 → 本地会话 |
| `GetConversationID(roomID) (uint64, error)` | 获取会话 ID |
| `GetMatrixRoomID(convID) (string, error)` | 获取 Matrix 房间 ID |
| `UpdateLastSync(roomID, eventID) error` | 更新最后同步信息 |
| `IsDirectRoom(roomID) (bool, error)` | 是否为私信房间 |
| `GetRoomInfo(roomID) (*RoomInfo, error)` | 获取房间信息 |
| `ClearCache()` | 清除缓存 |

#### `GroupMapper` 方法

| 方法 | 说明 |
|------|------|
| `CreateBridgedGroup(ownerID, name, desc) (*models.Group, error)` | 创建桥接群组 |
| `BridgeExistingGroup(groupID) error` | 桥接已有群组 |
| `AddLocalMemberToGroup / AddMatrixMemberToGroup` | 添加成员 |
| `RemoveMemberFromGroup` | 移除成员 |
| `GetGroupByMatrixRoom(roomID) (*models.Group, error)` | 反查群组 |
| `GetGroupMembers / IsGroupMember` | 成员查询 |

#### `ContactService` 方法

| 方法 | 说明 |
|------|------|
| `LookupLocalAccount(matrixUserID) (*models.Account, error)` | 通过 Matrix ID 查找本地用户 |
| `AddContact / RemoveContact` | 添加/移除联系人 |
| `CreateContactRequest / AcceptContactRequest / RejectContactRequest` | 好友请求 |
| `GetContacts / GetPendingContactRequests` | 查询列表 |
| `BlockMatrixUser / IsBlocked` | 屏蔽/检查屏蔽 |
| `GetLocalUserMatrixID / GetLocalUserMatrixIDByUsername` | 获取本地用户的 Matrix ID |

#### `homeserver` 子包

| 函数/类型 | 说明 |
|-----------|------|
| `InitHomeServer() error` | 初始化 Homeserver（自动迁移数据库、加载联邦密钥、初始化所有服务） |
| `GetHomeServer() *HomeServer` | 获取全局 Homeserver 实例（`sync.Once`） |
| `GetInitError() error` | 获取初始化错误 |
| `IsEnabled() bool` | 是否启用 |
| `RegisterRoutes(r gin.IRouter)` | 注册 Client-Server API 路由 |
| `RegisterFederationRoutes(r gin.IRouter)` | 注册 Federation 联邦路由 |
| `RegisterAppServiceRoutes(r gin.IRouter)` | 注册 Application Service API 路由 |
| `HSConfig` | Homeserver 配置（`Enabled`、`ServerName`、`BaseURL`、`FederationPort` 等） |
| `AppService` / `AppServiceService` | 应用服务管理（注册、查询、事件推送） |

## 依赖关系

### 内部依赖

| 包 | 用途 |
|----|------|
| `db` | 数据库访问（`gorm`），存储映射关系与消息记录 |
| `models` | 数据模型（`Account`、`Status`、`Conversation`、`MatrixUser`、`MatrixRoom` 等） |
| `event` | 事件系统，监听 `status` `after_create` 实现私信自动同步 |
| `log` | 日志 |
| `httpclient` | HTTP 客户端（`ASClient` / `Client` 复用） |
| `systemsettings` | 系统设置（`ServerName` 默认取站点域名） |
| `utils` | 工具函数（`FormatMatrixUserID` / `ParseMatrixUserID`） |
| `config` | 全局配置（Homeserver 子包读取 `EncryptedKey`） |

### 外部依赖

| 包 | 用途 |
|----|------|
| `github.com/gin-gonic/gin` | HTTP 路由（`WebhookHandler`、Homeserver handlers） |
| `github.com/google/uuid` | 生成事务 ID（`generateTxnID`） |
| `github.com/pkg/errors` | 错误包装 |
| `gorm.io/gorm` | ORM |
| `golang.org/x/net/html` | HTML 解析（消息格式转换 `stripHTMLTags`） |

### 数据库表

Bridge 顶层依赖 `models` 包中的映射表（如 `matrix_users`、`matrix_rooms`、`matrix_messages`）；Homeserver 子包通过 `autoMigrate()` 自动创建约 30 张表（`matrix_events`、`matrix_room_states`、`matrix_appservices`、`matrix_federation_keys` 等）。

## 设计说明

- **Token 安全**：`ASClient.ValidateHSToken` 使用 `crypto/subtle.ConstantTimeCompare` 常量时间比较，防止时序攻击；未配置 `HSToken` 时拒绝所有请求。
- **事务幂等性**：`WebhookHandler` 内置 `processedTxns` 缓存，防止 Homeserver 重试导致重复处理；清理周期 10 分钟，条目存活 1 小时。
- **线程安全**：`Config` 加载（`sync.Once`）、`Bridge` 启停（`atomic.Bool` + `sync.Once`）、全局实例（`sync.RWMutex`）均做了并发保护。
- **消息格式转换**：`MessageBridge` 支持 HTML 格式化消息的双向转换（`stripHTMLTags` / `textToHTML`），处理 `emote` 类型与 `org.matrix.custom.html` 格式。
- **命名空间隔离**：通过 `UserNamespacePrefix`（默认 `_c_`）区分本地映射的虚拟用户与真实 Matrix 用户，避免映射循环。
