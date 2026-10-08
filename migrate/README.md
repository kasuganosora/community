# migrate (数据库迁移)

基于 GORM 的数据库 schema 迁移框架，采用"脚本注册表 + 批次（batch）记录"模式，在应用启动时自动执行未运行的迁移，并提供事务化执行与按批次回滚能力。

## 目录结构

```
migrate/
├── migrate.go              # 迁移引擎：Up/Down、批次管理、事务包裹
├── migrate_test.go         # Migration 结构体单元测试
└── scripts/                # 迁移脚本集合（脚本注册表）
    ├── script.go           # MigrationScript 接口定义与 ScripsType 注册表
    ├── create_init_tables.go
    ├── init_system_config_options.go
    ├── add_roles.go
    ├── add_oauth_tables.go
    ├── activitypub_tables.go
    ├── group.go
    ├── matrix.go
    ├── matrix_hs.go
    ├── media_indexes.go
    ├── performance_indexes.go
    └── relay.go
```

## 核心概念

### `migrations` 表

迁移框架通过一张 `migrations` 表跟踪执行历史，对应结构体定义在 `migrate.go`：

| 字段 | 类型 | 说明 |
|------|------|------|
| `ID` | `int64` | 自增主键 |
| `Name` | `string` | 迁移脚本名（唯一），取自脚本结构体的反射类型名 |
| `Batch` | `int` | 批次号，同一次 `Up()` 中执行的所有脚本共享一个 batch |

表名通过 `Migration.TableName()` 返回 `"migrations"`，首次运行时由 `checkMigrationsTable()` 自动创建。

### `MigrationScript` 接口

`scripts/script.go` 定义了所有迁移脚本必须实现的契约：

```go
type MigrationScript interface {
    Up(db *gorm.DB) error
    Down(db *gorm.DB) error
}
```

### `ScripsType` 注册表

所有脚本在 `scripts/script.go` 的 `ScripsType` 切片中**按顺序**注册。注册顺序即执行顺序：

```go
var ScripsType = []MigrationScript{
    CreateInitTables{},
    InitSystemConfigOptions{},
    AddRoles{},
    AddOAuthTables{},
    CreateActivityPubTablesScript{},
    MigrateGroupTablesScript{},
    MigrateMatrixTablesScript{},
    MigrateMatrixHSScript{},
    MigrateMediaTablesScript{},
    CreatePerformanceIndexesScript{},
    MigrateRelayTableScript{},
    UnifyMediaAttachmentsScript{},
    RepairMatrixHSTablesScript{},
}
```

- `GetScripts()` 返回 `map[脚本名]MigrationScript`（key 为反射类型名）。
- `GetScriptsName()` 返回按注册顺序排列的脚本名切片，用于计算"未执行脚本"。

## 核心功能

### 1. `Up(db)` —— 执行未运行的迁移

执行流程：
1. `checkMigrationsTable()` 确保 `migrations` 表存在。
2. `getUnExecutedMigrationsScript()` 对比注册表与历史记录，得出未执行脚本列表（保持注册顺序）。
3. `getLastBatchNum()` 取当前最大 batch，`batch = lastBatch + 1`。
4. 逐个脚本执行，**每个脚本的 `Up()` 与对应迁移记录的插入被包裹在同一 `db.Transaction` 中**。

事务包裹的意义：
- 若脚本执行成功但记录插入失败（或反之），事务回滚，避免"脚本已执行但未记录"或"记录已写入但脚本未执行"的不一致状态。
- 注意：MySQL 的 DDL 会**隐式提交**，纯 DDL 脚本本身无法完全回滚，但迁移记录的插入仍受事务保护。

### 2. `Down(db)` —— 按批次回滚

执行流程：
1. 取最大 batch 号；若为 0 表示无可回滚迁移，直接返回。
2. 查询该 batch 的全部记录，按 `id DESC` 倒序回滚（保证依赖顺序正确）。
3. 对每条记录：在同一事务中执行 `script.Down(tx)` 并删除迁移记录。

一次 `Down()` 调用只回滚最后一个批次。

### 3. 幂等性保证

所有脚本均设计为可重复执行（idempotent），即使因 DDL 隐式提交导致事务回滚不完全，重复运行 `Up()` 也不会出错：

| 脚本 | 幂等策略 |
|------|----------|
| `CreateInitTables` | `AutoMigrate`（GORM 内部幂等） |
| `InitSystemConfigOptions` | `FirstOrCreate`，逐条按 key 去重 |
| `AddRoles` | `Where("name = ?").FirstOrCreate` |
| `AddOAuthTables` | `HasColumn` / `HasTable` 判断后再添加 |
| `*Tables` (Group/Matrix/Relay) | `CREATE TABLE IF NOT EXISTS` |
| `*Indexes` (Performance/Media) | `CREATE INDEX IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS` |

### 4. 多数据库适配（PostgreSQL / SQLite）

- AutoMigrate 类脚本直接复用模型；MySQL 列类型在 PostgreSQL 上由 `dialect.PostgresDialector` 转换。
- 手写 DDL 的脚本（`group.go`、`matrix.go`、`relay.go`）通过 `ddlFor(conn, mysqlSQL, postgresSQL)` 选择方言，PostgreSQL 版本见 `scripts/postgres_ddl.go`（PostgreSQL 的索引名在 schema 内全局唯一，因此带表名前缀）。
- 原生 SQL 中的标识符使用 `dialect.Quote`，不再写死反引号；`FULLTEXT` 索引仅在 MySQL 上创建。
- `migrate/postgres_test.go` 在空 PostgreSQL 库上执行全部迁移两次，并确认再次 AutoMigrate 不产生 `ALTER`。

### 5. XML 模式适配

所有 MySQL 特有脚本通过 `database.IsXmlMode()` 判断后端，在 sqlexec XML 模式下跳过（sqlexec 不支持 `ENGINE=InnoDB`、`FOREIGN KEY`、`COMMENT`、`FULLTEXT INDEX`、`ALTER TABLE` 等）。脚本同时保证 `Up` 与 `Down` 的跳过行为对称。

## 用法示例

### 应用启动时自动迁移

```go
import (
    "github.com/kasuganosora/community/db"
    "github.com/kasuganosora/community/migrate"
)

// db 包 import 时已自动完成 GORM 初始化
// 执行所有未运行的迁移脚本
if err := migrate.Up(db.GetDB()); err != nil {
    log.Fatal("migration failed: %v", err)
}
```

### 回滚最后一个批次

```go
import (
    "github.com/kasuganosora/community/db"
    "github.com/kasuganosora/community/migrate"
)

// 回滚最近一次 Up() 执行的全部脚本（按 id 倒序）
if err := migrate.Down(db.GetDB()); err != nil {
    log.Fatal("rollback failed: %v", err)
}
```

### 编写新的迁移脚本

1. 在 `scripts/` 下新建文件，定义实现 `MigrationScript` 接口的结构体：

```go
package scripts

import "gorm.io/gorm"

// AddUserAvatars 给 users 表增加 avatars 字段.
// Up 使用幂等写法以支持重复执行.
type AddUserAvatars struct{}

func (AddUserAvatars) Up(db *gorm.DB) error {
    return db.Exec("ALTER TABLE users ADD COLUMN IF NOT EXISTS avatars TEXT").Error
}

func (AddUserAvatars) Down(db *gorm.DB) error {
    return db.Exec("ALTER TABLE users DROP COLUMN IF EXISTS avatars").Error
}
```

2. 在 `ScripsType` 切片末尾追加注册：

```go
var ScripsType = []MigrationScript{
    // ...既有脚本...
    AddUserAvatars{},   // ← 新增
}
```

> 结构体名（此处 `AddUserAvatars`）会作为迁移记录的 `Name`，**重命名已发布的脚本会导致重复执行**。

### 直接调用单脚本（绕过框架）

部分脚本额外暴露了使用全局 `db.GetDB()` 的便捷函数，供非迁移框架场景（如按需补建表）调用：

```go
import "github.com/kasuganosora/community/migrate/scripts"

// 直接创建 ActivityPub 联邦表（幂等）
if err := scripts.CreateActivityPubTables(); err != nil { ... }

// 运行媒体表/索引迁移并建立搜索索引
if err := scripts.RunAllMigrations(); err != nil { ... }

// 创建性能索引 + ANALYZE TABLE
if err := scripts.RunPerformanceMigrations(); err != nil { ... }
```

> 注意：这些便捷函数使用全局 `db.GetDB()`，**不在事务内**，也不会写入 `migrations` 记录表；正式迁移应走 `migrate.Up()`。

## 迁移脚本一览

| 脚本名 | 文件 | 职责 |
|--------|------|------|
| `CreateInitTables` | `create_init_tables.go` | `AutoMigrate` 创建全部核心模型表（User/Account/Status/Notification 等 40+ 张） |
| `InitSystemConfigOptions` | `init_system_config_options.go` | 写入系统配置默认值（站点信息、媒体限额、投票、注册策略、reCAPTCHA 等） |
| `AddRoles` | `add_roles.go` | 创建 `owner` / `admin` / `moderator` 三个内置角色及其权限 |
| `AddOAuthTables` | `add_oauth_tables.go` | 为 `applications` 增加 `client_id`/`client_secret`，新建 `oauth_authorization_codes` 表 |
| `CreateActivityPubTablesScript` | `activitypub_tables.go` | 联邦相关表：`Delivery`、`ProcessedActivity`、`ProxyFollow`、`AccountKey` |
| `MigrateGroupTablesScript` | `group.go` | 群组/联系人：`groups`、`group_members`、`group_messages`、`contacts`、`contact_requests` |
| `MigrateMatrixTablesScript` | `matrix.go` | Matrix 映射表：`matrix_users`、`matrix_rooms`、`matrix_messages` |
| `MigrateMatrixHSScript` | `matrix_hs.go` | 已废弃（Up 为空操作）。Homeserver 表由 `matrix/homeserver` 启动时根据 GORM 模型 AutoMigrate 创建 |
| `MigrateMediaTablesScript` | `media_indexes.go` | 媒体索引、`blurhash`/`focus_x|y` 列、全文搜索索引 |
| `CreatePerformanceIndexesScript` | `performance_indexes.go` | 为各核心表创建常用查询索引 |
| `MigrateRelayTableScript` | `relay.go` | 联邦中继表 `relays` |
| `UnifyMediaAttachmentsScript` | `unify_media_attachments.go` | 统一附件表为 `media_attachments`（迁移 `status_media_attachments` 关联并删除旧表） |
| `RepairMatrixHSTablesScript` | `matrix_hs_repair.go` | 删除旧 `MigrateMatrixHSScript` 用手写 DDL 建的空表（与模型冲突），由 Homeserver 重新 AutoMigrate |

## 配置说明

迁移框架自身无需单独配置，依赖以下外部配置：

| 配置 | 来源 | 用途 |
|------|------|------|
| 数据库连接 | `config.C.DbConnection` 等（见 [db 包文档](../db/README.md)） | 迁移执行所用的 GORM 句柄 |
| `multiStatements=true` | `db/database` 的 MySQL DSN | 支持迁移脚本批量执行多条 DDL |

迁移记录保存在业务数据库内的 `migrations` 表中，不使用独立数据库。

## API 速查

### `migrate` 包

| 函数 | 签名 | 说明 |
|------|------|------|
| `Up` | `(db *gorm.DB) error` | 执行所有未运行迁移（每个脚本事务化，共享同一 batch） |
| `Down` | `(db *gorm.DB) error` | 回滚最后一个 batch 的全部迁移（按 id 倒序，事务化） |

### `migrate/scripts` 包

| 函数 | 签名 | 说明 |
|------|------|------|
| `GetScripts` | `() map[string]MigrationScript` | 脚本名 → 实例映射 |
| `GetScriptsName` | `() []string` | 按注册顺序的脚本名 |
| `CreateActivityPubTables` / `RunActivityPubMigrations` | `() error` | 便捷函数：直接建 AP 表 |
| `MigrateGroupTables` | `() error` | 便捷函数：建群组表 |
| `MigrateMatrixTables` / `DropMatrixTables` | `() error` | 便捷函数：建/删 Matrix 映射表 |
| `MigrateMatrixHS` | `() error` | 便捷函数：Matrix HS 表 |
| `MigrateMediaTables` / `CreateSearchIndexes` / `OptimizeTables` / `RunAllMigrations` | `() error` | 便捷函数：媒体表与索引 |
| `CreatePerformanceIndexes` / `OptimizeDatabase` / `RunPerformanceMigrations` | `() error` | 便捷函数：性能索引与表优化 |
| `MigrateRelayTable` | `() error` | 便捷函数：建中继表 |

## 依赖关系

| 依赖 | 用途 |
|------|------|
| [`gorm.io/gorm`](https://gorm.io) | `*gorm.DB`、`Migrator`、事务 |
| [`github.com/pkg/errors`](https://github.com/pkg/errors) | `errors.Wrap` 错误包装 |
| `github.com/kasuganosora/community/db` | 全局 `db.GetDB()`（便捷函数使用） |
| `github.com/kasuganosora/community/db/database` | `database.IsXmlMode()` 后端判断 |
| `github.com/kasuganosora/community/models` | `AutoMigrate` 的模型定义 |
| `github.com/kasuganosora/community/systemsettings` | 系统配置键常量 |
| `github.com/kasuganosora/community/activitypub` | AP 联邦模型 |
| `github.com/kasuganosora/community/log` | 日志输出 |

## 注意事项

- **事务范围**：`Up()` / `Down()` 会把脚本执行与迁移记录写入包裹在事务中；脚本内部应使用传入的 `tx`（可能是事务句柄）而非 `db.GetDB()`，否则事务无法回滚。
- **MySQL DDL 隐式提交**：`ALTER`/`CREATE TABLE` 等 DDL 在 MySQL 中会触发隐式提交，纯 DDL 脚本即使事务失败也无法撤销已应用的 schema 变更；迁移脚本因此都被设计为幂等，重复 `Up()` 安全。
- **脚本命名不可变**：迁移记录以结构体的反射类型名作为唯一标识；重命名已发布的脚本会使其被视为新脚本而被重复执行。
- **PostgreSQL 事务**：PostgreSQL 中任何语句出错都会使当前事务失效（后续语句报 `25P02`），迁移脚本不能依赖“先执行、出错再忽略”，应先用 `HasTable` / `HasColumn` / `HasIndex` 判断。
- **XML 模式**：sqlexec XML 后端不支持 `ALTER TABLE`、`FULLTEXT INDEX`、`ENGINE=InnoDB` 等 MySQL DDL，相关脚本会在 `IsXmlMode()` 下自动跳过。
- **`Down` 的 no-op 设计**：`performance_indexes` 与 `media_indexes` 的 `Down()` 为 no-op——索引与新增列属非破坏性变更，按名 drop 索引在跨 DB 版本时脆弱，且重跑 `Up()` 本身幂等。
