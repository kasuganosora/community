# Community

Community 是一个使用 Go 和 Vue 构建的自托管社交平台，提供时间线、关注、回复、私信和媒体发布，并内置 ActivityPub 联邦与 Matrix Homeserver。

你可以通过网页使用 Community，也可以通过其 Mastodon 风格 API 或 Matrix 客户端接入。协议兼容性以实际实现和测试范围为准，不承诺兼容所有第三方客户端。

## 主要功能

- **社交与时间线**：关注、关注审批、公开与关注时间线、标签、列表、回复、转发、喜欢、表情回应、书签和通知。
- **内容与可见性**：媒体附件、内容警告、投票，以及公开、不公开、关注者可见和私信四种可见范围。
- **ActivityPub 联邦**：远端账号发现与搜索、关注和审批、内容投递、回复、私信与删除；投递使用持久队列、签名和重试机制。
- **Matrix 通信**：客户端登录与同步、房间、邀请、跨服务器消息，以及端到端加密所需的设备与密钥交换。
- **网页群聊**：创建群组、邀请成员、加入和搜索其他 Matrix 群；支持消息、回复、图片、文件和表情回应。网页创建非加密群，加密群使用 Matrix 客户端参与。
- **实例管理**：账号与内容管理、实例设置、自定义表情、内容过滤和屏蔽。
- **数据迁移**：Misskey / Mastodon 同域名数据库迁移、原签名密钥和兼容密码保留，以及 Community / Misskey / Mastodon 个人备份导入；提供预检、任务进度、重复执行去重和私有归档。

时间线支持事务事件、有界分批 Worker 和可重建的 Redis 候选缓存，API 与 Worker 可以使用同一个程序分别扩容。新分发默认运行于影子模式，验收后显式设置 `FEED_MODE=durable` 启用。网页支持实时变更提示和可变高度虚拟列表；配置、运行模式与回退步骤见 [Feed 运行说明](docs/feed-runtime.md)。

网页会为帖子中的链接补充标题、摘要、站点和可用缩略图，已有帖子也可在查看时补齐。预览按可见区域有界加载，图片走本站代理；抓取失败不影响原链接。范围与配置见 [链接预览](docs/link-previews.md)。

没有自定义头像的账号按完整账号名（含域名）生成稳定的几何头像，网页、ActivityPub 和 Matrix 使用相同规则。默认图片在内存中生成，无需文件存储；已有自定义头像保持优先。

网页消息页与 Matrix 共用联邦会话，可主动联系 Fediverse 账号，并在明确同意非加密的混合群中邀请外部 Matrix 用户。新消息、回复、附件与作者撤回通过持久任务投递给明确参与者；失败状态可见并可安全重试，不会自动外发历史 Matrix 消息。房间按明确收件人隔离，ActivityPub 私信不是端到端加密。收件范围、重试及限制见 [Matrix 与 ActivityPub 私信](docs/matrix-activitypub-direct.md)。

「消息 → 群聊」直接使用原生 Matrix 房间，与上述 ActivityPub 私信分开；可以与 Community 和外部 Matrix 成员交互，不邀请本站 ActivityPub 桥接账号。搜索中的「群组」支持本人群组、本站公开目录和指定服务器目录。网页编辑群消息会明确提示删除并重发，普通退群后不会因新消息自动加入。接口、部署限制与升级方式见 [网页群聊说明](docs/matrix-web-groups.md)。

### 整站迁移与个人备份

管理员可从「设置 → 整站迁移」导入源数据库适配器生成的私有迁移包；普通用户可从「数据导入与导出」下载 Community 备份、上传受支持的个人备份并查看仅自己可读的归档。同域名迁移保留原账号和联邦对象身份，无需每位用户自行迁移。

支持范围和限制以预检报告为准：无法等价支持的源字段会明确提示或阻止执行，不把仅保存原始记录视为功能已恢复。准备方法、支持版本、命令行和切换步骤见 [数据迁移说明](docs/data-migration.md)。

### 编辑就是删除并重发

Community **不支持原地编辑帖子**。网页中的“编辑（删除并重发）”会先明确告知用户，再执行替换：

- 新帖获得新的 ID 和链接，旧帖被删除。
- 默认保留可见范围、回复对象、附件、内容警告和投票配置；投票截止时间保留，票数归零。
- 旧帖的点赞、表情回应、转发、书签、投票记录和已有回复不会迁移到新帖。
- 替换在一个数据库事务中完成；事务失败会保留原帖，网页保留草稿。时间线通知在提交后发布。
- 联邦发送的是旧帖的 `Delete` 和新帖的 `Create`，不依赖远端对帖子 `Update` 的支持。

API 使用 `POST /api/v1/statuses/:id/replace`，返回新的 Status；v2 提供对应接口。原地编辑接口 `PUT /api/v1/statuses/:id` 及其 v2 对应接口返回 `405`。第三方客户端需要适配这一差异。

### 自定义表情与帖子回应

管理员可从「设置 → 自定义表情管理」上传、修改、禁用或删除表情，维护分类、搜索别名、授权说明、敏感标记、仅限本站使用及回应角色限制；支持批量操作和远端表情导入。普通账号和仅持有管理 scope 的非管理员不能操作后台。

支持 PNG/APNG、JPEG、GIF 和静态或动态 WebP：单文件最多 2 MiB，画布不超过 1024×1024，动图最多 128 帧。保留动画原文件并生成静态预览，拒绝 SVG 和伪造图片。表情文件存放于 `uploads/emojis/`，部署时应持久保存这个目录。

发帖时可按分类或别名选择表情，在光标处插入 `:shortcode:`；帖子、内容警告、昵称和个人简介会渲染关联的表情。敏感表情需要主动加载。改名不会自动改写既有帖子的短代码。

每人每帖可添加一种 Unicode 或自定义表情回应，再次点击可撤回，选择另一种会替换。回应与「喜欢」独立计数，并向作者发送独立通知；删除并重发不会迁移旧帖回应。回应者列表每组最多预览 20 人。

Misskey 可双向交换表情回应及撤回，也可交换帖子和个人资料中的自定义表情。远端表情按实例区分，须由管理员导入本站后才能由本地用户发送。分类、别名等本站管理信息不保证随联邦传输。

回应 API 是 Community 扩展：`GET /api/v1/statuses/:id/reactions` 查询，`PUT` / `DELETE /api/v1/statuses/:id/reactions/:name` 添加或撤回，`:name` 需 URL 编码；Status 响应包含 `emoji_reactions`。它不属于标准 Mastodon API，第三方客户端需要单独适配。

## 快速开始

### Docker Compose

仓库中的 Compose 配置包含 Community、Redis 7 和 Meilisearch，并在构建时编译前后端；数据库可以使用 Compose 自带的 MySQL 8.4 / PostgreSQL 17 容器，也可以连接外部服务器（需要 Docker Compose v2.24+）。

最简单的方式：直接 `docker compose up -d --build`，在日志中找到一次性 setup token，打开 `https://<域名>/setup` 按向导完成安装（支持全新安装和从 Misskey 迁移）。详见 [docs/setup.md](docs/setup.md)。也可以手动编写配置：

```bash
git clone https://github.com/kasuganosora/community.git
cd community
cp .env.example .env
openssl rand -base64 32
```

编辑 `.env`：

- 将上一步生成的随机值填入 `ENCRYPTED_KEY`。
- 将 `DB_PASSWORD` 改为自己的数据库密码；默认 Compose 使用 `DB_USERNAME=root`。
- 选择数据库（见下表）；`.env.example` 默认启用自带的 MySQL 容器。
- 本地体验使用 `APP_BASE_URL=http://localhost:8080`；正式联邦实例应填写固定、可从外部访问的 HTTPS 地址。

```bash
docker compose up -d --build
docker compose logs -f community
```

启动后访问 `http://localhost:8080`。可以通过以下接口检查服务：

```bash
curl -fsS http://localhost:8080/ready
curl -fsS http://localhost:8080/health/detailed
curl -fsS http://localhost:8080/api/v2/instance
```

上传文件保存在 `./uploads`，数据库和 Redis 使用 Docker 数据卷。升级前备份数据库与上传目录，启动时让数据库迁移正常完成。

数据库在 `.env` 中选择，四种方式任选其一（`.env.example` 中有对应的注释示例）：

| 方式 | `.env` 设置 |
|------|-------------|
| 自带 PostgreSQL 容器 | `COMPOSE_PROFILES=postgres`、`DB_CONNECTION=postgres` |
| 自带 MySQL 容器 | `COMPOSE_PROFILES=mysql`、`DB_CONNECTION=mysql` |
| 外部 PostgreSQL | `DB_CONNECTION=postgres`、`DB_HOST`、`DB_PORT`（默认 5432），可选 `DB_SSLMODE` / `DB_DSN` |
| 外部 MySQL | `DB_CONNECTION=mysql`、`DB_HOST`、`DB_PORT`（默认 3306） |

使用自带容器时不要设置 `DB_HOST` / `DB_PORT`：应用默认连接网络别名 `db`（即启用的数据库容器），容器按 `DB_DATABASE`、`DB_USERNAME`、`DB_PASSWORD` 初始化。`postgres` 和 `mysql` profile 只能启用一个；连接外部服务器时两个都不要启用。两种方式都必须设置 `DB_PASSWORD`。

从旧版 Compose 升级（MySQL 原先总是启动）：在 `.env` 中加入 `COMPOSE_PROFILES=mysql`，并删除只用于本机运行的 `DB_HOST` / `DB_PORT`。旧版 Compose 会强制覆盖这两项，现在则直接使用 `.env` 中的值。数据卷名称不变，已有数据会保留。

默认 Compose 还会映射自带数据库的端口（MySQL `3306`、PostgreSQL `5432`）和 Redis 的 `6379` 端口。公开部署时应移除这些映射或限制到本机，并为应用配置 HTTPS 反向代理。`APP_BASE_URL` 是账号与联邦对象身份的一部分，建立实例后不要随意更换。

### 本地源码开发

已验证的开发组合为 Go **1.27.1**、Node.js **26.10.0**、npm **12.2.0**、MySQL **8.4.11** 和 Redis **7.4.11**；Go 版本声明以 [go.mod](go.mod) 为准。视频处理需要 FFmpeg。

前端使用 TypeScript **6.0.3**；当前 `typescript-eslint` 的兼容范围尚不包含 TypeScript 7。依赖版本与完整解析结果分别记录在 `go.mod` / `go.sum` 和 `frontend/package.json` / `frontend/package-lock.json` 中。

先准备可访问的 MySQL（或 PostgreSQL，`DB_CONNECTION=postgres`，见 [db/README.md](db/README.md)）数据库和 Redis，再复制 `.env.example`。除上述必填项外，本机运行还需要填写连接参数，例如：

```dotenv
LISTEN=127.0.0.1:8080
APP_BASE_URL=http://localhost:8080
DEV_MODE=true
DB_CONNECTION=mysql
DB_HOST=127.0.0.1
DB_PORT=3306
DB_DATABASE=community
DB_USERNAME=root
REDIS_0_HOST=127.0.0.1
REDIS_0_PORT=6379
REDIS_0_DB=0
STORAGE_TYPE=local
```

保留自己设置的 `DB_PASSWORD` 和 `ENCRYPTED_KEY`，然后在仓库根目录构建并启动：

```bash
go mod download
npm --prefix frontend ci
npm --prefix frontend run build
mkdir -p uploads
go build -o community ./cmd/community
./community
```

后端会提供 `frontend/dist` 中的网页。开发前端时，可另开终端运行：

```bash
cd frontend
npm run dev
```

Vite 默认使用 `http://localhost:3000`，将 API 请求代理到 `http://localhost:8080`；可通过 `VITE_DEV_PROXY_TARGET` 修改代理目标。

## 配置

完整配置入口见 [.env.example](.env.example) 和 [配置模块说明](config/README.md)。

| 配置 | 用途 |
| --- | --- |
| `APP_BASE_URL` | 含协议的实例地址，用于本地账号、对象 URL 和联邦身份 |
| `ENCRYPTED_KEY` | 必填加密密钥，至少 16 个字符；应使用随机生成的值 |
| `DB_*`、`REDIS_0_*` | 数据库与 Redis 连接 |
| `STORAGE_TYPE`、`S3_*` | 本地媒体或 S3 兼容存储（见下文“对象存储”） |
| `SMTP_*` | 需要发送邮件时配置 SMTP |
| `MEILISEARCH_*` | 可选全文搜索服务 |
| `ENV_FILE` | 显式指定配置文件路径 |
| `APP_ROOT` | 配置和前端等运行时文件的根目录 |

环境变量优先于配置文件。未指定 `ENV_FILE` 时，配置按 `APP_ROOT`、工作目录、可执行文件目录等位置查找。不要把密码、私钥或真实会话令牌提交到仓库。

### 对象存储（S3 / Cloudflare R2 / MinIO）

`STORAGE_TYPE=local`（默认）时文件写入 `./uploads`。`STORAGE_TYPE=s3` 时，网页端与 Mastodon API 的媒体上传（`/api/v1/media`、`/api/v2/media`）、缩略图、旧版头像/头图上传、自定义表情和 Matrix 媒体都只写入存储桶，本地不保留副本。数据库只保存对象键（如 `media/2026/10/09/<文件名>`），不保存存储桶 URL 或本机路径。

Cloudflare R2 示例（存储桶需事先创建；令牌需对该桶有“对象读和写”权限）：

```dotenv
STORAGE_TYPE=s3
S3_ENDPOINT=https://<ACCOUNT_ID>.r2.cloudflarestorage.com
S3_REGION=auto
S3_BUCKET=community-file
S3_ACCESS_KEY=<R2_ACCESS_KEY_ID>
S3_SECRET_KEY=<R2_SECRET_ACCESS_KEY>
S3_USE_SSL=true
S3_FORCE_PATH_STYLE=true
# 可选：R2 公开访问地址（r2.dev 或自定义域名），对应存储桶根目录
# S3_PUBLIC_URL=https://media.example.com
```

- 媒体地址始终是本站的 `/media/<id>/<文件名>`（联邦与客户端看到的地址不随存储后端变化）。未配置 `S3_PUBLIC_URL` 时，由本站从存储桶读取后返回（支持 Range 请求）；配置后，图片、音频、视频直接 302 跳转到 `<S3_PUBLIC_URL>/<对象键>`，其他类型仍由本站以附件形式返回。
- `S3_PUBLIC_URL` 指向**存储桶根目录**，不要在末尾附加桶名。
- 配置错误不会悄悄回退到本地存储：上传会失败，`/health/detailed` 的 `storage` 项会显示 `unhealthy` 及原因（HeadBucket 探测，超时 4 秒，结果缓存 30 秒，不包含密钥和端点主机名）。
- 从本地切换到 S3 后，桶内缺失的旧文件仍会从 `./uploads` 读取（`S3_LEGACY_LOCAL_FALLBACK=false` 可关闭）。如需完全迁移，可把 `./uploads` 下的目录结构原样上传到桶根目录（例如 `rclone copy ./uploads r2:community-file`）。
- 旧版本在 S3 模式下把完整的存储桶 URL 写进 `media_attachments.file_path`；迁移脚本 `NormalizeStorageKeysScript` 会把它们改回对象键，读取时也会自动兼容。

## 联邦与客户端验证范围

以下场景于 **2026-10-03** 在独立的真实服务器与浏览器实验环境验证，使用多账号检查内容送达和权限，而非仅检查 HTTP 请求成功。

| 对端 | 已验证场景 |
| --- | --- |
| Misskey 2026.10.0 | 双向关注、锁定账号审批、公开内容、回复、私信、删除；四种可见范围下删除并重发，确认旧帖消失、新帖送达和访问权限保持；自定义表情、Unicode/自定义表情回应及撤回 |
| Mastodon 4.7.3 | 与 Community、Misskey 双向发现、关注、公开/关注者可见/私密提及、回复和删除；三站共同回复线程、提及通知、群私信收件隔离和删除重发；与 Community 双向自定义表情、资料更新和清空 |
| Synapse 1.162.0 | 新建 v10 房间的双向加入、私人邀请、消息、退房和权限拒绝；查询事件发生时的历史状态 |
| Element Web 1.12.30 | 登录、两种服务器托管方向的普通房间收发；跨服务器双向加密收发，双方刷新后仍能解密，服务器历史仅包含密文；两种房间托管方向的普通/加密附件双向收发与解密后内容校验 |

Community 的私信通过 ActivityPub 对应 Misskey 的“指定用户可见（Direct）”帖子及其回复，可从 Community 的私信会话和 Misskey 的帖子页面操作。Misskey 独立的 Chat 功能不是同一条联邦通信通道。

Mastodon 将这类私信称为“私密提及（Private mentions）”。其默认策略可能将陌生账号的私密提及放入通知请求，而不直接显示在首页或普通通知列表；排查收件时应同时检查通知请求。 Misskey 也可能按首页回复策略隐藏已收到的回复，应同时检查提及、通知和帖子详情。

文件传输已检查 Community、Misskey、Mastodon 六个方向的图片、动图和视频，以及公开帖、私信、多附件和实际下载。Misskey 可发送普通文件；Community、Mastodon 的发帖上传接口只支持各自声明的媒体类型。远端附件可能经过转码，验收会区分原文件字节保持与转码后的内容可用性。

Mastodon 4.7.3 会将带表情的 Like 当作普通收藏，添加、替换、撤回已验证；它不保留所选表情，也不执行 Community 的单个表情敏感标记。两站自定义表情图片互通与表情回应语义支持是不同能力。

联邦故障验收覆盖停机、待发送任务存在时重启、自然退避重试、重复投递和已撤回活动重放。修复后，实际 Misskey 恢复场景的添加、替换和撤回结果两端一致。投递成功响应不保证远端异步处理顺序；缺少因果信息的全新活动仍存在协议限制。

这些结果不代表完整协议认证。仍需区分以下范围：

- 旧版 UUID 事件房间保留本地使用能力；缺少规范签名材料的旧历史不会被伪装成可联邦事件。本次真实 Matrix 联邦验证使用新建房间。
- Matrix 对部分晚到历史分支采用保守授权检查，尚未覆盖所有合法并发分支的在线状态合并。
- 跨设备密钥恢复、音视频通话和其他第三方客户端不在上述验收范围内。
- ActivityPub 私信的可见性控制不等于 Matrix 端到端加密，不应混淆两者。

联邦部署需要正确的 HTTPS、发现接口和可访问的实例身份。不要通过关闭证书校验、签名验证或 SSRF 防护来解决部署问题。

## 测试

在仓库根目录运行：

```bash
go test -p 3 ./...
npm --prefix frontend test
npm --prefix frontend run lint
npm --prefix frontend run build
```

数据库相关测试默认使用内存引擎（sqlexec / SQLite）；设置 `TEST_DATABASE_URL=postgres://…` 可改在 PostgreSQL 上运行，详见 [tests/README.md](tests/README.md)。

如果测试环境设置了 `HTTP_PROXY`，本机测试服务器的请求可能被代理；此时可仅对测试命令清除该变量：

```bash
HTTP_PROXY= http_proxy= go test -p 3 ./...
```

本次 Feed 与管理表单改动的完整后端回归通过 87 个测试包，前端通过 73 个测试文件、342 项测试及 lint、生产构建。真实浏览器验证覆盖分页、实时重连、账号切换时的私信隔离，以及后台九类设置页的编辑、保存和移动布局。详细场景、测量条件及尚未达到的性能门槛见 [本次验收记录](docs/tasks/feed-validation-20261011.md)。测试数量会随开发变化，以实际运行输出为准。

时间线压测使用独立数据库中的 2,000 个账号、60,000 条帖子，覆盖六种时间线、20 / 40 条分页、并发 1 / 8 / 32、冷缓存及深分页。优化包括批量缓存重建、关联与互动状态批量加载、标签及媒体查询计划、游标与列表缓存隔离。测量条件、结果和限制见[时间线性能报告](docs/timeline-performance.md)；可使用[压测脚本](scripts/benchmark-timelines.md)对自己的数据进行复测。

Feed 可靠分发、多实例扩展与网页虚拟列表的实现细节、阶段和验收标准见 [Feed 扩展实施计划](docs/tasks/feed_scaling_plan.md)，部署参数见 [运行说明](docs/feed-runtime.md)。计划中的性能门槛与验收记录中的测量结果分开列出。

首页推荐现有独立的候选、排序和稳定分页，可选择公开发现或个性化，并支持不感兴趣、隐藏作者/话题与撤销。热门话题提供滚动 24 小时 / 7 天、本地 / 本站已知联邦范围及不同作者计数。首次部署推荐默认关闭；可在后台开启，Meilisearch 增强需通过对应版本的质量审核。配置、隐私边界和资源限制见 [推荐运行说明](docs/recommendations.md)。

Matrix 使用独立的 30 个账号、100 个房间和 50,000 条时间线事件测试同步、历史分页及消息发送，并以真实 Element / Synapse 验证双向普通和加密消息、文件下载。优化包括批量状态及撤回查询、未读计数、请求取消、设备消息确认和媒体连接复用，同时修复历史权限、私有回执和异步上传问题。测量结果和限制见 [Matrix 性能报告](docs/matrix-performance.md)，复测方法见 [Matrix 压测脚本](scripts/benchmark-matrix.md)。

## 代码与文档导航

| 目录 | 内容 |
| --- | --- |
| [frontend](frontend/README.md) | Vue、TypeScript、Pinia 与网页界面 |
| [controllers](controllers/README.md) / [serializer](serializer/README.md) | HTTP API 与响应序列化 |
| [activitypub](activitypub/README.md) | ActivityPub 发现、收件箱、对象与联邦投递 |
| [matrix](matrix/README.md) | Matrix Homeserver 与桥接相关实现 |
| [models](models/README.md) / [migrate](migrate/README.md) | 数据模型与数据库迁移 |
| [feed](feed/README.md) / [notifications](notifications/README.md) | 时间线与通知 |
| [storage](storage/README.md) / [media](media/README.md) | 存储与媒体处理 |
| [tests](tests/README.md) | 测试工具和开发说明 |

欢迎通过 [Issues](https://github.com/kasuganosora/community/issues) 提交问题。报告兼容性问题时，请附上客户端或服务器版本、复现步骤与脱敏日志。
