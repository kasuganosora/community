# activitypub (ActivityPub)

ActivityPub 联邦协议实现包，负责与其他去中心化社交实例之间的消息收发、身份发现与内容同步。该包内置 HTTP Signatures 验签、SSRF 防护、消息投递队列与中继（Relay）支持，是整个社区系统接入 Fediverse（如 Mastodon、Pleroma、Misskey 等）的核心网关。

## 核心功能

- **联邦消息收发**：通过 `Inbox` / `SharedInbox` 接收远程实例的活动（Create、Follow、Like、Announce、Delete、Undo、Move 等），通过 `DeliveryQueue` 异步投递本地活动到远程 inbox。
- **SSRF 防护**：`ssrfSafeHTTPClient` 在拨号层拦截所有指向内网/保留地址（loopback、private、link-local、CGNAT、云元数据地址 169.254.169.254 等）的请求，并通过 `ValidateURI` / `IsSSRFBlocked` 提供前置校验与错误识别。
- **HTTP Signatures**：实现 RFC 草案 [HTTP Signatures](https://datatracker.ietf.org/doc/html/draft-cavage-http-signatures)，支持请求签名（`SignWithRSA`）、验签（`VerifyRequest` / `SignatureVerifier`）、Digest 校验、`(request-target)` 强制签名、Date 时间窗口防重放、公钥 LRU 缓存。
- **WebFinger / NodeInfo 发现**：对外提供 `/.well-known/webfinger`、`/.well-known/host-meta`、`/.well-known/nodeinfo` 端点；对内可通过 `DiscoverActor` 根据 acct（`user@domain`）反向发现远程 Actor。
- **远程资源解析**：`Resolver` 负责将远程 Actor / Note 解析为本地 `models.Account` / `models.Status`，内置 `sync.Map` + TTL 缓存与后台刷新机制。
- **中继（Relay）服务**：`RelayService` 管理与公共中继服务器的订阅关系，自动发送/撤销 Follow 活动，并将公开状态分发到中继 inbox。
- **触达范围计算**：`StatusReachFinder` / `AccountReachFinder` 计算一条状态/账户相关活动应被分发到哪些远程 inbox（粉丝、被提及者、中继，并剔除被屏蔽账户）。
- **系统代理账户**：`ProxyService` 维护一个 `system.proxy` 系统账户，在无本地用户关注某个远程用户时由代理账户代为关注，以保证内容可达性。

## 架构 / 结构

| 源文件 | 职责 |
|--------|------|
| `ssrf.go` | SSRF 防护核心：禁用 CIDR 列表、`ssrfSafeDialContext` 拨号拦截、`ssrfSafeHTTPClient` 共享客户端、`ValidateURI` / `IsSSRFBlocked` 公开 API |
| `client.go` | `Client` —— ActivityPub HTTP 客户端，封装 `Get` / `Post` / `FetchActor` / `FetchObject` / `FetchStatus`，Post 自动附加 HTTP Signature |
| `signature.go` | HTTP Signatures 实现：`SignWithRSA`、`VerifyWithRSA`、`ParsePublicKey`、`SignatureHeader`、`SignatureVerifier`（带公钥 LRU 缓存）、`VerifyActivitySignature` |
| `deliver.go` | `DeliveryQueue` 异步投递队列、`Delivery` / `ProcessedActivity` GORM 模型、`DeliveryStatus` 状态机、指数退避重试 `RetryPolicy` / `GetNextRetryTime` |
| `inbox.go` | `Inbox` / `SharedInbox` Gin handler，按活动类型分发到 `handleCreateActivity`、`handleFollowActivity` 等；活动去重、Content-Type 校验 |
| `outbox.go` | `Outbox` / `Followers` / `Following` / `ActorProfile` Gin handler，对外暴露本地用户的 ActivityPub 端点 |
| `webfinger.go` | `WebFinger` / `HostMeta` / `NodeInfo` 端点、`DiscoverActor` 反向发现、`ActivityPubError` 错误类型 |
| `resolver.go` | `Resolver` 远程资源解析与本地化（Account / Status），带 TTL 缓存；`GetOrCreateRemoteAccount` / `ResolveRemoteStatus` 便捷函数 |
| `relay.go` | `RelayService` 中继管理：创建/启用/禁用/删除中继，`RelaySerializer` 序列化 |
| `proxy.go` | `ProxyService` 系统代理账户，`GetOrCreateProxyAccount` / `EnsureProxyFollow` / `CleanupProxyFollow` |
| `status_reach.go` | `StatusReachFinder` / `AccountReachFinder` 计算消息触达 inbox 列表，`DistributeToRelays` / `GetEnabledRelayInboxes` |
| `mention.go` | `ParseMentions` 从 HTML 链接、ActivityPub tag 数组、纯文本中提取 `Mention` |
| `featured.go` | `Featured` handler，对外暴露用户的置顶状态集合 |
| `models.go` | ActivityPub 数据模型：`Actor`、`Note`、`Activity` 及其子类型、`Collection`、`WebFingerResponse` 等 |

## 用法示例

### 创建客户端并获取远程 Actor

```go
import (
    "context"
    "crypto/rsa"
    "github.com/kasuganosora/community/activitypub"
)

func fetchActor(privateKey *rsa.PrivateKey) {
    // actorID 是本实例发送方的 Actor URI，用于 HTTP Signature keyId
    client := activitypub.NewClient(privateKey, "https://my-instance.com/users/alice")

    actor, err := client.FetchActor(context.Background(), "https://remote.example/users/bob")
    if err != nil {
        // err 可能是 SSRF 阻断、网络错误或解析错误
        if activitypub.IsSSRFBlocked(err) {
            // 远程主机被 SSRF 策略拦截
        }
        return
    }

    fmt.Println(actor.PreferredUsername, actor.Inbox)
}
```

### 投递活动到远程 inbox

```go
// 1. 创建投递队列（workers 为后台 worker 数量）
queue := activitypub.NewDeliveryQueue(privateKey, "https://my-instance.com/users/alice", 4)
queue.Start()
defer queue.Stop()

// 2. 构造一个 Create 活动
activity := map[string]any{
    "@context": "https://www.w3.org/ns/activitystreams",
    "id":       "https://my-instance.com/activities/create/1",
    "type":     "Create",
    "actor":    "https://my-instance.com/users/alice",
    "to":       []string{"https://www.w3.org/ns/activitystreams#Public"},
    "object": map[string]any{
        "id":           "https://my-instance.com/users/alice/statuses/1",
        "type":         "Note",
        "attributedTo": "https://my-instance.com/users/alice",
        "content":      "Hello Fediverse!",
    },
}

// 3. 入队（bto / bcc 等隐私字段会被自动剥离）
err := queue.Enqueue(
    "https://my-instance.com/activities/create/1",
    "https://remote.example/users/bob/inbox",
    activity,
)
```

### 验证收到的活动签名

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/kasuganosora/community/activitypub"
)

func inboxHandler(c *gin.Context) {
    body, _ := io.ReadAll(c.Request.Body)

    // VerifyActivitySignature 会自动获取并缓存发送方公钥，
    // 校验 algorithm、(request-target)、Date 时间窗口、Digest，并返回 actor URI
    actorURI, err := activitypub.VerifyActivitySignature(c, body)
    if err != nil {
        c.AbortWithStatusJSON(401, gin.H{"error": "signature verification failed"})
        return
    }
    // actorURI 即为可信的发送方 Actor URI
}
```

### 通过 WebFinger 发现远程用户

```go
// acct 格式：username@domain 或 acct:username@domain
actor, err := activitypub.DiscoverActor("bob@remote.example")
if err != nil {
    if errors.Is(err, activitypub.ErrActorNotFound) {
        // 未找到该用户
    }
    return
}
fmt.Println(actor.ID)
```

### 解析远程状态并本地化

```go
// 便捷函数：内部使用单例 Resolver（带 30 分钟 TTL 缓存）
status, err := activitypub.ResolveRemoteStatus("https://remote.example/users/bob/statuses/42")
if err != nil {
    return
}

// 或自行创建 Resolver（可注入私钥与 actorID）
resolver := activitypub.NewResolver(privateKey, "https://my-instance.com/actor")
account, err := resolver.ResolveAccount(ctx, "bob@remote.example") // 支持 acct 或 URI
followers, err := resolver.FetchRemoteFollowers(ctx, account, 100)
```

### 计算状态的触达范围

```go
// unsafe=false 表示剔除被屏蔽账户
finder := activitypub.NewStatusReachFinder(status, false)
inboxes, err := finder.GetInboxes()
// inboxes 包含粉丝、被提及者、以及（公开状态下）中继的 inbox URL
```

### 管理中继

```go
relaySvc := activitypub.NewRelayService()

// 订阅一个中继
relay, err := relaySvc.CreateRelay("https://relay.example/inbox")
if err != nil {
    return
}
// 发送 Follow 活动到中继服务器
if err := relaySvc.EnableRelay(relay.ID); err != nil {
    return
}

// 列出所有中继（带状态）
relays, _ := relaySvc.GetAllRelays()
serialized := activitypub.SerializeRelays(relays)
```

## 配置说明

本包通过 `systemsettings` 读取配置，无需额外环境变量，主要依赖以下系统设置项：

| 配置键（systemsettings） | 说明 | 默认值 |
|--------------------------|------|--------|
| `KeyDomain` | 当前实例域名，用于构造 Actor URI、WebFinger 响应等 | `localhost` |
| `KeyMediaAttachmentsBaseURL` | 媒体附件的基础 URL，用于构造 Note 中的 `attachment.url` | 与 `KeyDomain` 一致 |

示例：

```sql
INSERT INTO system_settings (`key`, `val`) VALUES ('config.domain', 'social.example.com');
```

**内置常量**（定义于源码，不可通过配置修改）：

| 常量 | 值 | 说明 |
|------|-----|------|
| `MaxRequestBodySize` | `1024 * 1024`（1 MB） | inbox 请求体最大大小 |
| `maxResponseSize` | `10 * 1024 * 1024`（10 MB） | 单次联邦请求响应体最大大小 |
| `defaultCacheTTL` | `30 * time.Minute` | Resolver 缓存 TTL |
| `maxCacheEntries` | `10000` | Resolver 最大缓存条目 |
| `RetryPolicy` | `[5m, 30m, 2h, 12h, 24h]` | 投递失败重试间隔 |

**SSRF 禁用网段**（`ssrf.go`）：默认拦截 `0.0.0.0/8`、`10.0.0.0/8`、`127.0.0.0/8`、`169.254.0.0/16`（含云元数据地址）、`172.16.0.0/12`、`192.168.0.0/16`、`::1/128`、`fe80::/10` 等 IPv4/IPv6 保留网段；重定向最多跟随 5 跳，且禁止跳转到非 HTTP(S) scheme。

## 依赖关系

### 内部包

| 包 | 用途 |
|----|------|
| `github.com/kasuganosora/community/db` | GORM 数据库访问（`db.GetDB()`） |
| `github.com/kasuganosora/community/models` | 数据模型：`Account`、`Status`、`Follow`、`Relay`、`Block` 等 |
| `github.com/kasuganosora/community/systemsettings` | 读取实例域名、媒体基础 URL 等配置 |
| `github.com/kasuganosora/community/utils` | URI 解析（`ExtractDomainFromURI`、`ParseRFC3339` 等） |
| `github.com/kasuganosora/community/serializer` | 用户自定义字段序列化 |
| `github.com/kasuganosora/community/log` | 日志输出 |

### 外部库

| 库 | 用途 |
|----|------|
| `github.com/gin-gonic/gin` | HTTP handler（WebFinger、Inbox、Outbox 等端点） |
| `gorm.io/gorm` | ORM |
| `github.com/hashicorp/golang-lru/v2/expirable` | 公钥的 LRU + TTL 缓存 |

### 数据库表

本包定义并依赖以下数据库表（通过 GORM AutoMigrate 自动建表）：

- `activitypub_deliveries` —— 待投递的消息队列（`Delivery`）
- `activitypub_processed_activities` —— 已处理活动 ID，用于 inbox 去重（`ProcessedActivity`）
