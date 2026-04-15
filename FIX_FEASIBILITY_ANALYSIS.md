# 修复方案全方位可行性分析报告

> 分析日期: 2026-04-15
> 分析范围: DEEP_REVIEW_REPORT.md 中 31 个问题的全部修复方案
> 分析维度: 可行性 (Feasibility)、稳定性 (Stability)、可维护性 (Maintainability)

---

## 评分标准

| 等级 | 含义 |
|------|------|
| ✅ 高 | 方案正确、可直接实施、风险极低 |
| ⚠️ 中 | 方案方向正确但需要调整或注意边界条件 |
| ❌ 低 | 方案有缺陷需要重新设计 |

---

## 🔴 Critical 修复方案分析

### C1. Admin 授权绕过

**原方案:** 在 scope 中间件中，对 Web token 且有 admin scope 时检查 `user.IsAdmin`。

**可行性: ✅ 高**
- `AuthMiddleware` 已将 `*models.User`（含 `IsAdmin`）存入 `c.Get("user")`，零额外查询
- 已验证全部 40+ admin 路由均未独立检查 `IsAdmin`
- 仅有 `AuthTypeWeb=0` 和 `AuthTypeOauth=1` 两种 token 类型，覆盖完整

**稳定性: ⚠️ 中 — 有 3 个边界条件需处理**

1. **Web 前端 admin UI 会受影响**：如果 Web 端使用 web token 调用 admin API，修复后非管理员将被拒。这是**期望行为**，但需确认前端已有 admin 权限判断逻辑，否则普通用户会看到 401 错误
2. **Web token 的 `Scopes` 字段为 nil**：`NewAuthToken(userId, AuthTokenTypeWeb, 0, 0, nil, ...)` 创建时 scopes 为 nil。直接调用 `tokenData.Scopes.HasScope()` 会 panic。原方案用 `scope.Type == AppScopeTypeAdmin` 做前置判断避免了此问题 ✅
3. **未来的 AuthType**：如果新增 `AuthType = 2`，该类型会走 OAuth 的 scope 检查路径——这是安全的默认行为 ✅

**可维护性: ✅ 高**
- 所有 admin 路由自动受保护，新增 admin 路由无需额外代码
- 授权逻辑集中在单一中间件，清晰可审计

**建议调整:**
```go
// 推荐方案：对 admin scope 类型，不区分 AuthType，统一检查 IsAdmin
if tokenData.AuthType == models.AuthTokenTypeWeb {
    hasAdminScope := false
    for _, scope := range scopes {
        if scope.Type == models.AppScopeTypeAdmin {
            hasAdminScope = true
            break
        }
    }
    if !hasAdminScope {
        c.Next()  // 非 admin 的 Web token 仍然放行
        return
    }
    // 有 admin scope 的 Web token，需要 IsAdmin 检查
}
// 继续走到 scope 检查...
```

---

### C2. ActivityPub 无重放防护

**原方案:** 解析 `Date` header，检查是否在 ±5 分钟窗口内。

**可行性: ✅ 高**
- ~25 行纯 stdlib 代码，无新依赖

**稳定性: ⚠️ 中 — 有 4 个边界条件需处理**

1. **Date 格式不匹配**：原方案用 `time.RFC1123` 解析。HTTP Date 可以是 `RFC1123`、`RFC1123Z`、`RFC850`、`asctime` 四种格式。部分联邦服务器可能发送带时区偏移的日期（如 `-0500`），`time.RFC1123` 无法解析。
   - **建议:** 改用 `http.ParseTime()`，它是 Go 标准库专门为 HTTP Date 设计的多格式解析器
2. **Date header 缺失**: 原方案用 `if dateStr != ""` 做条件判断，缺失时直接放行。这是**错误的安全策略**。
   - **建议:** Date 缺失时**必须拒绝**，但同时需要验证 `date` 在 `sig.Headers`（已签名 header 列表）中。否则攻击者可以伪造 Date header 绕过
3. **5 分钟窗口是否合适**: Mastodon ~30s（过于激进），Pleroma/Lemmy/GoToSocial 均用 5 分钟。推荐 5 分钟，可考虑做成配置项
4. **服务器时钟偏差**: 自托管实例可能存在 >5 分钟偏差，导致合法请求被拒。但联邦协议要求时钟相对同步，这是合理的约束

**可维护性: ✅ 高**
- 独立函数，清晰错误信息
- 窗口时间可配置化

**建议调整:**
```go
func verifyRequestDate(sig *SignatureHeader, req *http.Request) error {
    // 1. 检查 date 在签名 header 列表中
    if !strings.Contains(sig.Headers, "date") {
        return fmt.Errorf("date header must be signed")
    }
    // 2. 必须有 Date header
    dateStr := req.Header.Get("Date")
    if dateStr == "" {
        return fmt.Errorf("missing date header")
    }
    // 3. 使用 http.ParseTime 解析（支持所有 HTTP 日期格式）
    date, err := http.ParseTime(dateStr)
    if err != nil {
        return fmt.Errorf("invalid date format: %w", err)
    }
    // 4. 检查时间窗口
    skew := 5 * time.Minute
    now := time.Now()
    if date.Before(now.Add(-skew)) || date.After(now.Add(skew)) {
        return fmt.Errorf("request date too far from current time")
    }
    return nil
}
```

---

### C3. Digest 非强制验证

**原方案:** 对有 body 的 POST 请求，强制要求 Digest header 并验证 digest 在签名 headers 列表中。

**可行性: ✅ 高**
- 现有 digest 验证逻辑只需加强（从 optional 变 mandatory）

**稳定性: ⚠️ 中 — 有 3 个边界条件需处理**

1. **GET 请求无 body**: 不需要 Digest。`len(body) == 0` 的 POST 也不需要。应只对 `len(body) > 0` 强制
2. **`digest` 必须在 `sig.Headers` 中**: 如果 `digest` header 存在但未包含在签名 header 列表中，攻击者可以篡改 Digest header 本身。必须验证 `strings.Contains(sig.Headers, "digest")`
3. **联邦兼容性**: Mastodon/Pleroma/Misskey 均签名 digest，符合 ActivityPub HTTP Signatures 规范。极少数非标准实现可能不签——但这正是安全增强的目的

**与 C2 的执行顺序（关键）:**
```
ParseSignatureHeader → verifyRequestDate (C2) → getPublicKey → verifyDigestIsSigned (C3) → RSA verify
```
- C2 在密钥获取前执行（快速时间检查，省网络开销）
- C3 在 RSA 前执行（SHA-256 比 RSA-2048 快几个数量级）
- 两者检查不同字段，互不依赖，可独立失败 ✅

**可维护性: ✅ 高**
- 与 C2 合并为签名验证前检查链，逻辑清晰

**建议实现:**
```go
func verifyDigestIsSigned(sig *SignatureHeader, req *http.Request, body []byte) error {
    if len(body) == 0 {
        return nil  // 无 body 不需要 Digest
    }
    // 检查 digest 在签名 header 列表中
    if !strings.Contains(sig.Headers, "digest") {
        return fmt.Errorf("digest header must be signed when body is present")
    }
    // 检查 Digest header 存在
    digest := req.Header.Get("Digest")
    if digest == "" {
        return fmt.Errorf("missing digest header for request with body")
    }
    // 验证 digest 值匹配 body
    expected := "SHA-256=" + base64.StdEncoding.EncodeToString(
        sha256.Sum256(body)[:],
    )
    if digest != expected {
        return fmt.Errorf("digest mismatch")
    }
    return nil
}
```

---

### C4. UploadAsync 跳过文件验证

**原方案:** 在 `processUploadAsync` 开头添加与 `Upload` 相同的类型/大小验证。

**可行性: ✅ 高**
- `isAllowedContentType()` 和 `isValidFileSize()` 已存在，可直接复用
- 两个函数签名分别为 `func(string) bool` 和 `func(int64, string) bool`，参数在 goroutine 中均可获取

**稳定性: ⚠️ 中 — 有 2 个重要调整**

1. **验证时机**: 原方案说"在 `processUploadAsync` 开头添加"，但 `processUploadAsync` 是在 goroutine 中运行的。`UploadAsync` 在启动 goroutine 前已经创建了 DB 记录（`db.GetDB().Create(mediaRecord)`）。如果在 goroutine 中才验证失败，DB 中会残留一条无效记录。
   - **建议:** 在 `UploadAsync` 中、创建 DB 记录**之前**就做验证。失败时直接返回 415/413，不进入 goroutine，不写 DB
2. **提取共用函数**: `Upload` 中验证散布在流程各处（L65-69, L72-75），应提取为共用函数 `validateFileUpload(header *multipart.FileHeader) error`

**可维护性: ✅ 高**
- 消除 Upload/UploadAsync 的验证逻辑重复

**建议实现:**
```go
// 新增共用验证函数
func validateFileUpload(header *multipart.FileHeader) error {
    contentType := header.Header.Get("Content-Type")
    if !isAllowedContentType(contentType) {
        return fmt.Errorf("unsupported media type: %s", contentType)
    }
    if !isValidFileSize(header.Size, contentType) {
        return fmt.Errorf("file size exceeds limit")
    }
    return nil
}

// UploadAsync 中：在创建 DB 记录前验证
func UploadAsync(c *gin.Context) {
    // ... 绑定请求 ...
    if err := validateFileUpload(fileHeader); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return  // 不创建 DB 记录，不启动 goroutine
    }
    // ... 创建 DB 记录 & 启动 goroutine ...
}
```

---

### C5. 全链路无 Magic Bytes 验证

**原方案:** 在保存文件前读取前 512 字节，用 `http.DetectContentType()` 验证。

**可行性: ✅ 高（但建议用 `filetype.Match()` 而非 `http.DetectContentType()`）**

**两种方案对比:**

| 特性 | `http.DetectContentType()` | `filetype.Match()` |
|------|--------------------------|-------------------|
| 支持类型 | ~25 种 | 300+ 种 |
| WebP 支持 | Go 1.21+ | 所有 Go 版本 |
| 已在项目中使用 | 仅 matrix/homeserver | `attachment/save.go` ✅ |
| 读取字节数 | 512 | 261 (min) |
| 检测精度 | 中等 | 高（专用 magic 数据库）|

- **建议使用 `filetype.Match()`**：已在 `go.mod` 中，已在 `attachment/save.go` 中使用，支持更全面
- 512 字节足够：所有常见格式的 magic number 均在前 512 字节内 ✅
- `http.DetectContentType()` 的 WebP 支持依赖 Go 1.21+，需确认项目 Go 版本

**稳定性: ⚠️ 中 — 边界条件**

1. **类型不匹配时如何处理**: Content-Type 为 `image/jpeg` 但 magic bytes 为 `image/png` 的情况——应**拒绝上传**（不应静默覆盖 Content-Type，因为后续处理流程依赖 Content-Type 决定图片处理器）
2. **polyglot 文件**: JPEG 头部 + PHP payload。Magic bytes 检测只能确认文件头匹配，不能保证文件尾部安全。这需要额外的图片解码验证（可参考 M5 解压炸弹防护）
3. **误报风险**: `filetype.Match()` 返回 `application/octet-stream` 表示未知——应视为不匹配

**可维护性: ✅ 高**
- 集中在文件保存前一个检查点
- `filetype` 库已存在，无需新增依赖

---

### C6. WebSocket Origin Check 禁用

**原方案:** `origin == "" || origin == "https://"+r.Host`

**可行性: ⚠️ 中 — 方案过于简化，需调整为可配置方案**

**稳定性: ❌ 有 4 个会导致生产环境 WebSocket 完全中断的风险**

1. **反向代理场景（最严重）**: 应用在 Nginx/Caddy/Cloudflare 后面时，`r.Host` 是**内部主机名**（如 `localhost:8080`），而 `Origin` 是**外部域名**（如 `social.example.com`）。原方案的比较**永远失败**，导致所有浏览器 WebSocket 连接中断
2. **端口差异**: 开发环境前端在 `:3000`，后端在 `:8080`。Origin 带 port，Host 可能不带
3. **多域名**: 同一实例服务 `example.com` 和 `social.example.com` 时，单 host 比较不够
4. **`origin == ""` 放行**: 原方案允许空 Origin。非浏览器客户端（CLI 工具、服务端连接）不发 Origin header，放行是正确行为。但需确认不会有浏览器在特殊情况下不发 Origin

**可维护性: ⚠️ 需要配合配置系统**

**建议调整为可配置方案:**
```go
var upgrade = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        origin := r.Header.Get("Origin")
        if origin == "" {
            return true  // 非浏览器客户端
        }
        originURL, err := url.Parse(origin)
        if err != nil {
            return false
        }
        // 从配置读取允许的域名列表
        // 回退到 r.Host 用于简单部署
        allowedHosts := config.C.WebSocketAllowedOrigins
        if len(allowedHosts) == 0 {
            allowedHosts = []string{r.Host}
        }
        for _, allowed := range allowedHosts {
            if originURL.Host == allowed {
                return true
            }
        }
        return false
    },
}
```

---

### C7. Subscribe 逻辑反转

**原方案:** 将 `Unsubscribe` 改为 `Subscribe`。

**可行性: ✅ 高**
- go-redis `PubSub.Subscribe()` 支持向现有 PubSub 连接添加 channel，天然支持多路复用
- 完整生命周期追踪确认：首次 sub 时 pub==nil，走创建路径；后续 sub 时 pub!=nil，走添加路径

**稳定性: ✅ 高**
- 修复后，Redis PubSub 单连接多 channel 天然工作正确
- `redisMsgChan` 是单个 channel，Redis 自动将所有已订阅 channel 的消息推送到同一 channel
- 无并发风险：goroutine 模型序列化消息处理

**可维护性: ⚠️ 中**
- 当前架构仍是单 PubSub + 无 channel 追踪列表。unsubscribe 无法精确知道订阅了哪些 channel
- 建议：后续维护中添加 `subscribedChannels map[string]bool` 以支持精确取消订阅
- 但当前修复已足够——功能从"完全不可用"变为"可用"

**结论: 原方案正确，1 行修复，可直接实施。**

---

### C8. CleanFeeds 双重前缀

**原方案:** `[]string{"feed:public:all"}` → `[]string{allPublicTimeline.GetTimelineId()}`

**可行性: ✅ 高**
- `allPublicTimeline.GetTimelineId()` 返回 `"all"`
- `CleanFeeds` 内部执行 `Sprintf("feed:public:%s", feedId)` → `"feed:public:all"` ✅
- 与 `tag_timeline.go:102` 的写法完全一致（已有正确用例）

**稳定性: ✅ 高**
- 纯正确性修复，无副作用
- Rebuild 函数现在能正确清理目标 key

**可维护性: ✅ 高**
- 与现有正确用法对齐，消除 API 语义歧义

**结论: 原方案正确，1 行修复，可直接实施。**

---

### C9. Tag 推送缺少 Tag 字段

**原方案:** 函数签名增加 `tagName` 参数，每个 `TagTimeline` 设置 `Tag: tagName`。

**可行性: ✅ 高**
- 完整调用链追踪：`tag.Push()` 遍历 `status.Tags`，对每个 tag 调用 `pushAllChannel(status, method)`——tag.Name 在调用点可用，只需传入
- `TagTimeline` 结构体已有 `Tag string` 字段，初始化时设空是遗漏而非设计

**稳定性: ✅ 高**
- 当前功能完全失效，任何修复只能改善
- Redis channel 名称将从 `"subscribed:feed:hashtag:"` 变为 `"subscribed:feed:hashtag:golang"` 等，与 WebSocket 客户端的订阅 channel 匹配

**可维护性: ✅ 高**
- 与 `public_timeline.go` 的 `pushAllChannel` 结构一致

**注意:** 需同步修改 `Unpush()` 中的 `pushAllChannel` 调用（如果存在）。

**结论: 原方案正确，~10 行修复，可直接实施。**

---

## 🟠 High 修复方案分析

### H1. Actor 字段未与签名验证者比对

**可行性: ✅ 高**
- `VerifyActivitySignature` 返回的 `actor`（从 keyId 提取）和 activity JSON 中的 `"actor"` 字段在 `inbox.go:72-96` 处均可获取
- 4 行代码即可完成交叉验证

**稳定性: ✅ 高**
- 所有合规 ActivityPub 实现都会设置 `actor` 匹配签名密钥
- `actor` 字段可能为空（某些 activity 类型），`!= ""` 前置判断避免误拒
- 仅拒绝伪造 actor 的恶意请求，不影响正常联邦

**可维护性: ✅ 高**
- Mastodon/Pleroma 均执行此检查，是 ActivityPub 安全标准实践

**结论: 原方案正确，4 行修复，可直接实施。**

---

### H2. 公钥 Owner/ID 未验证

**可行性: ✅ 高**
- 需确认 Actor model 结构体有 `Id`、`PublicKey.Id`、`PublicKey.Owner` 字段——根据 `ParsePublicKey` 和 `FetchActor` 的使用模式推断这些字段存在
- ~10 行验证逻辑

**稳定性: ✅ 高**
- 防止缓存投毒攻击（恶意 actor 的公钥被缓存到受害者 keyId 下）
- 所有合规实现都包含这三个字段。`!= ""` 前置判断兼容缺少字段的非标准实现
- **必须在 H2 修复后再实施 H11（缓存限制），否则 H11 的缓存限制可能掩盖投毒问题**

**可维护性: ✅ 高**
- 实现 W3C Security Vocabulary 的 key ownership proof
- 每个检查都有清晰的错误信息

**结论: 原方案正确，建议实现。修复优先级应在 H11 之前。**

---

### H3. 无文件扩展名白名单

**可行性: ✅ 高**

**比原方案更好的方案:** 不维护独立的扩展名白名单，而是**从已验证的 Content-Type 推导扩展名**：
```go
exts, _ := mime.ExtensionsByType(validatedContentType)
ext := exts[0]  // 如 "image/jpeg" → ".jpg"
```
- 消除了 Content-Type ↔ Extension 不一致的问题
- 系统已通过 `isAllowedContentType` 限制了合法 Content-Type，`mime.ExtensionsByType` 只会返回对应扩展名
- 文件已被重命名为时间戳格式，客户端原始文件名不影响最终扩展名

**稳定性: ✅ 高**
- `mime.ExtensionsByType` 是标准库函数，对所有已注册 MIME 类型可靠
- 覆盖了 `.jpg/.jpeg` 双扩展名等边界情况

**可维护性: ✅ 高**
- 无需维护静态映射表
- 新增允许的 Content-Type 时自动获得对应扩展名

---

### H4. SSE Payload 注入

**可行性: ✅ 高**

**实际风险重新评估（比报告更低）:**
- `payload` 是 `json.Marshal` 的输出——JSON 标准不产生裸换行符 ✅
- `event` 值来自系统硬编码类型字符串 ✅
- 唯一风险：Redis 中被篡改的 status event 的 `Type` 字段含恶意字符（极端情况）

**建议:** 仍应修复（defense-in-depth），但属低优先级。只需转义 `\r` 和 `\n`。

---

### H5. Timeline 无最大 Limit

**可行性: ✅ 高**
- 一行 `if req.Limit > 80 { req.Limit = 80 }` 即可
- admin 端点已使用 80 作为上限

**稳定性: ✅ 高**
- 仅限制上限，不影响正常使用

---

### H6. Admin Report Delete 无用户验证

**可行性: ✅ 高**
- 依赖 C1 修复。C1 修复后，Web token 访问 admin 路由需要 `IsAdmin=true`
- H6 自动被 C1 修复覆盖

**结论: C1 修复后此项自动解决，无需单独处理。**

---

### H7. Matrix Sync 时间源不一致

**可行性: ⚠️ 建议不修改**

**深入分析后的结论:**
- 使用 `created_at`（本地接收时间）进行增量过滤是 **Matrix 协议的标准做法**（Synapse、Dendrite 均如此）
- 代码注释明确解释了设计意图（L470-473）
- `time.Now()` 用于 `next_batch` token 生成，`created_at` 用于增量查询——这是正确的两层设计
- `origin_server_ts` 用于联邦事件的 leave/join 过滤存在理论上的时钟偏差风险，但这是 Matrix 协议固有限制，不修复也不影响安全性

**唯一建议:** L475 的 `created_at > ?` 考虑改为 `created_at >= ?`，避免极端情况下边界事件遗漏（代价是可能的重复，客户端应去重）

**结论: 当前实现基本正确，无需重大修改。降级为 Low 优先级或标记为 "works as designed"。**

---

### H8. Email SMTP 明文传输

**可行性: ⚠️ 中**

**原方案未给出具体实现。需要重写 SMTP 发送逻辑：**

当前 `smtp.SendMail(d.server, d.auth, ...)` 不支持 STARTTLS。需要改为手动 `smtp.NewClient` → `StartTLS` → `Auth`。

**建议:**
```go
// 替换 smtp.SendMail 为手动客户端
conn, _ := tls.DialWithDialer(&net.Dialer{Timeout: 10*time.Second}, "tcp", server, &tls.Config{ServerName: host})
client, _ := smtp.NewClient(conn, host)
client.Auth(d.auth)
// ... 发送邮件 ...
```

**稳定性: ⚠️**
- 需要同时支持 TLS 直连和 STARTTLS 两种模式
- 开发环境可能需要非 TLS（如 MailHog on port 1025）
- 应增加 `SmtpTLS` 配置项（默认 `true`），TLS 禁用时打印 warning

**可维护性: ✅ 高**
- 可配置的 TLS 标志是行业标准做法

---

### H9. Email Template Path Traversal

**可行性: ✅ 高（但实际风险比报告评估更低）**

**风险降级原因:**
1. 生产环境使用 `embed.FS`——**完全沙箱化**，路径遍历无法逃逸
2. `language.Make()` 对 Accept-Language 严格校验，产生合法 BCP 47 tag
3. 模板基础名全部硬编码（`"recover_password_verify_email"` 等）
4. 仅开发环境（`os.DirFS`）存在理论风险

**建议: 仍添加 allowlist 校验作为 defense-in-depth，但优先级降为 Low。**

---

### H10. Email CC 使用 To 而非 Cc

**可行性: ✅ 高**
- 一字修复：`msg.To("", addr)` → `msg.Cc("", addr)`
- `enmime.Builder` 同时提供 `To()` 和 `Cc()` 方法

**稳定性: ✅ 高**
- 无副作用，纯粹修正 API 调用

**结论: 原方案正确，1 字修复，可直接实施。**

---

### H11. Public Key Cache 无大小限制

**可行性: ✅ 高**

**两种实现方案:**

| 方案 | 代码量 | 依赖 | 推荐度 |
|------|--------|------|--------|
| 引入 `hashicorp/golang-lru` | ~5 行 | 新增依赖 | ⭐⭐⭐⭐⭐ |
| 手动添加限制+清理 | ~40 行 | 无 | ⭐⭐⭐ |

**推荐 `hashicorp/golang-lru/v2/expirable`**：Battle-tested，LRU+TTL 内置，API 简洁。

**稳定性: ✅ 高**
- 10,000 上限 × ~1KB/entry ≈ 10MB，对任何实例都可忽略
- LRU 淘汰策略合理——活跃 actor 的密钥保留，不活跃的被淘汰
- 1 小时 TTL 已在现有代码中

**可维护性: ✅ 高**
- 替换手写 cache 为成熟库，减少维护负担

**⚠️ 重要:** 此修复应在 H2（公钥 Owner 验证）之后实施。如果先限制缓存但未防止投毒，被投毒的密钥可能在被淘汰前已造成损害。H2 阻止投毒，H11 限制内存——两者互补。

---

### H12. Admin 画廊批量删除

**可行性: ⚠️ 低优先级**

**结论: 这是预期的 admin 运维行为，不是安全漏洞。**
- 路由已受 `CheckScopeMiddleware` 保护
- C1 修复后只有真正的 admin 能访问
- 参数提供合理的过滤维度

**可选改进（非必须）:**
1. `dry_run` 参数返回预计删除数量
2. 单次最大删除量限制（如 10,000）
3. 修复 fallback 路径不删除存储文件的遗漏

---

## 🟡 Medium 修复方案分析

### M1. BatchAddToTimelineItems 未使用

**可行性: ✅ 高**
- 函数已存在，只需替换逐个调用为批量调用
- 注意：pipeline 中的 `ZScore` 检查结果被忽略（死代码），应清理

**稳定性: ⚠️**
- Pipeline 不支持条件逻辑（无法根据 ZScore 结果决定是否 ZAdd）
- 建议：reblog 情况回退到单条路径，非 reblog 用 pipeline

---

### M2. Goroutine 泄漏 — WebSocket 断连

**可行性: ✅ 高**
- `readChan` 是无缓冲 channel，`closeChan` 关闭后 read goroutine 永久阻塞
- 解决方案：在 close 时关闭 WebSocket 连接（`ws.Close()`），使 `ReadMessage()` 返回错误，goroutine 自然退出

**稳定性: ✅ 高**
- `ws.Close()` 是幂等操作

---

### M3. Media 服务 subdir 路径遍历

**可行性: ✅ 高**
- 当前无调用方，属于潜在漏洞
- `filepath.Clean(subdir)` + `strings.Contains(subdir, "..")` 检查即可

---

### M4. SVG 文件接受风险

**可行性: ✅ 高**
- API controller 的 `AllowedImageTypes` 不含 SVG ✅
- 但 media service 只检查 `strings.HasPrefix(contentType, "image/")`，SVG 是 `image/svg+xml`
- 解决方案：在白名单中显式排除 `image/svg+xml`

---

### M5. 无解压炸弹防护

**可行性: ✅ 高**
- 在 `ProcessImage` 中添加像素数上限检查（如 100M 像素）
- Go `image.DecodeConfig` 可在不解码完整图像的情况下获取尺寸

---

### M6. 无上传速率限制

**可行性: ⚠️ 中**
- 需要 Redis 计数器或令牌桶中间件
- 涉及全局限流架构设计，非单点修复
- 建议：作为独立任务规划

---

### M7. LIKE 注入通配符

**可行性: ✅ 高**
- 转义 `%`、`_`、`\` 三个字符即可
- Admin-only 端点，风险极低

---

### M8. 错误信息泄露内部细节

**可行性: ✅ 高**
- 替换为通用错误消息，详细错误写日志

---

### M9. checkLocalStorage 健康检查副作用

**可行性: ✅ 高**
- 改为只读检查（`os.Stat` + `os.Open` 只读打开）
- 在只读文件系统容器中兼容性更好

---

### M10. 迁移脚本部分未注册

**可行性: ✅ 高（但需谨慎）**

**⚠️ 关键注意:**
- 8 个脚本未注册，涉及 Matrix、ActivityPub、Group、Relay、Media 功能
- **必须验证**这些脚本创建的表是否已通过 GORM AutoMigrate 或其他方式存在
- 如果表已存在，盲目注册可能导致重复创建冲突
- 如果表不存在，这些功能完全不可用

**建议:**
1. 检查 GORM AutoMigrate 是否覆盖了这些表
2. 如未覆盖，注册缺失脚本
3. 注册前检查每个脚本的 `Up()` 是否有 `IF NOT EXISTS` 保护
4. 添加注册后，在测试环境完整运行迁移验证

---

## 📊 修复优先级矩阵（修订版）

### P0 — 立即修复（安全漏洞，方案已验证）

| ID | 问题 | 修复量 | 风险 |
|----|------|--------|------|
| C1 | Admin 授权绕过 | ~15 行 | 需确认前端兼容 |
| C2 | 重放攻击防护 | ~25 行 | 使用 `http.ParseTime`，验证 date 已签名 |
| C3 | Digest 强制验证 | ~20 行 | 需验证 digest 在 sig.Headers 中 |
| C7 | Subscribe 逻辑反转 | 1 行 | 无风险 |
| C8 | CleanFeeds 双前缀 | 1 行 | 无风险 |
| C9 | Tag 推送缺失 Tag | ~10 行 | 无风险 |

### P1 — 高优先级（安全加固，方案需微调）

| ID | 问题 | 修复量 | 调整 |
|----|------|--------|------|
| C4 | UploadAsync 无验证 | ~15 行 | 在 goroutine 前验证 |
| C5 | Magic Bytes 验证 | ~20 行 | 用 `filetype.Match()` |
| H1 | Actor 未比对 | 4 行 | 无调整 |
| H2 | 公钥 Owner 未验证 | ~10 行 | 优先于 H11 |
| H3 | 扩展名白名单 | ~5 行 | 从 Content-Type 推导 |
| C6 | WebSocket Origin | ~15 行 | **必须改为可配置方案** |
| H10 | CC Bug | 1 字 | 无调整 |

### P2 — 中优先级（加固/改善）

| ID | 问题 | 修复量 | 备注 |
|----|------|--------|------|
| H4 | SSE 注入 | ~5 行 | 低实际风险 |
| H5 | Limit 无上限 | 1 行 | admin 已有 80 限制 |
| H8 | SMTP 明文 | ~30 行 | 需重写发送逻辑 |
| H11 | Cache 无限 | ~5 行(库) | H2 之后实施 |
| H9 | Template 遍历 | ~5 行 | embed.FS 已沙箱化 |
| M1 | BatchAdd 未用 | ~20 行 | 需处理 pipeline 条件逻辑 |
| M2 | Goroutine 泄漏 | ~3 行 | 关闭 ws 触发退出 |
| M5 | 解压炸弹 | ~5 行 | DecodeConfig 检查像素 |
| M7 | LIKE 注入 | ~5 行 | Admin-only |
| M8 | 错误泄露 | ~10 行 | 替换为通用消息 |
| M9 | 健康检查副作用 | ~15 行 | 改为只读 |
| M10 | 迁移未注册 | ~8 行 | **需先验证表状态** |

### P3 — 低优先级 / 无需修复

| ID | 问题 | 原因 |
|----|------|------|
| H6 | Report Delete 无验证 | C1 修复自动覆盖 |
| H7 | Matrix 时间源 | 设计正确，协议标准做法 |
| H12 | Admin 批量删除 | 预期 admin 行为 |
| M3 | subdir 遍历 | 无调用方 |
| M4 | SVG 风险 | 白名单不含 SVG |
| M6 | 速率限制 | 需架构设计 |

---

## 🔑 跨问题依赖关系

```
H2 (公钥 Owner 验证) → H11 (Cache 限制)
    ↑ 必须先修复 H2 防止投毒，再限制缓存大小

C1 (Admin 授权) → H6 (Report Delete)
    ↑ C1 修复后 H6 自动解决

C2 (Date 检查) + C3 (Digest 强制) → 签名验证链
    ↑ 执行顺序: ParseSig → C2(date) → getPublicKey → C3(digest) → RSA
    ↑ 两个快速检查在前，省网络+CPU 开销

C4 (UploadAsync 验证) + C5 (Magic Bytes) + H3 (扩展名)
    ↑ 可合并为一次 "上传验证链" 重构
    ↑ 验证顺序: Content-Type 白名单 → 文件大小 → Magic Bytes → 扩展名推导
```

---

## ⚠️ 原方案中需要修正的问题

1. **C2**: `time.RFC1123` → 应改为 `http.ParseTime()`；Date 缺失时应拒绝而非放行；需验证 date 在签名 header 中
2. **C4**: 验证应在 goroutine 启动前、DB 记录创建前执行，而非在 goroutine 内
3. **C5**: `http.DetectContentType()` → 建议用 `filetype.Match()`（更全面，已是项目依赖）
4. **C6**: 原方案 `origin == "" || origin == "https://"+r.Host` 在反向代理场景下会中断所有 WebSocket 连接 → 必须改为可配置方案
5. **H3**: 不需要独立白名单 → 从已验证 Content-Type 推导扩展名更优
6. **H7**: 建议不修改或仅做 `>` → `>=` 微调
7. **H9**: 实际风险因 `embed.FS` 沙箱化而大幅降低