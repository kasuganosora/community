# Community

Community 是一个使用 Go 和 Vue 构建的自托管社交平台，提供时间线、关注、回复、私信和媒体发布，并内置 ActivityPub 联邦与 Matrix Homeserver。

你可以通过网页使用 Community，也可以通过其 Mastodon 风格 API 或 Matrix 客户端接入。协议兼容性以实际实现和测试范围为准，不承诺兼容所有第三方客户端。

## 主要功能

- **社交与时间线**：关注、关注审批、公开与关注时间线、标签、列表、回复、转发、喜欢、书签和通知。
- **内容与可见性**：媒体附件、内容警告、投票，以及公开、不公开、关注者可见和私信四种可见范围。
- **ActivityPub 联邦**：远端账号发现与搜索、关注和审批、内容投递、回复、私信与删除；投递使用持久队列、签名和重试机制。
- **Matrix 通信**：客户端登录与同步、房间、邀请、跨服务器消息，以及端到端加密所需的设备与密钥交换。
- **实例管理**：账号与内容管理、实例设置、自定义表情、内容过滤和屏蔽。

### 编辑就是删除并重发

Community **不支持原地编辑帖子**。网页中的“编辑（删除并重发）”会先明确告知用户，再执行替换：

- 新帖获得新的 ID 和链接，旧帖被删除。
- 默认保留可见范围、回复对象、附件、内容警告和投票配置；投票截止时间保留，票数归零。
- 旧帖的点赞、转发、书签、投票记录和已有回复不会迁移到新帖。
- 替换在一个数据库事务中完成；事务失败会保留原帖，网页保留草稿。时间线通知在提交后发布。
- 联邦发送的是旧帖的 `Delete` 和新帖的 `Create`，不依赖远端对帖子 `Update` 的支持。

API 使用 `POST /api/v1/statuses/:id/replace`，返回新的 Status；v2 提供对应接口。原地编辑接口 `PUT /api/v1/statuses/:id` 及其 v2 对应接口返回 `405`。第三方客户端需要适配这一差异。

## 快速开始

### Docker Compose

仓库中的 Compose 配置包含 Community、MySQL 8.4 和 Redis 7，并在构建时编译前后端。

```bash
git clone https://github.com/kasuganosora/community.git
cd community
cp .env.example .env
openssl rand -base64 32
```

编辑 `.env`：

- 将上一步生成的随机值填入 `ENCRYPTED_KEY`。
- 将 `DB_PASSWORD` 改为自己的数据库密码；默认 Compose 使用 `DB_USERNAME=root`。
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

默认 Compose 还会映射 MySQL 的 `3306` 和 Redis 的 `6379` 端口。公开部署时应移除这些映射或限制到本机，并为应用配置 HTTPS 反向代理。`APP_BASE_URL` 是账号与联邦对象身份的一部分，建立实例后不要随意更换。

### 本地源码开发

已验证的开发组合为 Go **1.27.1**、Node.js **26.10.0**、npm **12.2.0**、MySQL **8.4.11** 和 Redis **7.4.11**；Go 版本声明以 [go.mod](go.mod) 为准。视频处理需要 FFmpeg。

前端使用 TypeScript **6.0.3**；当前 `typescript-eslint` 的兼容范围尚不包含 TypeScript 7。依赖版本与完整解析结果分别记录在 `go.mod` / `go.sum` 和 `frontend/package.json` / `frontend/package-lock.json` 中。

先准备可访问的 MySQL 数据库和 Redis，再复制 `.env.example`。除上述必填项外，本机运行还需要填写连接参数，例如：

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
| `STORAGE_TYPE`、`S3_*` | 本地媒体或 S3 兼容存储 |
| `SMTP_*` | 需要发送邮件时配置 SMTP |
| `MEILISEARCH_*` | 可选全文搜索服务 |
| `ENV_FILE` | 显式指定配置文件路径 |
| `APP_ROOT` | 配置和前端等运行时文件的根目录 |

环境变量优先于配置文件。未指定 `ENV_FILE` 时，配置按 `APP_ROOT`、工作目录、可执行文件目录等位置查找。不要把密码、私钥或真实会话令牌提交到仓库。

## 联邦与客户端验证范围

以下场景于 **2026-10-03** 在独立的真实服务器与浏览器实验环境验证，使用多账号检查内容送达和权限，而非仅检查 HTTP 请求成功。

| 对端 | 已验证场景 |
| --- | --- |
| Misskey 2026.10.0 | 双向关注、锁定账号审批、公开内容、回复、私信、删除；四种可见范围下删除并重发，确认旧帖消失、新帖送达和访问权限保持 |
| Mastodon 4.7.3 | 与 Community、Misskey 双向发现、关注、公开/关注者可见/私密提及、回复和删除；三站共同回复线程、提及通知、群私信收件隔离和删除重发 |
| Synapse 1.162.0 | 新建 v10 房间的双向加入、私人邀请、消息、退房和权限拒绝；查询事件发生时的历史状态 |
| Element Web 1.12.30 | 登录、两种服务器托管方向的普通房间收发；跨服务器双向加密收发，双方刷新后仍能解密，服务器历史仅包含密文 |

Community 的私信通过 ActivityPub 对应 Misskey 的“指定用户可见（Direct）”帖子及其回复，可从 Community 的私信会话和 Misskey 的帖子页面操作。Misskey 独立的 Chat 功能不是同一条联邦通信通道。

Mastodon 将这类私信称为“私密提及（Private mentions）”。其默认策略可能将陌生账号的私密提及放入通知请求，而不直接显示在首页或普通通知列表；排查收件时应同时检查通知请求。 Misskey 也可能按首页回复策略隐藏已收到的回复，应同时检查提及、通知和帖子详情。

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

如果测试环境设置了 `HTTP_PROXY`，本机测试服务器的请求可能被代理；此时可仅对测试命令清除该变量：

```bash
HTTP_PROXY= http_proxy= go test -p 3 ./...
```

本轮依赖升级后的完整后端回归通过 70 个测试包、6414 个测试及子测试，42 项按环境条件跳过。前端通过 25 个测试文件、107 项测试及 lint、生产构建。测试数量会随开发变化，当前结果以实际运行输出为准。

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
