# tests (测试基础设施)

为整个项目提供统一的测试基础设施，包括内存数据库（DB）mock、Redis mock、测试 fixture 生成器以及 HTTP 端点的集成测试。所有测试资源均通过 `t.Cleanup` 自动回收，支持并行测试。

## 核心功能

- **内存数据库**：基于 SQLExec MVCC 内存数据源，每个测试可拥有独立隔离的数据库实例，无需外部 MySQL。
- **Redis mock / real 双模式**：单元测试使用 `redismock`，集成测试连接真实 Redis，支持自动探测回退。
- **统一测试设置（`TestSetup`）**：一站式组合 DB、Redis 与 fixture，通过 functional options 灵活配置。
- **Fixture 生成器**：快速创建 `User`、`Account`、`Status`、`Follow`、`Block`、`AuthToken`、`Role`、`Filter`、`Poll`、`Attachment`、`Notification` 等模型测试数据。
- **集成测试**：基于 `httptest` 与 `gin.TestMode` 对 OAuth、Media、Search 等 API 端点进行结构化验证。

## 架构 / 结构

```
tests/
├── integration/                  # 集成测试
│   └── oauth_integration_test.go # OAuth / Media / Search API 端点结构化验证
└── testutil/                     # 测试工具库
    ├── env.go                    # 测试配置（.env.test 加载、Redis 连接参数）
    ├── db_mock.go                # 内存数据库 TestDB（isolated / shared 两种模式）
    ├── redis_mock.go             # Redis mock / real / auto 三种模式
    ├── redis_helper.go           # Redis 可用性检查与连接信息
    ├── fixture.go                # FixtureBuilder 测试数据生成器
    └── setup.go                  # TestSetup 统一测试设置（DB + Redis + Fixtures）
```

### `integration/` — 集成测试

存放跨层（controller → service → db）的端到端集成测试。当前包含：

- `TestOAuthIntegration`：验证 OAuth 各授权流程（CreateApp、Authorize、Password Grant、Refresh Token Grant、Client Credentials Grant）的请求结构。
- `TestMediaIntegration`：验证 Media 上传 / 获取 / 删除端点。
- `TestSearchIntegration`：验证 v2 搜索（accounts / statuses / hashtags）端点。

> 通过 `gin.SetMode(gin.TestMode)` 初始化，使用 `httptest.NewRequest` 构造请求，不依赖真实服务器。

### `testutil/` — 测试工具库

可被任意包导入的测试辅助库，导入路径：

```
github.com/kasuganosora/community/tests/testutil
```

## 用法示例

### 完整集成测试（推荐）

使用 `FullSetup` 一键创建 DB、Redis 与 fixture，所有资源通过 `t.Cleanup` 自动释放：

```go
import (
    "testing"

    "github.com/kasuganosora/community/models"
    "github.com/kasuganosora/community/tests/testutil"
)

func TestSomething(t *testing.T) {
    setup := testutil.FullSetup(t, []any{
        &models.User{}, &models.Account{}, &models.Status{},
    })

    // 创建测试数据
    user := setup.Fixtures.CreateUser(func(u *models.User) {
        u.UserName = "alice"
    })
    account := setup.Fixtures.CreateAccount(user)
    status := setup.Fixtures.CreateStatus(account)

    // 使用 setup.GormDB() 进行查询测试
    // 使用 setup.RedisClient() 进行缓存测试
}
```

### 仅使用数据库

```go
func TestDBOnly(t *testing.T) {
    setup := testutil.DBOnlySetup(t, &models.User{}, &models.Account{})
    // setup.RedisClient() 返回 mock Redis（不会连接真实实例）
    db := setup.GormDB()

    // ... 测试逻辑
}
```

或使用快速构造函数：

```go
func TestQuickDB(t *testing.T) {
    db := testutil.QuickDBSetup(t, &models.User{})
    defer db.Close()

    // db.GormDB 即可直接使用
}
```

### 隔离 / 共享数据库模式

```go
// 隔离模式（默认，支持并行测试）
setup := testutil.NewTestSetup(t, testutil.WithIsolatedDB(), testutil.WithModels(&models.User{}))

// 共享模式（性能更好，但不支持并行测试）
setup := testutil.NewTestSetup(t, testutil.WithSharedDB())
```

### Redis 设置

```go
// 强制 mock（单元测试）
setup := testutil.NewTestSetup(t, testutil.WithMockRedis())

// 强制真实 Redis（不可用时跳过测试）
setup := testutil.NewTestSetup(t, testutil.WithRealRedis())

// 自动探测（可用则真实，否则 mock）
setup := testutil.NewTestSetup(t, testutil.WithRedisMode(testutil.RedisModeAuto))
```

独立的 Redis 快捷构造函数：

```go
// mock Redis（通过 Mock 字段设置预期）
client := testutil.QuickMockRedisSetup(t)
client.Mock.ExpectGet("key").SetVal("value")

// 真实 Redis（不可用时跳过）
client := testutil.QuickRealRedisSetup(t)

// 自动探测
client := testutil.QuickRedisSetup(t)

// 获取底层 *redis.Client
rdb := client.RedisClient()
```

### Fixture 生成器

所有 fixture 工厂方法均接受可选的 `override` 函数，用于定制字段：

```go
setup := testutil.FullSetup(t, []any{&models.User{}, &models.Account{}})

// 创建用户并覆盖默认值
user := setup.Fixtures.CreateUser(func(u *models.User) {
    u.UserName = "bob"
    u.EmailHash = "custom_hash"
})

// 创建完整测试链路：user -> account -> status
u, acc, st := setup.Fixtures.FullTestSetup()

// 其他可用 fixture
setup.Fixtures.CreateFollow(follower, following)
setup.Fixtures.CreateBlock(blocker, blocked)
setup.Fixtures.CreateAuthToken(user.Id)
setup.Fixtures.CreateRole()
setup.Fixtures.CreateFilter(accountId)
setup.Fixtures.CreatePoll(statusId, accountId)
setup.Fixtures.CreateAttachment()
setup.Fixtures.CreateNotification(accountId)
```

### 集成测试示例

```go
package integration

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/gin-gonic/gin"
    "github.com/stretchr/testify/assert"
)

func TestOAuthIntegration(t *testing.T) {
    w := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(w)
    c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/apps", nil)

    assert.NotNil(t, c.Request)
    assert.Equal(t, http.MethodPost, c.Request.Method)
}
```

## 核心类型与函数

### 类型

#### `TestSetup`（统一测试设置）

聚合 DB、Redis 与 fixture 的测试环境，通过 `NewTestSetup` 创建：

| 字段 | 类型 | 说明 |
|------|------|------|
| `DB` | `*TestDB` | 内存数据库实例 |
| `Redis` | `*RedisTestClient` | Redis 测试客户端 |
| `Fixtures` | `*FixtureBuilder` | 测试数据生成器 |

#### `TestDB`（测试数据库）

| 字段 | 类型 | 说明 |
|------|------|------|
| `DB` | `*api.DB` | SQLExec DB 实例 |
| `GormDB` | `*gorm.DB` | GORM DB 实例 |
| `Session` | `*api.Session` | SQLExec Session |

#### `DBMode`（数据库模式）

| 常量 | 值 | 说明 |
|------|----|------|
| `ModeIsolated` | `0` | 每个测试独立数据库（默认，支持并行） |
| `ModeShared` | `1` | 共享数据库，测试间清理数据（性能更好） |

#### `RedisTestMode`（Redis 模式）

| 常量 | 值 | 说明 |
|------|----|------|
| `RedisModeMock` | `0` | 使用 redismock（无需真实 Redis） |
| `RedisModeReal` | `1` | 连接真实 Redis（不可用则跳过测试） |
| `RedisModeAuto` | `2` | 自动探测：可用则真实，否则 mock |

#### `RedisTestClient`（Redis 测试客户端）

| 字段 | 类型 | 说明 |
|------|------|------|
| `Client` | `*redis.Client` | Redis 客户端 |
| `Mock` | `redismock.ClientMock` | Mock 句柄（真实模式下为 nil） |

#### `FixtureBuilder`（测试数据生成器）

| 方法 | 说明 |
|------|------|
| `CreateUser(overrides ...)` | 创建测试用户 |
| `CreateAccount(user, overrides ...)` | 创建测试账户 |
| `CreateStatus(account, overrides ...)` | 创建测试嘟文 |
| `CreateFollow(follower, following, overrides ...)` | 创建关注关系 |
| `CreateBlock(blocker, blocked, overrides ...)` | 创建屏蔽关系 |
| `CreateAuthToken(userId, overrides ...)` | 创建认证 token |
| `CreateRole(overrides ...)` | 创建角色 |
| `CreateFilter(accountId, overrides ...)` | 创建过滤器 |
| `CreatePoll(statusId, accountId, overrides ...)` | 创建投票 |
| `CreateAttachment(overrides ...)` | 创建附件 |
| `CreateNotification(accountId, overrides ...)` | 创建通知 |
| `FullTestSetup()` | 一键创建 user + account + status 完整链路 |

### 函数

#### 设置函数

| 函数 | 说明 |
|------|------|
| `NewTestSetup(t, opts ...SetupOption) *TestSetup` | 创建统一测试设置（推荐入口） |
| `FullSetup(t, models, opts ...) *TestSetup` | 完整设置：DB + Redis + Fixtures |
| `DBOnlySetup(t, models ...) *TestSetup` | 仅数据库（Redis 为 mock） |
| `RedisOnlySetup(t, mode) *TestSetup` | 仅 Redis |
| `QuickDBSetup(t, models ...) *TestDB` | 快速创建隔离数据库 |
| `QuickRedisSetup(t) *RedisTestClient` | 快速创建 Redis（自动探测） |
| `QuickMockRedisSetup(t) *RedisTestClient` | 快速创建 mock Redis |
| `QuickRealRedisSetup(t) *RedisTestClient` | 快速创建真实 Redis |

#### SetupOption（functional options）

| 函数 | 说明 |
|------|------|
| `WithDBMode(mode)` | 设置数据库模式 |
| `WithRedisMode(mode)` | 设置 Redis 模式 |
| `WithModels(models ...)` | 设置自动迁移的模型 |
| `WithRealRedis()` | 强制真实 Redis |
| `WithMockRedis()` | 强制 mock Redis |
| `WithSharedDB()` | 使用共享数据库 |
| `WithIsolatedDB()` | 使用隔离数据库 |

#### 配置函数

| 函数 | 说明 |
|------|------|
| `GetTestConfig() *TestConfig` | 获取测试配置（从 `.env.test` 或环境变量加载，`sync.Once` 初始化） |
| `IsRedisAvailable() bool` | 检查 Redis 是否可用 |
| `GetRedisAddr() string` | 获取 Redis 测试地址 |
| `FlushTestRedis(t, client)` | 清空测试 Redis 数据库 |

#### 辅助函数

| 函数 | 说明 |
|------|------|
| `RandomString(length) string` | 生成随机字符串（基于 `crypto/rand`） |
| `PtrString(s) *string` | 字符串取指针 |
| `PtrInt(i) *int` | int 取指针 |
| `PtrInt64(i) *int64` | int64 取指针 |
| `PtrTime(t) *time.Time` | time 取指针 |

## 测试配置

测试配置从项目根目录的 `.env.test` 文件加载（不存在则回退到环境变量和默认值）。`.env.test` 已被 git 忽略，请从 `.env.test.example` 复制，不要提交真实密码：

```bash
# Redis 测试连接（默认使用 DB 15）
REDIS_TEST_HOST=127.0.0.1
REDIS_TEST_PORT=6379
REDIS_TEST_PASSWORD=
REDIS_TEST_DB=15
```

> 环境变量优先级高于 `.env.test`；数据库（DB）使用 SQLExec 内存引擎，无需外部配置。

## 依赖关系

| 依赖 | 用途 |
|------|------|
| [`github.com/redis/go-redis/v9`](https://github.com/redis/go-redis) | Redis 客户端 |
| [`github.com/go-redis/redismock/v9`](https://github.com/go-redis/redismock) | Redis mock |
| [`github.com/kasuganosora/sqlexec`](https://github.com/kasuganosora/sqlexec) | 内存 SQL 执行引擎（MVCC） |
| [`gorm.io/gorm`](https://gorm.io) | ORM |
| [`github.com/google/uuid`](https://github.com/google/uuid) | 生成唯一数据源名称与 UUID |
| [`github.com/gin-gonic/gin`](https://github.com/gin-gonic/gin) | HTTP 测试上下文（集成测试） |
| [`github.com/stretchr/testify`](https://github.com/stretchr/testify) | 断言库 |
| `github.com/kasuganosora/community/models` | 数据模型（fixture 依赖） |
| `github.com/kasuganosora/community/db/redis` | 全局 Redis 客户端池注入 |
| `github.com/kasuganosora/community/utils` | 随机字符串生成 |

> `integration/` 包内的测试为 `package integration`，不对外导出可复用函数，仅作为端到端测试集合存在。
