# 安装与配置 / Installation & configuration

> Moved from the old README. The quickest path is the [setup wizard](setup.md).

## Docker Compose（手动配置）

仓库中的 Compose 配置包含 Community、Redis 7 和 Meilisearch，并在构建时编译前后端；数据库可以使用 Compose 自带的 MySQL 8.4 / PostgreSQL 17 容器，也可以连接外部服务器（需要 Docker Compose v2.24+）。

最简单的方式：直接 `docker compose up -d --build`，在日志中找到一次性 setup token，打开 `https://<域名>/setup` 按向导完成安装（支持全新安装和从 Misskey 迁移）。向导生成的配置写入宿主机的 `./var/config`（容器内 `/app/config/.env`），不会写进源码目录 `config/`。详见 [docs/setup.md](setup.md)。也可以手动编写配置：

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


## 配置

完整配置入口见 [.env.example](../.env.example) 和 [配置模块说明](../config/README.md)。

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
