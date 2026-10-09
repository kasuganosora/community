# activitypub (ActivityPub)

ActivityPub 联邦协议实现包，负责与其他去中心化社交实例之间的消息收发、身份发现与内容同步。该包内置 HTTP Signatures 验签、SSRF 防护、消息投递队列与中继（Relay）支持，是整个社区系统接入 Fediverse（如 Mastodon、Pleroma、Misskey 等）的核心网关。

## 核心功能

- **联邦消息收发**：通过 `Inbox` / `SharedInbox` 接收远程实例的活动（Create、Follow、Like、Announce、Delete、Undo、Move 等），通过 `DeliveryQueue` 异步投递本地活动到远程 inbox。
- **SSRF 防护**：`ssrfSafeHTTPClient` 在拨号层拦截所有指向内网/保留地址（loopback、private、link-local、CGNAT、云元数据地址 169.254.169.254 等）的请求，并通过 `ValidateURI` / `IsSSRFBlocked` 提供前置校验与错误识别。
- **HTTP Signatures**：实现 RFC 草案 [HTTP Signatures](https://datatracker.ietf.org/doc/html/draft-cavage-http-signatures)，支持请求签名（`SignWithRSA`）、验签（`VerifyRequest` / `SignatureVerifier`）、Digest 校验、`(request-target)` 强制签名、Date 时间窗口防重放、公钥 LRU 缓存。
- **WebFinger / NodeInfo 发现**：对外提供 `/.well-known/webfinger`、`/.well-known/host-meta`、`/.well-known/nodeinfo` 端点，以及 `/nodeinfo/2.1`、`/nodeinfo/2.0` 文档（带 schema profile 的 Content-Type，用户数/活跃数/本地帖子数缓存 10 分钟，`openRegistrations` 取自站点设置）；对内可通过 `DiscoverActor` 根据 acct（`user@domain`）反向发现远程 Actor。
- **远程资源解析**：`Resolver` 负责将远程 Actor / Note 解析为本地 `models.Account` / `models.Status`，内置 `sync.Map` + TTL 缓存与后台刷新机制。
- **中继（Relay）服务**：`RelayService` 管理与公共中继服务器的订阅关系，自动发送/撤销 Follow 活动，并将公开状态分发到中继 inbox。
- **触达范围计算**：`StatusReachFinder` / `AccountReachFinder` 计算一条状态/账户相关活动应被分发到哪些远程 inbox（粉丝、被提及者、中继，并剔除被屏蔽账户）。
- **系统代理账户**：`ProxyService` 维护一个 `system.proxy` 系统账户，在无本地用户关注某个远程用户时由代理账户代为关注，以保证内容可达性。

## 自定义表情与回应

`emoji.go` 解析和生成 `Emoji` tag（名称、图片及 Misskey 的 `_misskey_license.freeText`），以实例域名和短代码共同标识远端表情。只有实际用于正文、内容警告或资料的表情才关联到对象；禁用或仅限本站使用的表情不会作为本站 Emoji tag 对外发布。远端资源导入使用原有的 TLS、SSRF 与重定向验证，图片限制见 [emojiasset](../emojiasset/README.md)。

`reactions.go` 使用 Misskey 的 `Like.content`、`_misskey_reaction` 和 `Emoji` tag 交换回应；`Undo` 必须匹配原活动身份与作者。回应独立于普通 Like，每位用户每帖一种，替换与撤回保持幂等。数据库变更与持久发件队列同事务提交，投递前复核帖子可见性和屏蔽关系。

`profile_update.go` 在资料修改时向已接受的远端关注者投递 Person `Update`，保留事务内快照，支持明确清空昵称、简介和表情。入站更新区分字段省略与明确清空，并在事务提交后清理资料缓存。帖子仍使用删除并重发，不支持原地编辑。

## 附件与故障恢复

入站帖子和远端解析会保存图片、视频、音频及支持的普通文件附件元数据，保留替代文本与尺寸；未缓存的远端附件直接使用原站 URL。导入只解析受限数量的元数据并验证 URL，不在收件事务内下载大文件。帖子及私信的可见性检查仍适用；附件 URL 的访问策略由媒体所在实例决定。

回应的原始 Like 身份按已验证的账号分别持久保存。重复 Like、旧活动重放、先到的 Undo 和事务回滚不会重新生成已撤销回应或多余通知。发件队列展开和实际发送前都复核回应是否仍有效，丢弃已经替换或撤回的待发 Like；Undo 继续清理曾经发送的副本。

投递采用至少一次语义，重试保留同一活动 ID。租约和退避时间持久保存，重启后继续处理；等待重试的实例或作者不会占满批次并阻塞无关队列。远端返回成功仅表示接受请求，不能保证其异步处理顺序；没有时间戳或因果序号的两个全新远端 Like 也无法可靠推断新旧。

## 架构 / 结构

| 源文件 | 职责 |
|--------|------|
| `ssrf.go` | SSRF 防护核心：禁用 CIDR 列表、`ssrfSafeDialContext` 拨号拦截、`ssrfSafeHTTPClient` 共享客户端、`ValidateURI` / `IsSSRFBlocked` 公开 API |
| `client.go` | `Client` —— ActivityPub HTTP 客户端，封装 `Get` / `Post` / `FetchActor` / `FetchObject` / `FetchStatus`，Post 自动附加 HTTP Signature |
| `signature.go` | HTTP Signatures 实现：`SignWithRSA`、`VerifyWithRSA`、`ParsePublicKey`、`SignatureHeader`、`SignatureVerifier`（带公钥 LRU 缓存）、`VerifyActivitySignature` |
| `deliver.go` | `DeliveryQueue` 异步投递队列、`Delivery` / `ProcessedActivity` GORM 模型、`DeliveryStatus` 状态机、指数退避重试 `RetryPolicy` / `GetNextRetryTime` |
| `inbox.go` | `Inbox` / `SharedInbox` Gin handler，按活动类型分发到 `handleCreateActivity`、`handleFollowActivity` 等；活动去重、Content-Type 校验 |
| `outbox.go` | `Outbox` / `Followers` / `Following` / `ActorProfile` Gin handler，对外暴露本地用户的 ActivityPub 端点 |
| `nodeinfo.go` | `NodeInfo` 发现文档与 `NodeInfo20` / `NodeInfo21` |
| `webfinger.go` | `WebFinger` / `HostMeta` 端点、`DiscoverActor` 反向发现、`ActivityPubError` 错误类型 |
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

### 隔离联邦实验网

默认继续拒绝所有内网地址。仅为隔离的互操作实验，可给独立进程显式设置
`ACTIVITYPUB_LAB_PEERS=misskey.federation.test=172.30.50.10,synapse.federation.test=172.30.50.10`。
配置仅接受精确 `.test` 主机名到单个 RFC1918 IPv4 的映射，不接受通配符、CIDR、
回环或链路本地地址；格式错误时失败关闭。请求必须为 HTTPS，端口只能是 443 或 8448。
每次新连接解析到的全部地址都必须与固定地址一致，之后直接拨号该 IP，防止 DNS 重绑定。
重定向同样校验；其他主机与 IP 字面地址仍执行默认 SSRF 禁止规则。
TLS 验证不会关闭：给实验进程用 `SSL_CERT_FILE` 挂载可信实验 CA，并为各主机签发匹配 SAN 的证书。
此配置不应注入日常开发或生产进程；实验服务、数据库、Redis 与网络应独立运行。
