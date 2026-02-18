# ActivityPub 联邦功能增强规划

**创建时间**: 2025-02-17
**预估时间**: 4-6 周
**优先级**: 🟢 低
**状态**: 📋 待实现

---

## 📋 概述

ActivityPub 是 Mastodon 实现联邦通信的核心协议。本规划旨在增强联邦功能，使本实例能够与其他 Mastodon 实例和其他 ActivityPub 兼容平台（如 Pleroma、Pixelfed、PeerTube 等）进行完整交互。

---

## 🏗️ 架构设计

### 目录结构

```
activitypub/
├── client.go           # ActivityPub HTTP 客户端
├── server.go           # ActivityPub 服务端处理
├── inbox.go            # 收件箱处理
├── outbox.go           # 发件箱处理
├── signature.go        # HTTP 签名
├── deliver.go          # 消息投递
├── resolver.go         # 远程资源解析
├── models.go           # ActivityPub 数据模型
├── activities/         # 活动类型处理
│   ├── create.go       # Create 活动
│   ├── update.go       # Update 活动
│   ├── delete.go       # Delete 活动
│   ├── follow.go       # Follow 活动
│   ├── accept.go       # Accept 活动
│   ├── reject.go       # Reject 活动
│   ├── announce.go     # Announce (转发)
│   ├── like.go         # Like (收藏)
│   ├── undo.go         # Undo 活动
│   └── move.go         # Move 活动
├── actors/             # Actor 处理
│   ├── person.go       # Person Actor
│   ├── service.go      # Service Actor
│   └── group.go        # Group Actor
├── objects/            # 对象处理
│   ├── note.go         # Note 对象 (嘟文)
│   ├── image.go        # Image 对象
│   ├── video.go        # Video 对象
│   └── collection.go   # Collection 对象
└── queue/              # 消息队列
    ├── deliver_queue.go
    └── retry.go
```

---

## ✅ 任务清单

### Phase 1: 核心基础设施 (1 周)

#### 1.1 HTTP 签名验证
- [ ] 实现 HTTP Signatures 规范
- [ ] 公钥获取和缓存
- [ ] 签名验证中间件
- [ ] 签名生成（发送请求时）

#### 1.2 ActivityPub 数据模型
- [ ] Actor 模型 (Person, Service, Group)
- [ ] Object 模型 (Note, Image, Video, etc.)
- [ ] Activity 模型 (Create, Update, Delete, etc.)
- [ ] Collection 模型 (OrderedCollection, Collection)

#### 1.3 WebFinger 支持
- [ ] 实现 `/.well-known/webfinger` 端点
- [ ] 支持 `acct:` 资源查询
- [ ] 返回 ActivityPub Actor 链接

---

### Phase 2: Inbox 处理 (1-2 周)

#### 2.1 Actor Inbox
- [ ] 实现 `/users/:username/inbox` 端点
- [ ] 签名验证
- [ ] 活动类型路由

#### 2.2 活动处理器
| 活动类型 | 优先级 | 说明 |
|---------|--------|------|
| Create | 🔴 高 | 接收远程嘟文 |
| Update | 🔴 高 | 更新远程嘟文 |
| Delete | 🔴 高 | 删除远程嘟文 |
| Follow | 🔴 高 | 关注请求 |
| Accept | 🔴 高 | 接受关注 |
| Reject | 🟡 中 | 拒绝关注 |
| Announce | 🔴 高 | 转发嘟文 |
| Like | 🔴 高 | 收藏嘟文 |
| Undo | 🔴 高 | 撤销操作 |
| Block | 🟡 中 | 屏蔽用户 |
| Flag | 🟢 低 | 举报用户 |
| Move | 🟢 低 | 账户迁移 |
| Add | 🟢 低 | 添加到集合 |
| Remove | 🟢 低 | 从集合移除 |

---

### Phase 3: Outbox 处理 (1 周)

#### 3.1 Actor Outbox
- [ ] 实现 `/users/:username/outbox` 端点
- [ ] 返回用户活动流
- [ ] 支持分页

#### 3.2 活动发布
- [ ] 发布嘟文 (Create Note)
- [ ] 更新嘟文 (Update Note)
- [ ] 删除嘟文 (Delete Note)
- [ ] 关注用户 (Follow)
- [ ] 转发嘟文 (Announce)
- [ ] 收藏嘟文 (Like)

---

### Phase 4: 消息投递 (1 周)

#### 4.1 投递队列
- [ ] 基于 Redis 的投递队列
- [ ] 批量投递优化
- [ ] 失败重试机制
- [ ] 死信队列处理

#### 4.2 投递策略
- [ ] 直接投递给 followers
- [ ] 公开消息投递到 sharedInbox
- [ ] 私密消息单独投递

#### 4.3 错误处理
```go
type DeliveryError struct {
    StatusCode int
    Retries    int
    LastError  error
    NextRetry  time.Time
}

// 重试策略
var RetryPolicy = []time.Duration{
    5 * time.Minute,   // 第1次重试
    30 * time.Minute,  // 第2次重试
    2 * time.Hour,     // 第3次重试
    12 * time.Hour,    // 第4次重试
    24 * time.Hour,    // 第5次重试
}
```

---

### Phase 5: 远程资源解析 (1 周)

#### 5.1 远程账户解析
- [ ] 通过 WebFinger 发现
- [ ] 通过 ActivityPub Actor URL 解析
- [ ] 缓存远程账户信息

#### 5.2 远程嘟文解析
- [ ] 按需获取远程嘟文
- [ ] 获取嘟文回复
- [ ] 获取嘟文上下文

#### 5.3 媒体附件处理
- [ ] 下载远程媒体
- [ ] 生成缩略图
- [ ] 缓存媒体文件

---

### Phase 6: 特殊功能 (可选)

#### 6.1 投票联邦
- [ ] Question 对象支持
- [ ] 投票结果同步
- [ ] 投票过期处理

#### 6.2 定时嘟文联邦
- [ ] scheduledAt 属性同步
- [ ] 跨实例定时发布

#### 6.3 编辑嘟文联邦
- [ ] 历史版本同步
- [ ] updated 字段处理

#### 6.4 表情反应联邦
- [ ] EmojiReaction 扩展
- [ ] 与 Pleroma 兼容

---

## 📊 API 端点规划

### 服务端端点 (接收)

| 端点 | 方法 | 说明 |
|------|------|------|
| `/.well-known/webfinger` | GET | WebFinger 发现 |
| `/.well-known/nodeinfo` | GET | NodeInfo |
| `/users/:username` | GET | Actor Profile |
| `/users/:username/inbox` | POST | 接收活动 |
| `/users/:username/outbox` | GET | 用户活动流 |
| `/users/:username/followers` | GET | 关注者集合 |
| `/users/:username/following` | GET | 关注集合 |
| `/users/:username/collections/featured` | GET | 置顶嘟文 |

### 客户端请求 (发送)

| 功能 | 说明 |
|------|------|
| 发现远程用户 | WebFinger 查询 |
| 获取远程 Actor | HTTP GET + Accept header |
| 投递活动 | HTTP POST + 签名 |
| 获取远程嘟文 | HTTP GET |

---

## 🔧 数据库扩展

### 新增表

```sql
-- 远程账户缓存
CREATE TABLE remote_accounts (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    uri VARCHAR(255) NOT NULL UNIQUE,
    domain VARCHAR(255) NOT NULL,
    inbox_url VARCHAR(255),
    outbox_url VARCHAR(255),
    followers_url VARCHAR(255),
    shared_inbox_url VARCHAR(255),
    public_key TEXT,
    public_key_updated_at TIMESTAMP,
    last_fetched_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_domain (domain),
    INDEX idx_last_fetched (last_fetched_at)
);

-- 投递队列
CREATE TABLE delivery_queue (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    activity_id VARCHAR(255) NOT NULL,
    target_inbox VARCHAR(255) NOT NULL,
    payload JSON NOT NULL,
    status ENUM('pending', 'processing', 'delivered', 'failed') DEFAULT 'pending',
    retries INT DEFAULT 0,
    next_retry_at TIMESTAMP,
    last_error TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_status (status),
    INDEX idx_next_retry (next_retry_at)
);

-- 联邦统计
CREATE TABLE federation_stats (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    domain VARCHAR(255) NOT NULL UNIQUE,
    users_count INT DEFAULT 0,
    statuses_count INT DEFAULT 0,
    last_status_at TIMESTAMP,
    first_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);
```

---

## 🔐 安全考虑

### 1. 签名验证
- 所有接收的活动必须验证签名
- 检查签名时间戳，防止重放攻击
- 缓存公钥，减少远程获取

### 2. 权限检查
- 验证活动发起者权限
- 检查是否被屏蔽/静音
- 验证私有嘟文的收件人

### 3. 速率限制
- 按域名限制请求频率
- 防止 DoS 攻击
- 检测异常活动模式

### 4. 内容过滤
- 应用用户定义的过滤器
- 过滤敏感内容
- 媒体扫描

---

## 📈 性能优化

### 1. 缓存策略
```go
type CacheConfig struct {
    // 远程账户缓存 TTL
    RemoteAccountTTL time.Duration `default:"24h"`
    
    // 公钥缓存 TTL
    PublicKeyTTL time.Duration `default:"7d"`
    
    // WebFinger 缓存 TTL
    WebFingerTTL time.Duration `default:"24h"`
    
    // 远程嘟文缓存 TTL
    RemoteStatusTTL time.Duration `default:"1h"`
}
```

### 2. 批量处理
- 批量投递到共享收件箱
- 批量获取远程嘟文
- 批量更新统计

### 3. 并发控制
- 投递工作池
- 限制并发连接数
- 优先级队列

---

## 🧪 测试计划

### 单元测试
- [ ] HTTP 签名生成/验证
- [ ] 活动序列化/反序列化
- [ ] WebFinger 解析
- [ ] 远程资源解析

### 集成测试
- [ ] 与 Mastodon 实例交互
- [ ] 与 Pleroma 实例交互
- [ ] 与 Pixelfed 实例交互

### 联邦测试套件
- [ ] Mastodon Federation Tester
- [ ] ActivityPub 测试用例

---

## 📅 时间线

| 阶段 | 时间 | 依赖 |
|------|------|------|
| Phase 1: 基础设施 | 第 1 周 | - |
| Phase 2: Inbox 处理 | 第 2-3 周 | Phase 1 |
| Phase 3: Outbox 处理 | 第 4 周 | Phase 1 |
| Phase 4: 消息投递 | 第 5 周 | Phase 2, 3 |
| Phase 5: 远程解析 | 第 6 周 | Phase 1 |
| Phase 6: 特殊功能 | 可选 | Phase 1-5 |

---

## 📚 参考资料

1. [ActivityPub W3C 规范](https://www.w3.org/TR/activitypub/)
2. [Activity Streams 2.0](https://www.w3.org/TR/activitystreams-core/)
3. [HTTP Signatures](https://tools.ietf.org/html/draft-cavage-http-signatures-12)
4. [WebFinger](https://tools.ietf.org/html/rfc7033)
5. [Mastodon ActivityPub 实现](https://docs.joinmastodon.org/spec/activitypub/)
6. [Go-Fed 库](https://github.com/go-fed/activity)

---

**更新时间**: 2025-02-17
