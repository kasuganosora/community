# db (数据库连接层)

统一管理应用数据存储连接的基础设施模块，对外暴露 GORM 数据库句柄与多实例 Redis 客户端，支持 MySQL、PostgreSQL、SQLite 与基于 sqlexec 的 XML 持久化四种后端。

## 目录结构

| 路径 | package | 职责 |
|------|---------|------|
| `db.go` | `db` | 对外门面（facade），聚合 `database` 与 `redis` 子包，在 `init()` 中触发 GORM 初始化 |
| `database/` | `database` | GORM 初始化、连接池管理、测试模式、sqlexec XML 适配 |
| `dialect/` | `dialect` | 方言差异小工具（标识符引用、ILIKE、JSON 取值等）与 PostgreSQL dialector |
| `redis/` | `redis` | 多实例 Redis 客户端管理与测试工具 |

## 支持的后端

| 后端 | `DbConnection` 取值 | 说明 |
|------|---------------------|------|
| MySQL | `mysql` | 生产环境推荐，支持完整 DDL/索引/外键 |
| PostgreSQL | `postgres`（亦接受 `postgresql`、`pgsql`） | 基于 pgx；模型中的 MySQL 列类型由 `dialect.PostgresDialector` 自动转换 |
| SQLite | `sqlite` | 轻量本地存储，启用 WAL 模式 |
| XML (sqlexec) | `xml` | 基于 [sqlexec](https://github.com/kasuganosora/sqlexec) 的 XML 文件持久化，适配嵌入式/测试场景 |

## 核心功能

### 1. GORM 初始化与句柄管理

- 在 `db` 包 `init()` 时自动调用 `database.InitGorm()` 完成 GORM 连接建立。
- 通过 `sync.RWMutex` 保护全局 `DB` 句柄，所有访问均并发安全。
- 幂等初始化：`DB != nil` 时跳过，避免重复连接导致连接泄漏。
- 失败路径会通过 `closeGormSilently()` 进行 best-effort 清理。

### 2. 连接池配置

通过 `DBConfig` 结构体统一管理连接池参数，MySQL 与 SQLite 后端分别采用不同默认策略：

| 参数 | MySQL 默认 | SQLite 默认 | 说明 |
|------|-----------|-------------|------|
| `MaxOpenConns` | 100 | 1 | 最大连接数（SQLite 写连接仅 1） |
| `MaxIdleConns` | 10 | 1 | 最大空闲连接数 |
| `ConnMaxLifetime` | 30 min | 1 h | 连接最大生命周期 |
| `ConnMaxIdleTime` | 10 min | — | 空闲连接最大生命周期 |

SQLite 后端额外启用以下 PRAGMA 优化：
- `journal_mode=WAL` —— 避免 `SQLITE_BUSY`
- `busy_timeout=5000`
- `synchronous=NORMAL`
- `cache_size=-64000`（64MB）
- `temp_store=MEMORY`

### 3. sqlexec XML 适配

当 `DbConnection == "xml"` 时，使用 `xml_engine.go` 中的 `xmlDialectorWrapper` / `xmlMigratorWrapper` 包装 GORM dialector：

- 建表时自动追加 `ENGINE=xml`，从 GORM schema 生成 `CREATE TABLE IF NOT EXISTS`。
- `AddColumn` / `DropColumn` / `AlterColumn` 为 no-op（sqlexec 不支持 `ALTER TABLE`）。
- `IsXmlMode()` 供迁移脚本判断后端类型，跳过 MySQL 特有 DDL。

### 4. Redis 多实例管理

- `redis.go` 在 `init()` 中按 `REDIS_%d_HOST` 序号顺序自动加载多实例 Redis 客户端。
- 第 0 个实例从 `config.C.Redis0Host/Port/Password/Db` 读取，其余从 `config.C.Other` map 读取。
- 索引越界或未配置任何实例时 `panic`。

### 5. 测试模式

为单元测试提供注入点，避免触发真实数据库连接：

- `detectTestMode()` 通过 `os.Args` 的 `-test.*` 参数或 `GO_TEST_MODE=1` 环境变量自动识别。
- `SetTestMode()` / `SetTestDB()` / `ResetTestDB()` 显式控制测试态。
- `IsTestMode()` 判断当前是否处于测试模式。
- Redis 子包提供 `SetRedisesForTest()` / `GetRedisesForTest()` 注入 mock 客户端。

## 配置说明

### 数据库配置（`config.C` 字段）

```bash
# 后端类型: mysql / sqlite / xml
DB_CONNECTION=mysql

# MySQL
DB_HOST=127.0.0.1
DB_PORT=3306
DB_USERNAME=root
DB_PASSWORD=secret
DB_DATABASE=community

# PostgreSQL（DB_PORT 未显式设置时使用 5432）
DB_CONNECTION=postgres
DB_HOST=127.0.0.1
DB_USERNAME=community
DB_PASSWORD=secret
DB_DATABASE=community
DB_SSLMODE=disable          # 可选：disable / require / verify-full …
# DB_DSN=postgres://user:pass@host:5432/community?sslmode=require  # 可选：完整 DSN，覆盖上面的 DB_* 字段

# SQLite（DbHost 为数据库文件路径）
DB_CONNECTION=sqlite
DB_HOST=./community.db

# XML（DbHost 为数据目录，DbDatabase 为数据库名）
DB_CONNECTION=xml
DB_HOST=./database
DB_DATABASE=community
```

### 连接池（环境变量覆盖）

```bash
# 对 MySQL 与 PostgreSQL 生效；非法或 ≤0 的值会被忽略并回退默认值
DB_MAX_OPEN_CONNS=100
DB_MAX_IDLE_CONNS=10
DB_CONN_MAX_LIFETIME_MINUTES=30
DB_CONN_MAX_IDLE_TIME_MINUTES=10
```

### Redis 配置

```bash
# 第 0 个实例（固定字段）
REDIS_0_HOST=127.0.0.1
REDIS_0_PORT=6379
REDIS_0_PASSWORD=
REDIS_0_DB=0

# 第 1、2 … 个实例（通过 Other map 读取，未配置即停止加载）
REDIS_1_HOST=127.0.0.1
REDIS_1_PORT=6380
REDIS_1_PASSWORD=
REDIS_1_DB=0
```

### 开发模式

```bash
# DevMode=true 时启用 GORM Debug 日志（打印 SQL）
DEV_MODE=true
```

## 用法示例

### 获取 GORM 句柄

```go
import "github.com/kasuganosora/community/db"

// db 包在 import 时已自动调用 database.InitGorm()
// 直接获取并发安全的 GORM 句柄
gdb := db.GetDB()

var user models.User
gdb.First(&user, 1)
```

### 获取 Redis 客户端

```go
import "github.com/kasuganosora/community/db"

// 不传参：返回第 0 个实例
rdb := db.GetRedis()
rdb.Set(ctx, "key", "value", 0)

// 传索引：返回第 N 个实例
rdb1 := db.GetRedis(1)
```

### 健康检查与统计

```go
import "github.com/kasuganosora/community/db/database"

// 探活
if err := database.PingDB(); err != nil {
    log.Fatal("database unreachable: %v", err)
}

// 连接池统计信息（用于 metrics / 健康端点）
stats, err := database.GetDBStats()
// stats = {
//   "open_connections": ..., "in_use": ..., "idle": ...,
//   "wait_count": ..., "wait_duration_ms": ...,
//   "max_idle_closed": ..., "max_lifetime_closed": ...
// }

// 优雅关闭
defer func() { _ = database.CloseDB() }()
```

### 判断后端类型

```go
import "github.com/kasuganosora/community/db/database"

if database.IsXmlMode() {
    // 跳过 MySQL 特有 DDL（CREATE INDEX、FULLTEXT、ENGINE=InnoDB …）
}
```

### 自定义连接池配置

```go
import "github.com/kasuganosora/community/db/database"

// 直接使用默认配置
cfg := database.DefaultDBConfig()

// 或从环境变量加载（非法值会被忽略）
cfg = database.LoadDBConfig()
```

### 测试中注入 mock DB

```go
import "github.com/kasuganosora/community/db/database"

func TestSomething(t *testing.T) {
    database.ResetTestDB()           // 清理状态
    database.SetTestDB(mockGormDB)   // 注入 mock，同时启用测试模式
    defer database.ResetTestDB()

    // ... 调用业务代码，不会连真实数据库 ...
}
```

```go
import cRedis "github.com/kasuganosora/community/db/redis"

func TestRedisLogic(t *testing.T) {
    saved := cRedis.GetRedisesForTest()
    cRedis.SetRedisesForTest([]*redis.Client{miniredisClient})
    defer cRedis.SetRedisesForTest(saved)
}
```

## API 速查

### `db` 包（门面）

| 函数 | 签名 | 说明 |
|------|------|------|
| `GetDB` | `() *gorm.DB` | 返回全局 GORM 句柄 |
| `GetRedis` | `(connNum ...int) *redis.Client` | 返回指定索引的 Redis 客户端，缺省为 0 |

### `database` 包

| 函数 | 签名 | 说明 |
|------|------|------|
| `InitGorm` | `()` | 初始化 GORM（由 `db.init()` 自动调用） |
| `GetDB` | `() *gorm.DB` | 并发安全获取 GORM 句柄 |
| `CloseDB` | `() error` | 关闭并清空全局句柄 |
| `PingDB` | `() error` | 探活 |
| `GetDBStats` | `() (map[string]any, error)` | 连接池统计 |
| `IsXmlMode` | `() bool` | 是否处于 sqlexec XML 模式 |
| `DefaultDBConfig` | `() DBConfig` | 默认连接池配置 |
| `LoadDBConfig` | `() DBConfig` | 从环境变量加载连接池配置 |
| `SetTestMode` / `IsTestMode` | — | 测试模式控制 |
| `SetTestDB` / `ResetTestDB` | — | 注入/重置测试 DB |

### `redis` 包

| 函数 | 签名 | 说明 |
|------|------|------|
| `GetRedis` | `(connNum ...int) *redis.Client` | 返回指定索引客户端 |
| `GetRedises` | `() []*redis.Client` | 返回全部客户端 |
| `SetRedisesForTest` / `GetRedisesForTest` | — | 测试注入工具 |

## 依赖关系

| 依赖 | 用途 |
|------|------|
| [`gorm.io/gorm`](https://gorm.io) | ORM 框架与 Migrator |
| [`gorm.io/driver/mysql`](https://github.com/go-gorm/mysql) | MySQL 驱动 |
| [`gorm.io/driver/postgres`](https://github.com/go-gorm/postgres) | PostgreSQL 驱动（pgx） |
| [`github.com/glebarez/sqlite`](https://github.com/glebarez/sqlite) | 纯 Go SQLite 驱动（CGO-free） |
| [`github.com/redis/go-redis/v9`](https://github.com/redis/go-redis) | Redis 客户端 |
| [`github.com/kasuganosora/sqlexec`](https://github.com/kasuganosora/sqlexec) | XML 持久化引擎 |
| `github.com/kasuganosora/community/config` | 读取 `config.C` 配置 |
| `github.com/kasuganosora/community/log` | 日志输出 |

## 注意事项

- **并发安全**：所有对全局 `DB` 的读写均由 `dbMu sync.RWMutex` 保护，业务代码应始终通过 `GetDB()` 而非直接访问 `DB` 变量。
- **multiStatements**：MySQL DSN 显式开启 `multiStatements=true`，以支持迁移脚本中的批量 DDL；这些语句不拼接任何用户输入，不存在注入风险。
- **`InitGorm` 的 Fatal 行为**：致命配置错误（如无效 DSN、不支持的连接类型）会通过 `log.Fatal` 终止进程，因此 `Fatal` 在解锁互斥锁之后才执行，避免 `exitAfterDefer`。
- **PostgreSQL 差异**：
  - 模型保留 MySQL 列类型标签（`tinyint`、`datetime`、`json`、`longtext` …），由 `PostgresDialector.DataTypeOf` 转为 `smallint`、`timestamptz`、`jsonb`、`text` 等，因此 MySQL 现有表结构不受影响。
  - 需要 `citext` 扩展（PostgreSQL 13+ 为受信任扩展，数据库 owner 可创建；启动时自动执行 `CREATE EXTENSION IF NOT EXISTS citext`，无权限时请由超级用户预先创建）。`accounts.username`、`accounts.acct`、`accounts.domain`、`users.username`、`tags.name` 使用 `citext`，与 MySQL `_ci` 排序规则一样大小写不敏感（含唯一索引）。
  - 会话时区跟随进程本地时区（与 MySQL DSN 的 `loc=Local` 一致）；使用 `DB_DSN` 时需自行设置 `TimeZone`。
  - 写入前会去掉字符串中的 NUL 字节（PostgreSQL `text` 无法存储 U+0000）。
  - 原生 SQL 中的方言差异请使用 `db/dialect`：`dialect.Quote`、`dialect.ILike` + `dialect.LikeEscape`（其余文本列在 PostgreSQL 上大小写敏感）、`dialect.JSONText`；保留字列名优先使用 `clause.Eq` / `map[string]any` 条件。
  - `FULLTEXT` 索引与 `OPTIMIZE TABLE` 仅在 MySQL 上执行。
- **Redis 未配置**：若没有任何 Redis 实例被加载，`GetRedis()` 会 `panic("no redis config")`，调用方需确保配置就绪。
