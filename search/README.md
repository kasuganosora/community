# search (搜索引擎)

基于 [Meilisearch](https://www.meilisearch.com/) 的全文搜索引擎模块，为社区提供账户、嘟文（Status）和标签（Hashtag）的高性能搜索能力，支持毫秒级全文检索、容错搜索、排序与过滤。

## 核心功能

- **全文搜索**：对账户、嘟文、标签三大实体执行 Meilisearch 全文检索，返回匹配的 ID 列表。
- **索引管理**：提供单条与批量索引写入（`Index*` / `BatchIndex*`）以及删除（`Delete*`）接口，保持索引与数据库同步。
- **高级搜索语法**：通过 `ParseSearchQuery` 解析 `from:`、`has:`、`is:`、`before:`、`after:`、`#tag` 等查询语法。
- **过滤与排序**：支持基于 Meilisearch 过滤表达式（filter DSL）的范围限制与字段排序（如按 `created_at`、`status_count` 排序）。
- **自动重连**：初始化连接失败时启动后台健康检查循环，Meilisearch 恢复后自动重新启用并初始化索引。
- **安全防护**：对所有拼入过滤表达式的 ID 进行数字校验（`sanitizeID` / `sanitizeIDs`），防止过滤器注入攻击。
- **运维监控**：提供 `HealthCheck()` 健康检查与 `GetStats()` 索引统计信息接口。

## 索引概览

Meilisearch 索引名称常量：

| 常量 | 索引名 | 可搜索字段 | 可过滤字段 | 可排序字段 |
|------|--------|------------|------------|------------|
| `IndexAccounts` | `accounts` | `username`、`display_name`、`bio`、`domain` | `id`、`created_at` | `created_at` |
| `IndexStatuses` | `statuses` | `content`、`spoiler_text`、`username` | `account_id`、`visibility`、`created_at` | `created_at` |
| `IndexHashtags` | `hashtags` | `name` | `status_count`、`created_at` | `status_count`、`created_at` |

> 三个索引均使用默认排序规则：`words`、`typo`、`proximity`、`attribute`、`sort`、`exactness`。

## 配置

搜索引擎配置通过 `config.C.Other` 映表读取（键名不区分大小写由调用方决定）。

| 配置键 | 默认值 | 说明 |
|--------|--------|------|
| `MEILISEARCH_HOST` | `http://127.0.0.1:7700` | Meilisearch 服务地址。默认用 IPv4，避免 `localhost` 先走 `::1` 连不上只监听 127.0.0.1 的实例 |
| `MEILISEARCH_API_KEY` | （空） | Meilisearch API Key。开发模式不设 master key 时留空 |
| `MEILISEARCH_ENABLED` | `auto` | `auto` 或未设置：引擎在线就用，离线走数据库并持续重试。`true` 启动时先连一次。`false` 完全不用 |

```bash
# Meilisearch 服务地址
MEILISEARCH_HOST=http://127.0.0.1:7700

# Meilisearch API Key（开发模式可留空）
MEILISEARCH_API_KEY=

# 自动适配。显式 false 才关闭
MEILISEARCH_ENABLED=auto
```

> **自动适配**：未设置或 `auto` 时后台探测默认地址，通了就建索引并回填，断了立刻改走数据库，恢复后重新建索引并回填。显式 `false`/`off` 不会探测。

## 架构与结构

```
search/
├── meilisearch.go        # 核心实现：客户端封装、文档类型、搜索与索引 API
└── meilisearch_test.go   # 单元测试与注入防护测试
```

### 运行时行为

```
GetMeilisearchClient()  (sync.Once 单例)
        │
        ▼
   enabled == true ?
        │ yes
        ▼
   init()  (sync.Once)
        │
        ├─ 创建 meilisearch 客户端 (HTTP 超时 30s)
        ├─ 连接检查 (VersionWithContext, 超时 5s)
        │     ├─ 成功 → initIndexes() 创建三个索引并应用设置
        │     └─ 失败 → setEnabled(false) + 启动 reconnectLoop()
        │                   └─ 每 30s 重试，成功后重新启用并重建索引
```

### 安全限制常量

| 常量 | 值 | 说明 |
|------|----|------|
| `maxSearchLimit` | `40` | 单次搜索返回的最大结果数 |
| `defaultSearchTimeout` | `10s` | 每次搜索请求的超时时间 |
| `defaultHTTPTimeout` | `30s` | HTTP 客户端整体超时 |
| `initCheckTimeout` | `5s` | 初始化连接检查超时 |
| `indexOpsTimeout` | `15s` | 索引写操作（增删改）超时 |
| `batchSize` | `1000` | 批量索引的批次大小 |

## 用法示例

### 初始化与检查启用状态

```go
import "github.com/kasuganosora/community/search"

// 获取单例客户端（内部使用 sync.Once，首次调用时初始化并连接 Meilisearch）
client := search.GetMeilisearchClient()

// 检查搜索引擎是否已启用（enabled == true 且 client != nil）
if !client.IsEnabled() {
    // 搜索引擎未启用，可降级为数据库查询
}
```

### 索引写入

```go
client := search.GetMeilisearchClient()

// 索引单个账户
err := client.IndexAccount(&search.AccountSearchDocument{
    ID:          "1",
    Username:    "alice",
    DisplayName: "Alice",
    Domain:      "example.com",
    Bio:         "Hello world",
    CreatedAt:   time.Now().Unix(),
})

// 索引单条嘟文
err = client.IndexStatus(&search.StatusSearchDocument{
    ID:          "100",
    Content:     "This is a status update",
    SpoilerText: "",
    AccountID:   "1",
    Username:    "alice",
    Visibility:  "public",
    CreatedAt:   time.Now().Unix(),
})

// 索引单个标签
err = client.IndexHashtag(&search.HashtagSearchDocument{
    ID:          "200",
    Name:        "golang",
    StatusCount: 42,
    CreatedAt:   time.Now().Unix(),
})
```

### 批量索引

```go
client := search.GetMeilisearchClient()

// 批量索引账户（内部按 batchSize=1000 分批提交）
docs := []*search.AccountSearchDocument{
    {ID: "1", Username: "alice"},
    {ID: "2", Username: "bob"},
}
err := client.BatchIndexAccounts(docs)

// 批量索引嘟文
statusDocs := []*search.StatusSearchDocument{
    {ID: "100", Content: "first", AccountID: "1"},
    {ID: "101", Content: "second", AccountID: "2"},
}
err = client.BatchIndexStatuses(statusDocs)
```

### 删除索引

```go
client := search.GetMeilisearchClient()

err := client.DeleteAccount("1")   // 删除账户索引
err = client.DeleteStatus("100")   // 删除嘟文索引
err = client.DeleteHashtag("200")  // 删除标签索引
```

### 全文搜索

```go
client := search.GetMeilisearchClient()

// 搜索账户（limit/offset 会被限制在 [0, maxSearchLimit=40] 范围内）
// followingIDs 用于过滤为当前用户关注的账户 ID 列表
accountIDs, err := client.SearchAccounts("alice", 10, 0, []string{"1", "2", "3"})

// 搜索嘟文（可按 accountID 和 visibility 过滤，结果按 created_at 倒序）
statusIDs, err := client.SearchStatuses("hello world", 20, 0, "1", []string{"public"})

// 搜索标签（结果按 status_count 倒序）
hashtagNames, err := client.SearchHashtags("go", 10, 0)
```

> 搜索结果返回的是 **ID 列表**（`[]string`），而非完整文档。调用方需根据 ID 从数据库查询完整实体。标签搜索返回的是标签 `name` 列表。

### 高级搜索语法

```go
// 解析高级搜索语法，返回纯文本与过滤器映射
text, filters := search.ParseSearchQuery("hello from:alice has:media #golang world")

// text     => "hello world"
// filters  => map[string]string{
//                "from": "alice",
//                "has":  "media",
//                "tag":  "golang",
//             }
```

支持的语法：

| 语法 | 键名 | 示例 | 说明 |
|------|------|------|------|
| `from:<user>` | `from` | `from:alice` | 搜索特定用户的嘟文 |
| `has:<type>` | `has` | `has:media` | 搜索包含媒体的嘟文 |
| `is:<type>` | `is` | `is:reply` | 搜索回复/转发 |
| `before:<date>` | `before` | `before:2024-01-01` | 搜索特定日期之前的嘟文 |
| `after:<date>` | `after` | `after:2023-01-01` | 搜索特定日期之后的嘟文 |
| `#<tag>` | `tag` | `#golang` | 搜索包含特定标签的嘟文 |

> 不匹配任何语法的词会被拼接到 `text` 作为全文搜索关键词。

### 运维与监控

```go
client := search.GetMeilisearchClient()

// 健康检查（超时 5s）
ok := client.HealthCheck()

// 获取索引统计信息
stats, err := client.GetStats()
// stats 示例：
// {
//   "database_size": 12345678,
//   "last_update":   "2024-01-01T00:00:00Z",
//   "indexes": {
//     "accounts": {"number_of_documents": 100, "is_indexing": false, ...},
//     "statuses": {"number_of_documents": 500, "is_indexing": true,  ...},
//     "hashtags": {"number_of_documents": 50,  "is_indexing": false, ...}
//   }
// }
```

### 辅助函数

```go
// 将任意值序列化为 JSON 字符串
jsonStr := search.ToJSON(doc)

// 安全地将字符串解析为 uint64（解析失败返回 0）
id := search.StringToUint64("12345") // 12345
id = search.StringToUint64("abc")   // 0
```

## 核心类型与函数

### 常量

| 常量 | 值 | 说明 |
|------|----|------|
| `IndexAccounts` | `"accounts"` | 账户索引名称 |
| `IndexStatuses` | `"statuses"` | 嘟文索引名称 |
| `IndexHashtags` | `"hashtags"` | 标签索引名称 |

### 类型

#### `SearchDocument`（接口）

所有搜索文档实现的统一接口：

```go
type SearchDocument interface {
    GetID() string
    GetType() string
}
```

#### `AccountSearchDocument`

账户搜索文档，对应 `accounts` 索引：

| 字段 | 类型 | JSON | 说明 |
|------|------|------|------|
| `ID` | `string` | `id` | 唯一标识（必填） |
| `Username` | `string` | `username` | 用户名（可搜索） |
| `DisplayName` | `string` | `display_name` | 显示名称（可搜索） |
| `Domain` | `string` | `domain` | 域名（可搜索） |
| `Bio` | `string` | `bio` | 个人简介（可搜索） |
| `CreatedAt` | `int64` | `created_at` | 创建时间戳（可过滤、可排序） |

> `GetType()` 返回 `"account"`。

#### `StatusSearchDocument`

嘟文搜索文档，对应 `statuses` 索引：

| 字段 | 类型 | JSON | 说明 |
|------|------|------|------|
| `ID` | `string` | `id` | 唯一标识（必填） |
| `Content` | `string` | `content` | 内容（可搜索） |
| `SpoilerText` | `string` | `spoiler_text` | 内容警告文本（可搜索） |
| `AccountID` | `string` | `account_id` | 作者账户 ID（可过滤） |
| `Username` | `string` | `username` | 作者用户名（可搜索） |
| `Visibility` | `string` | `visibility` | 可见性（可过滤） |
| `CreatedAt` | `int64` | `created_at` | 创建时间戳（可过滤、可排序） |

> `GetType()` 返回 `"status"`。

#### `HashtagSearchDocument`

标签搜索文档，对应 `hashtags` 索引：

| 字段 | 类型 | JSON | 说明 |
|------|------|------|------|
| `ID` | `string` | `id` | 唯一标识（必填） |
| `Name` | `string` | `name` | 标签名（可搜索） |
| `StatusCount` | `int64` | `status_count` | 关联嘟文数（可过滤、可排序） |
| `CreatedAt` | `int64` | `created_at` | 创建时间戳（可过滤、可排序） |

> `GetType()` 返回 `"hashtag"`。

#### `SearchResult`

聚合搜索结果：

| 字段 | 类型 | JSON | 说明 |
|------|------|------|------|
| `Accounts` | `[]string` | `accounts` | 匹配的账户 ID 列表 |
| `Statuses` | `[]string` | `statuses` | 匹配的嘟文 ID 列表 |
| `Hashtags` | `[]string` | `hashtags` | 匹配的标签名称列表 |

#### `MeilisearchClient`

Meilisearch 客户端封装，线程安全（内部使用 `sync.RWMutex` 保护 `enabled` 字段，使用 `sync.Once` 保证单例与初始化只执行一次）。

### 包级函数

| 函数 | 说明 |
|------|------|
| `GetMeilisearchClient() *MeilisearchClient` | 获取客户端单例（`sync.Once` 初始化，启用时自动连接并创建索引） |
| `ParseSearchQuery(query string) (text string, filters map[string]string)` | 解析高级搜索语法，拆分为全文关键词与过滤器映射 |
| `ToJSON(v any) string` | 将任意值序列化为 JSON 字符串 |
| `StringToUint64(s string) uint64` | 安全地将字符串解析为 `uint64`（失败返回 `0`） |

### `MeilisearchClient` 方法

#### 状态与监控

| 方法 | 说明 |
|------|------|
| `IsEnabled() bool` | 检查是否启用（`enabled == true` 且底层客户端非 nil） |
| `HealthCheck() bool` | 健康检查（调用 Meilisearch `/health`，超时 `5s`） |
| `GetStats() (map[string]any, error)` | 获取索引统计信息（文档数、是否正在索引、数据库大小等） |

#### 索引写入

| 方法 | 说明 |
|------|------|
| `IndexAccount(doc *AccountSearchDocument) error` | 索引单个账户文档 |
| `IndexStatus(doc *StatusSearchDocument) error` | 索引单个嘟文文档 |
| `IndexHashtag(doc *HashtagSearchDocument) error` | 索引单个标签文档 |
| `BatchIndexAccounts(docs []*AccountSearchDocument) error` | 批量索引账户（按 `batchSize=1000` 分批） |
| `BatchIndexStatuses(docs []*StatusSearchDocument) error` | 批量索引嘟文（按 `batchSize=1000` 分批） |

> 未启用时所有索引操作静默返回 `nil`，文档 `ID` 为空时返回错误。

#### 索引删除

| 方法 | 说明 |
|------|------|
| `DeleteAccount(id string) error` | 删除账户索引文档 |
| `DeleteStatus(id string) error` | 删除嘟文索引文档 |
| `DeleteHashtag(id string) error` | 删除标签索引文档 |

> 未启用时静默返回 `nil`，`id` 为空时返回错误。

#### 全文搜索

| 方法 | 说明 |
|------|------|
| `SearchAccounts(query string, limit, offset int64, followingIDs []string) ([]string, error)` | 搜索账户，可按关注 ID 列表过滤 |
| `SearchStatuses(query string, limit, offset int64, accountID string, visibility []string) ([]string, error)` | 搜索嘟文，可按作者与可见性过滤，按 `created_at` 倒序 |
| `SearchHashtags(query string, limit, offset int64) ([]string, error)` | 搜索标签，按 `status_count` 倒序 |

> 未启用时返回 `"meilisearch not enabled"` 错误。`limit`/`offset` 会被 `clampLimitOffset` 约束在安全范围内。

## 安全说明

### 过滤器注入防护

Meilisearch 的过滤器使用表达式 DSL（类似 SQL `WHERE`）。本包对所有拼入过滤表达式的 ID 值进行严格校验：

- **`sanitizeID(id string) bool`**：验证 ID 是否为纯数字（`0-9`），拒绝包含空格、字母、特殊字符、SQL 关键字等的注入载荷。
- **`sanitizeIDs(ids []string) []string`**：批量过滤 ID 列表，仅保留合法的数字 ID。
- **`clampLimitOffset(limit, offset int64) (int64, int64)`**：将 `limit` 限制在 `[0, maxSearchLimit=40]`，将负数 `offset` 归零。

校验覆盖范围：

| 搜索方法 | 校验的参数 |
|----------|------------|
| `SearchAccounts` | `followingIDs`（通过 `sanitizeIDs`） |
| `SearchStatuses` | `accountID`（通过 `sanitizeID`）、`visibility` 列表（逐项通过 `sanitizeID`） |
| `SearchHashtags` | 无外部 ID 输入 |

> 若 `followingIDs` 全部非法，`SearchAccounts` 返回空结果；若 `accountID` 非法，`SearchStatuses` 返回 `"invalid account_id"` 错误。

## 依赖关系

| 依赖 | 用途 |
|------|------|
| `github.com/meilisearch/meilisearch-go` | Meilisearch 官方 Go 客户端 |
| `github.com/kasuganosora/community/config` | 读取 `config.C.Other` 配置 |
| `github.com/kasuganosora/community/log` | 日志输出 |

### 外部服务

- **Meilisearch**：需要独立部署的 Meilisearch 实例（v1.12+），默认连 `http://127.0.0.1:7700`。未设置 `MEILISEARCH_ENABLED` 时自动探测：在线就建索引，离线走数据库。请参考 [Meilisearch 官方文档](https://www.meilisearch.com/docs/learn/getting_started/quick_start) 进行安装与配置。Docker Compose 已带上 `meilisearch` 服务。
