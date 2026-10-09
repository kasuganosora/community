# cmd (命令行入口)

Community 服务的命令行入口集合，包含主服务进程、Matrix 联邦测试工具和兼容 Mastodon `tootctl` 的管理命令行工具。

## 目录结构

```
cmd/
├── community/            # 主服务入口（HTTP API 服务器）
│   └── main.go
├── complement/           # Matrix homeserver 测试入口（Complement 兼容）
│   └── main.go
└── tootctl/              # 管理命令行工具（Mastodon-compatible CLI）
    ├── main.go
    └── commands/         # 各子命令实现
        ├── cache.go
        ├── domains.go
        ├── email_domain_blocks.go
        ├── emoji.go
        ├── feeds.go
        ├── media.go
        ├── media_storage.go    # 孤立文件扫描、引用集合、按前缀统计用量
        ├── media_orphans_test.go
        ├── media_security_test.go
        ├── self_destruct.go
        └── self_destruct_test.go
```

| 子目录 | 说明 |
|--------|------|
| `community/` | 主服务进程入口，负责初始化日志、数据库迁移、缓存、Matrix 桥接与 homeserver，装配 Gin 中间件链并启动 HTTP 服务器（带优雅关闭） |
| `complement/` | 为 [Complement](https://github.com/matrix-org/complement) 测试框架提供专用的 Matrix homeserver 入口，自动迁移表结构并注册全部 Matrix Client-Server / Federation API 路由 |
| `tootctl/` | 兼容 Mastodon 的管理命令行工具，基于 `urfave/cli/v2` 构建，提供账号、媒体、缓存、Feed、域名等运维管理能力 |

## 核心功能

### 1. community（主服务）

主服务进程的启动流程：

```
flag.Parse()
    │
    ├── initLogger()          # 初始化日志（开发模式 debug，生产模式 info + 文件输出）
    │
    ├── migrate.Up(db)        # 数据库迁移（或 --rollback 回滚）
    │
    ├── cache.Init()          # 初始化缓存
    │
    ├── matrix.Init()         # 初始化 Matrix Bridge
    │
    ├── homeserver.InitHomeServer()  # 初始化完整 Matrix 服务器
    │
    ├── setupServer()         # 装配 Gin 引擎 + 中间件 + 路由
    │
    └── startServer()         # 启动服务器（带优雅关闭）
```

**关键函数：**

| 函数 | 作用 |
|------|------|
| `main()` | 程序入口，编排启动流程；支持 `--rollback` 参数执行数据库回滚 |
| `initLogger()` | 根据开发模式配置日志级别和输出目标（stdout / 文件） |
| `setupServer()` | 创建 Gin 引擎，注册中间件链、路由、健康检查与指标端点 |
| `setupFrontend()` | 配置前端静态文件服务，实现 SPA fallback（对非 API 路径返回 `index.html`） |
| `startServer()` | 使用 `server` 包创建并启动带优雅关闭的 HTTP 服务器 |

**内置中间件链（执行顺序）：**

| 中间件 | 作用 |
|--------|------|
| `gin.Recovery()` | Panic 恢复 |
| `SecurityHeadersMiddleware()` | 安全响应头 |
| `CORSMiddleware(origins)` | 跨域资源共享（从配置读取允许来源） |
| `TimeoutMiddleware(30s)` | 请求超时控制 |
| `RequestLogMiddleware()` | 请求日志记录 |
| `MetricsMiddleware()` | Prometheus 指标采集 |
| `ErrorHandler()` | 统一错误处理 |

**内置端点：**

| 端点 | 方法 | 说明 |
|------|------|------|
| `/health` | GET | 简单健康检查 |
| `/health/detailed` | GET | 详细健康检查 |
| `/live` | GET | 存活探针（Liveness） |
| `/ready` | GET | 就绪探针（Readiness） |
| `/metrics` | GET | Prometheus 指标端点 |
| `/admin/logs/stats` | GET | 日志统计（需管理员权限） |
| `/admin/logs/health` | GET | 日志健康（需管理员权限） |

### 2. complement（Matrix 测试工具）

为 Complement 测试框架提供的专用入口，与主服务不同，它：

- 仅初始化核心认证表和 Matrix homeserver 表结构（通过 `AutoMigrate`）
- 注册**全部** Matrix Client-Server API 和 Federation API 路由
- 同时监听两个端口：HTTP `:8008`（Client API）和 HTTPS `:8448`（Federation）
- 使用 `gin.ReleaseMode`，不带前端静态文件服务

**注册的 Matrix 路由模块：**

| 函数 | 说明 |
|------|------|
| `homeserver.RegisterRoutes()` | 核心 Client-Server API |
| `RegisterFederationRoutes()` | Federation API |
| `RegisterBackfillRoutes()` | 历史消息回填 |
| `RegisterAdditionalFederationRoutes()` | 扩展 Federation API |
| `RegisterFederationE2EERoutes()` | 端到端加密 Federation |
| `RegisterRelationsRoutes()` | 事件关系（Relations） |
| `RegisterSpacesRoutes()` | Spaces |
| `RegisterAppServiceRoutes()` | AppService |
| `RegisterRoomUpgradeRoutes()` | 房间升级 |
| `RegisterKeySharingRoutes()` | 密钥共享 |
| `RegisterBatchSendRoutes()` | 批量发送（历史导入） |
| `RegisterAsyncMediaRoutes()` | 异步媒体 |

### 3. tootctl（管理命令行工具）

兼容 Mastodon `tootctl` 风格的管理 CLI，基于 [`urfave/cli/v2`](https://github.com/urfave/cli) 构建。

**顶层命令一览：**

| 命令 | 入口函数 | 说明 |
|------|----------|------|
| `self-destruct` | `SelfDestructCommand()` | 从联邦中擦除本服务器（广播 Delete 活动） |
| `version` | `VersionCommand(version)` | 显示版本、域名和数据库信息 |
| `accounts` | `AccountsCommand()` | 账号管理（create / delete / modify） |
| `cache` | `CacheCommand()` | 缓存管理（clear / recount） |
| `domains` | `DomainsCommand()` | 域名管理（purge / crawl） |
| `email-domain-blocks` | `EmailDomainBlocksCommand()` | 邮箱域名封锁（list / add / remove） |
| `emoji` | `EmojiCommand()` | 自定义表情（export / import / purge） |
| `feeds` | `FeedsCommand()` | Feed 管理（build / clear） |
| `media` | `MediaCommand()` | 媒体文件管理（remove / remove-orphans / refresh / usage / lookup） |
| `statuses` | `StatusesCommand()` | 帖子管理（remove） |

**子命令详情：**

#### cache

| 子命令 | 说明 | 常用 Flag |
|--------|------|-----------|
| `cache clear` | 清空 Redis 缓存（feed、timeline、status、account 等） | — |
| `cache recount TYPE` | 重新统计硬缓存计数器（`accounts` / `statuses`） | `--concurrency`, `--verbose` |

#### domains

| 子命令 | 说明 | 常用 Flag |
|--------|------|-----------|
| `domains purge DOMAIN[...]` | 彻底删除指定域名的所有账号及其关联数据 | `--concurrency`, `--dry-run`, `--by-uri`, `--limited-federation-mode` |
| `domains crawl [START]` | 爬取已知联邦宇宙并收集统计信息 | `--concurrency`, `--format`, `--exclude-suspended` |

#### email-domain-blocks

| 子命令 | 说明 | 常用 Flag |
|--------|------|-----------|
| `email-domain-blocks list` | 列出所有已封锁的邮箱域名 | — |
| `email-domain-blocks add DOMAIN[...]` | 添加邮箱域名封锁 | `--with-dns-records` |
| `email-domain-blocks remove DOMAIN[...]` | 移除邮箱域名封锁 | — |

#### emoji

| 子命令 | 说明 | 常用 Flag |
|--------|------|-----------|
| `emoji export PATH` | 导出自定义表情为 `.tar.gz` | `--overwrite`, `--category` |
| `emoji import PATH` | 从 `.tar.gz` 导入自定义表情（经 emojiasset 校验后写入 `emojis/<uuid>.<ext>` 键，`--overwrite` 时删除不再被引用的旧图） | `--prefix`, `--suffix`, `--overwrite`, `--category`, `--unlisted` |
| `emoji purge` | 软删除自定义表情（保留图片，以便恢复） | `--remote-only` |

#### feeds

| 子命令 | 说明 | 常用 Flag |
|--------|------|-----------|
| `feeds build [USERNAME]` | 构建用户的 Home / List Feed | `--all`, `--concurrency`, `--dry-run` |
| `feeds clear` | 清空 Redis 中的所有 Feed | — |

#### media

| 子命令 | 说明 | 常用 Flag |
|--------|------|-----------|
| `media remove` | 删除**远程账号**附件的本地缓存副本（经存储后端删除原图与缩略图，清空 `file_path`/`thumbnail_path`，行保留并回落到 `remote_url`）；本地用户媒体永不触碰；被其他行共享的文件保留 | `--days`, `--concurrency`, `--prune-profiles`, `--remove-headers`, `--dry-run` |
| `media remove-orphans` | 扫描孤立对象。**默认只报告（dry run）**，必须加 `--confirm` 才删除（同时给 `--dry-run` 时 dry run 优先）。只扫描 `media/`、`matrix/`、`refreshed/`，`emojis/`、`avatars/` 等永不删除；比较规范化后的存储键；跳过宽限期内的对象；数据库查询失败即中止；数据库无任何媒体引用而存储有对象时拒绝执行；删除前逐个回查数据库 | `--confirm`, `--min-age`（默认 24h，最少 1h）, `--prefix`, `--start-after`, `--dry-run` |
| `media refresh` | 重新拉取远程账号的媒体附件，写入 `media/<yyyy/mm/dd>/...` 键 | `--account`, `--domain`, `--status`, `--days`, `--force` |
| `media usage` | 按命名空间（`media/`、`matrix/`、`emojis/`…）统计真实对象数与字节数（本地目录或 S3 桶，S3 下另列旧本地回退目录） | — |
| `media lookup` | 通过媒体 URL 查找关联帖子 | — |

#### accounts

| 子命令 | 说明 | 常用 Flag |
|--------|------|-----------|
| `accounts create [USERNAME]` | 创建本地账号（已审核通过） | `--username`, `--email`, `--confirmed`, `--approve`, `--admin`, `--role`, `--password-stdin`, `--password` |
| `accounts delete [USERNAME]` | 删除账号 | `--username`, `--email` |
| `accounts modify [USERNAME]` | 修改账号属性 | `--email`, `--confirm`, `--approve`, `--disable`, `--enable`, `--admin`, `--unadmin`, `--role`, `--remove-role`, `--reset-password`, `--password-stdin`, `--password` |

- 密码：`--password-stdin` 读取标准输入的第一行；`--password` 会留在 shell 历史和进程列表中，不推荐。都不指定时 `create` 生成随机密码，只输出一次（`New password: ...`）。密码至少 8 个字符，与网页注册规则相同。
- `modify --reset-password` 生成并输出新的随机密码；修改密码会同时吊销该用户现有的登录会话和令牌。
- 角色：`--role NAME` 按名称分配角色（不区分大小写，可重复），内置 `owner`、`admin`、`moderator`，也可使用后台创建的自定义角色。`--admin` 表示超级管理员：设置管理员标记并分配 `owner` 角色；`--unadmin` 撤销两者。`--remove-role` 移除全部角色。
- `--confirmed` / `--confirm` 将邮箱标记为已验证；`modify --approve` 审核通过待审核账号。

#### self-destruct

| 子命令 | 说明 | 常用 Flag |
|--------|------|-----------|
| `self-destruct` | 从联邦中擦除服务器（交互式，需二次确认） | `--force`, `--batch-size`, `--workers`, `--continue-mode` |

> ⚠️ **危险操作**：`self-destruct` 会向所有已知联邦实例广播 Delete 活动，操作**不可逆**，完成后服务器将进入不可用状态。

## 用法示例

### 构建与运行

```bash
# 构建主服务
go build -o community ./cmd/community

# 构建 tootctl 管理工具
go build -o tootctl ./cmd/tootctl

# 构建 Complement 测试镜像入口
go build -o complement ./cmd/complement
```

### 启动主服务

```bash
# 正常启动
./community

# 回滚数据库迁移后退出
./community --rollback
```

### tootctl 命令示例

Docker 镜像已内置 `tootctl`（`/usr/local/bin/tootctl`），可直接在运行中的容器里执行，配置沿用容器的环境变量：

```bash
# 创建管理员，密码从标准输入读取（-T 关闭 TTY，管道才能传入）
printf '%s\n' "$ADMIN_PASSWORD" | docker compose exec -T community \
  tootctl accounts create admin --email admin@example.com --confirmed --admin --password-stdin

# 不指定密码时生成随机密码并输出一次
docker compose exec community tootctl accounts create admin --email admin@example.com --confirmed --admin

# 重置密码（输出新的随机密码）
docker compose exec community tootctl accounts modify admin --reset-password
```

```bash
# 查看版本
./tootctl version

# 创建管理员账号（生成随机密码并输出）
./tootctl accounts create admin --email admin@example.com --confirmed --admin

# 创建版主账号，密码从标准输入读取
./tootctl accounts create mod --email mod@example.com --confirmed --role moderator --password-stdin < password.txt

# 清空缓存
./tootctl cache clear

# 重新统计账号计数器（5 个并发 worker）
./tootctl cache recount accounts --concurrency 5

# 清理 7 天前的媒体附件（dry-run 预览）
./tootctl media remove --days 7 --dry-run

# 导入自定义表情
./tootctl emoji import /path/to/emojis.tar.gz --category mypack

# 构建所有用户的 Feed
./tootctl feeds build --all --concurrency 10

# 封锁邮箱域名
./tootctl email-domain-blocks add spam.com --with-dns-records

# 查看媒体占用（本地或 S3 桶的真实字节数）
./tootctl media usage

# 孤立文件：先预览，确认后再删除
./tootctl media remove-orphans
./tootctl media remove-orphans --confirm

# 爬取联邦统计（JSON 格式输出）
./tootctl domains crawl --format json --concurrency 50
```

### Complement 测试

```bash
# 设置环境变量
export SERVER_NAME=hs1
export DB_CONNECTION=sqlite
export DB_HOST=/data/community.db
export TLS_CERT=/certs/tls.crt
export TLS_KEY=/certs/tls.key

# 启动（Client API :8008，Federation :8448）
./complement
```

## 配置说明

### 主服务配置

主服务通过 `config` 包读取配置，关键项包括：

| 配置项 | 变量 | 说明 |
|--------|------|------|
| 监听地址 | `config.C.Listen` | HTTP 服务器监听地址（如 `:8080`） |
| 开发模式 | `config.C.DevMode` | `true` 时启用 debug 日志级别 |
| CORS 来源 | `config.C.CORSAllowedOrigins` | 逗号分隔的允许跨域来源列表 |

支持 `--rollback` 命令行参数触发数据库迁移回滚。

### Complement 环境变量

| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| `SERVER_NAME` | Homeserver 标识（如 `hs1`） | `localhost` |
| `DB_CONNECTION` | 数据库类型 | `sqlite` |
| `DB_HOST` | 数据库路径 | `/data/community.db` |
| `TLS_CERT` | Federation TLS 证书路径 | —（为空则不启动 :8448） |
| `TLS_KEY` | Federation TLS 私钥路径 | — |

### tootctl 配置

tootctl 执行命令前会自动执行 `migrate.Up()` 数据库迁移（仅查看帮助 `--help` / `-h` / `help` 时跳过），并通过 `config.C.DevMode` 判断是否启用 debug 日志。支持全局 `--verbose` / `-v` flag 切换详细输出；`--version` 打印 CLI 版本（无短别名，`-v` 始终表示 verbose）。

> 注意：`db` 包在进程启动时即建立数据库连接，因此即使只查看帮助，也需要可用的数据库配置。

## 依赖关系

### 外部依赖

| 依赖 | 用途 |
|------|------|
| [`github.com/gin-gonic/gin`](https://github.com/gin-gonic/gin) | HTTP Web 框架 |
| [`github.com/urfave/cli/v2`](https://github.com/urfave/cli) | tootctl 命令行框架 |
| [`gorm.io/gorm`](https://gorm.io) | ORM 数据库操作 |

### 内部包依赖

`cmd/` 下的入口聚合了项目核心包，调用关系如下：

```
cmd/community  ──→  config, db, cache, log, server, community(路由)
                    middleware, metrics, health, errors
                    migrate, models, event, utils
                    matrix, matrix/homeserver
                    notifications (side-effect import)

cmd/complement ──→  db, models, matrix/homeserver

cmd/tootctl    ──→  config, db, log, migrate
cmd/tootctl/commands ──→  db, models, storage, activitypub, systemsettings, log
```

| 内部包 | 在 cmd 中的用途 |
|--------|-----------------|
| `config` | 读取服务配置（监听地址、开发模式、CORS 等） |
| `db` | 获取数据库连接和 Redis 客户端 |
| `cache` | 主服务启动时初始化缓存 |
| `log` | 统一日志输出 |
| `server` | 带优雅关闭的 HTTP 服务器封装 |
| `middleware` | 安全头、CORS、超时、认证等中间件 |
| `metrics` / `health` | Prometheus 指标与健康检查端点 |
| `migrate` | 数据库迁移（Up / Down） |
| `matrix` / `matrix/homeserver` | Matrix 桥接与完整 homeserver 实现 |
| `storage` | 本地 / 云存储的媒体文件操作（tootctl media / emoji） |
| `activitypub` | ActivityPub 交付队列（tootctl self-destruct） |
| `systemsettings` | 系统设置读写（self-destruct 模式、域名配置等） |

## 注意事项

- `community` 进程的 `MaxMultipartMemory` 限制为 **8 MB**，超出部分写入临时文件，防止内存 DoS。
- `tootctl media refresh` 内置 **SSRF 防护**：通过 `validateRemoteURL()` 和 `safeDialContext()` 阻断私有地址、回环地址和链路本地地址，并对重定向目标二次校验。
- `tootctl self-destruct` 采用交互式二次确认，设置 `--force` 可跳过（危险）。操作后服务器进入"自毁模式"，仅允许用户登录下载数据。
- `complement` 入口与主服务入口独立，互不依赖前端文件，专为 Complement 自动化测试设计。
