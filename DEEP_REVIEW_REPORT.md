# Community 项目深度 Code Review 报告

> 审查日期: 2026-04-15
> 审查范围: 全项目 Go 后端 (含 Matrix Homeserver)
> 审查方法: 5 路并行子代理深入源码验证，交叉确认每个问题

---

## 🔴 Critical (必须立即修复)

### C1. Admin 授权绕过 — 任何普通用户可获取完整管理员权限

**文件:** `middleware/check_scope_middleware.go:26-28`

```go
// 如果是网站登陆的token 则不需要检查scope
if tokenData.AuthType == models.AuthTokenTypeWeb {
    c.Next()  // ← 跳过所有 scope 检查
    return
}
```

**根因:** Web 登录 token (`AuthTokenTypeWeb = 0`) 在 scope 中间件中被无条件放行。所有 admin 路由的权限守卫仅依赖此 scope 检查，且 **没有任何 admin handler 独立验证 `user.IsAdmin`**。

**攻击路径:**
1. 注册普通用户 → 登录获得 web token
2. 用该 token 调用 `POST /api/v1/admin/accounts/:id/action` → 暂停管理员
3. 调用 `DELETE /api/v1/admin/statuses/:id` → 删除任意帖子
4. 调用 `POST /api/v1/admin/matrix/migrate` → 执行数据库迁移

**修复方案:**
```go
if tokenData.AuthType == models.AuthTokenTypeWeb {
    for _, scope := range scopes {
        if scope.Type == models.AppScopeTypeAdmin {
            user := c.MustGet("user").(*models.User)
            if !user.IsAdmin {
                returnUnauthorized(c)
                return
            }
        }
    }
    c.Next()
    return
}
```

---

### C2. ActivityPub Inbox — 无重放攻击防护

**文件:** `activitypub/signature.go:212-252`

`VerifyRequestWithKeyID` 验证了签名，但 **从不检查 Date header 的新鲜度**。签名请求可以被无限重放。

```go
func (v *SignatureVerifier) VerifyRequestWithKeyID(req *http.Request, body []byte) error {
    // ... 验证签名 ...
    return nil  // ← 没有 Date 时间窗口检查
}
```

**修复方案:**
```go
dateStr := req.Header.Get("Date")
if dateStr != "" {
    date, err := time.Parse(time.RFC1123, dateStr)
    if err != nil || time.Since(date) > 5*time.Minute || time.Until(date) > 5*time.Minute {
        return fmt.Errorf("request date too far from current time")
    }
}
```

---

### C3. ActivityPub Inbox — POST Body 完整性未强制验证

**文件:** `activitypub/signature.go:229-234`

Digest header 是可选的。攻击者可以只签名 `(request-target) host date`，然后自由篡改 body：

```go
if digest := req.Header.Get("Digest"); digest != "" && body != nil {
    // 可选检查 — 如果省略 Digest header，body 完全未验证
}
```

**修复方案:** 对有 body 的 POST 请求，强制要求 Digest header 并验证 digest 在签名 headers 列表中。

---

### C4. Media Upload — UploadAsync 完全跳过文件验证

**文件:** `controllers/api/media/upload.go:206-252, 255-335`

`Upload()` (同步) 会调用 `isAllowedContentType()` 和 `isValidFileSize()`。
`UploadAsync()` 和其 goroutine `processUploadAsync()` **完全不调用任何验证**：

```go
func UploadAsync(c *gin.Context) {
    // ... 绑定请求 ...
    // ❌ 没有 isAllowedContentType()
    // ❌ 没有 isValidFileSize()
    go processUploadAsync(...)  // 直接写入磁盘
}

func processUploadAsync(...) {
    ext := filepath.Ext(fileHeader.Filename)  // ❌ 无扩展名白名单
    // ... 直接 io.Copy 到磁盘 ...
}
```

**影响:** 可上传任意类型和大小的文件。

**修复方案:** 在 `processUploadAsync` 开头添加与 `Upload` 相同的类型/大小验证，或提取为共用函数。

---

### C5. Media Upload — 全链路无 Magic Bytes 验证

**所有上传路径** (`upload.go`, `service.go`, `matrix/homeserver/media.go`) 仅依赖客户端提供的 `Content-Type` header。

虽然 `DetectImageType()` 存在于 `processor.go:443`，`attachment/save.go` 使用了 `filetype.Match()`，但这些 **均不在媒体上传路径中被调用**。

**修复方案:** 在保存文件前读取前 512 字节，用 `http.DetectContentType()` 验证是否与声明的 Content-Type 一致。

---

### C6. WebSocket Origin Check 完全禁用

**文件:** `controllers/api/streaming/common.go:17-21`

```go
var upgrade = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        return true  // ← 接受任何来源
    },
}
```

**影响:** 恶意网站可静默连接受害者浏览器的 WebSocket，读取实时消息流。

**修复方案:**
```go
CheckOrigin: func(r *http.Request) bool {
    origin := r.Header.Get("Origin")
    return origin == "" || origin == "https://"+r.Host
},
```

---

### C7. WebSocket Subscribe 逻辑反转 — Subscribe 实际执行 Unsubscribe

**文件:** `controllers/api/streaming/streaming.go:146-163`

```go
func subscribe(pub *redis.PubSub, ...) (ret *redis.PubSub, err error) {
    if pub != nil {
        err = pub.Unsubscribe(context.Background(), channelName)  // ← 退订而非订阅！
        ret = pub
        return
    }
    ret = db.GetRedis().Subscribe(context.Background(), channelName)  // 仅首次调用
    return
}
```

**影响:** 用户每次发送 subscribe 消息，实际上是退订了之前的频道。多流订阅（user + notification）完全不可用。

**修复方案:** 将 `Unsubscribe` 改为 `Subscribe`。

---

### C8. Public Timeline Rebuild 清理无效 — 双重前缀 Bug

**文件:** `feed/public/public_timeline.go:87` + `feed/service.go:236-248`

```go
// public_timeline.go:87
feed.CleanFeeds(allPublicTimeline, []string{"feed:public:all"})

// service.go:240
timelineKeyFormat := timeline.GetKeyFormat()  // "feed:public:%s"
timelineKey := fmt.Sprintf(timelineKeyFormat, "feed:public:all")
// 结果: "feed:public:feed:public:all" ← 键不存在！
```

**影响:** Rebuild 时旧数据永远不会被清理，导致时间线出现重复/幽灵帖子。

**修复:** `[]string{"feed:public:all"}` → `[]string{allPublicTimeline.GetTimelineId()}`

---

### C9. Tag Timeline 实时推送完全失效 — pushAllChannel 缺少 Tag 字段

**文件:** `feed/tag/tag_timeline.go:186-193`

```go
func pushAllChannel(status *models.Status, method string) {
    timelines := []*TagTimeline{
        {},              // Tag: "" → Redis key: "feed:hashtag:"
        {OnlyMedia: true}, // Tag: "" → Redis key: "feed:hashtag:media"
        ...
    }
```

所有 `TagTimeline` 的 `Tag` 字段为空字符串。`GetTimelineId()` 返回 `""`，产生的 Redis key 为 `feed:hashtag:` — 没有任何人订阅这个频道。

**影响:** 标签实时推送完全失效。客户端订阅 `#golang` 流永远不会收到更新。

**修复:** 函数签名增加 `tagName` 参数，每个 `TagTimeline` 都设置 `Tag: tagName`。

---

## 🟠 High (应该尽快修复)

### H1. ActivityPub — Actor 字段未与签名验证者比对

**文件:** `activitypub/inbox.go:66-96`, `activitypub/signature.go:344-346`

签名验证返回的 `actor` (来自 keyId) 与 activity body 中的 `activity["actor"]` 从不交叉验证。攻击者可伪造 actor 字段。

---

### H2. ActivityPub — 公钥 Owner/ID 字段未验证

**文件:** `activitypub/signature.go:254-287`

`getPublicKey` 获取 actor 的公钥后，不验证 `PublicKey.ID == keyID` 和 `PublicKey.Owner == actorURI`。

---

### H3. 无文件扩展名白名单

**文件:** `upload.go:87,271` / `service.go:80`

所有上传路径直接使用 `filepath.Ext(header.Filename)` 作为文件扩展名，无白名单验证。可上传 `.php`, `.html`, `.exe` 等。

---

### H4. SSE Payload 注入 — 无换行符清理

**文件:** `controllers/api/streaming/common.go:122`

```go
_, _ = c.Writer.Write([]byte("event: " + event + "\n" + "data: " + payload + "\n\n"))
```

如果 Redis 中的 payload 包含 `\n\n`，会导致 SSE 帧被提前拆分，可能注入伪造事件。

---

### H5. Timeline 无最大 Limit 上限

**文件:** `controllers/api/timelines/common.go:15-28`

`TimelineRequest.Limit` 没有上限校验。客户端可请求 `?limit=999999999`，导致 DB 返回海量记录。

---

### H6. Admin Report Delete 无用户验证

**文件:** `controllers/api/report/delete.go:10-16`

删除 report 不验证操作者身份/角色，结合 C1 可被任意 web 登录用户调用。

---

### H7. Matrix Sync 时间源不一致

**文件:** `matrix/homeserver/sync.go`

| 位置 | 使用的时间源 | 问题 |
|------|-------------|------|
| `next_batch` 生成 (L266,396) | `time.Now()` (应用墙钟) | — |
| 增量时间线查询 (L475) | `created_at` (DB时间) | 时钟偏差导致事件遗漏 |
| 离开房间过滤 (L833,842) | `origin_server_ts` (远程服务器时间) | 联邦 leave 事件被永久过滤 |
| 新加入检测 (L443) | `origin_server_ts` (远程时间) | 联邦 join 事件漏检 |

**修复:** 统一使用 `created_at`（本地接收时间）。

---

### H8. Email SMTP 明文传输凭据

**文件:** `email/smtp/drive.go:28-34`

默认端口 25，无 TLS/STARTTLS。`smtp.PlainAuth()` 以明文发送用户名密码。

---

### H9. Email Template Path Traversal

**文件:** `email/send.go:49,103`

`templateName` 直接拼接 `"mails/" + templateName + ".html"`，含 `../` 可遍历嵌入文件系统。

---

### H10. Email CC 地址 Bug — 使用 To 而非 Cc

**文件:** `email/smtp/drive.go:88-89`

CC 收件人使用 `msg.To()` 而非 `msg.Cc()`，导致所有收件人互相可见。

---

### H11. Public Key Cache 无大小限制

**文件:** `activitypub/signature.go:158-197`

全局 `PublicKeyCache` 无上限，攻击者可向大量不同 keyId 发送活动以耗尽内存。

---

### H12. Admin 画廊 — 大量删除无防护

**文件:** `controllers/api/admin/thumbnails.go:250-309`

`older_than_days: 0` + `status: "all"` 会删除所有缩略图，无确认/限速。

---

## 🟡 Medium (建议修复)

### M1. `BatchAddToTimelineItems` 存在但从未使用

**文件:** `feed/service.go:304-352`

为每个 follower/mention/tag 逐个调用 `AddToTimelineItem`，每次一次 Redis 往返。Pipeline 优化函数已写好但未接入生产代码。1000 follower = 1000 次 Redis 调用。

---

### M2. Goroutine 泄漏 — WebSocket 断连

**文件:** `controllers/api/streaming/streaming.go:72-84`

`readChan` 是无缓冲 channel，主循环退出时 read goroutine 永久阻塞。每断一个连接泄漏一个 goroutine。

---

### M3. Media 服务 `subdir` 路径遍历 (潜在)

**文件:** `media/service.go:92-93`

`filepath.Join(subdir, ...)` 无 `..` 清理。当前无调用方，但一旦接入即漏洞。

---

### M4. SVG 文件接受风险

**文件:** `media/service.go:118` — `strings.HasPrefix(contentType, "image/")` 接受 `image/svg+xml`。API controller 虽不包含 SVG 在允许列表中，但仅依赖 Content-Type header。

---

### M5. 无解压炸弹防护

**文件:** `media/processor.go:97-131`

`ProcessImage` 无像素数上限。恶意压缩 PNG (1KB → 100K×100K) 可导致 OOM。

---

### M6. 无上传速率限制

**文件:** `controllers/api/media/upload.go`, `activitypub/inbox.go`

上传端点、inbox 端点均无速率限制。

---

### M7. LIKE 注入 — 通配符未转义

**文件:** `controllers/api/admin/accounts.go:61,66,72`

```go
query = query.Where("username LIKE ?", "%"+username+"%")
```

用户输入的 `%` 和 `_` 未转义。

---

### M8. 错误信息泄露内部细节

**文件:** `controllers/api/admin/matrix.go:42,83`

内部错误 (连接失败、迁移失败) 原样返回客户端，暴露内部主机名/端口。

---

### M9. `checkLocalStorage` 健康检查有副作用

**文件:** `health/health.go` — `StorageChecker` 在检查过程中创建目录。健康检查不应有副作用。

---

### M10. 迁移脚本部分未注册

**文件:** `migrate/migrate.go`

`ScripsType` 仅注册 4 个脚本。`MediaIndexes`, `PerformanceIndexes`, `Group`, `Matrix`, `MatrixHS` 等未注册，`Up()` 不会执行。

---

## 📊 汇总

| 严重级别 | 数量 | 类别 |
|---------|------|------|
| 🔴 Critical | 9 | 授权绕过(1), 协议安全(3), 上传安全(2), WebSocket(2), 数据损坏(1) |
| 🟠 High | 12 | 协议安全(3), 输入验证(3), Admin(2), 网络(2), 联邦(1), 缓存(1) |
| 🟡 Medium | 10 | 性能(1), 资源泄漏(1), 输入验证(3), 安全(2), 运维(3) |

**最优先修复 (Top 5):**

1. **C1** — Admin 授权绕过 → 添加 `IsAdmin` 检查
2. **C2+C3** — ActivityPub 签名验证 → 添加 Date 检查 + 强制 Digest
3. **C6+C7** — WebSocket 安全 → Origin 检查 + 修复 Subscribe 逻辑
4. **C4+C5** — 媒体上传 → 添加 magic bytes + async 路径验证
5. **C8+C9** — Feed Bug → 修复 CleanFeeds 参数 + Tag pushAllChannel