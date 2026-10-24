<p align="center">
  <img src="frontend/public/logo.svg" alt="Community" width="88">
</p>

<h1 align="center">Community</h1>

<p align="center"><b>为你的 Fediverse 小角落准备的一台小服务器：Mastodon 客户端、Misskey 式表情回应和 Matrix 聊天，全部内置。</b></p>

<p align="center">
  <a href="README.md">English</a> · <b>简体中文</b> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ru.md">Русский</a> · <a href="README.fr.md">Français</a> · <a href="README.de.md">Deutsch</a>
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/github/go-mod/go-version/kasuganosora/community">
  <img alt="ActivityPub" src="https://img.shields.io/badge/ActivityPub-federated-8a2be2">
  <img alt="Matrix" src="https://img.shields.io/badge/Matrix-homeserver-0dbd8b">
  <a href="https://maid.lat"><img alt="Demo" src="https://img.shields.io/badge/demo-maid.lat-1e90ff"></a>
</p>

<p align="center">
  <img src="docs/images/desktop-home.jpg" alt="Community on desktop" width="72%">
  <img src="docs/images/mobile-about.jpg" alt="Community on mobile" width="22%">
</p>

Community 是一个用 Go 和 Vue 写的自托管社交服务器。时间线、关注、回复、投票、媒体、私信都有，还能和 Mastodon、Misskey 以及整个 Fediverse 上的人互动。聊天室来自同一个程序里内置的 Matrix Homeserver，不需要再额外部署什么。

在线体验：**https://maid.lat**。

## 为什么选 Community？

Fediverse 上已经有很多优秀的服务器软件。Community 的不同之处在于：

- **继续用你熟悉的 Mastodon 客户端。** Community 实现了 Mastodon 客户端 API，可以直接用 Mastodon App 登录（已用 iOS 上的 Toot! 验证）。同时还有 Misskey 风格的功能：表情回应（Unicode 和自定义表情）、头像装饰，以及完整的网页端。表情回应可以和 Misskey 双向互通。
- **自带聊天。** 同一个服务器里内置了 Matrix Homeserver。用户可以在网页里加入群聊，也可以用 Element 等 Matrix 客户端（包括端到端加密房间），和其他 Matrix 服务器上的人聊天。不需要在旁边再跑一个 Synapse。
- **小巧，好维护。** 一个 Go 程序同时负责 API、网页、联邦和 Matrix。支持 PostgreSQL、MySQL 或 SQLite。Redis 必需；全文搜索用的 Meilisearch 可选。演示站上主进程大约只占 82 MiB 内存。
- **整站搬家，粉丝不丢。** 从 Misskey 或 Mastodon 切换过来？Community 会导入整个站点并保留密钥和地址，关注关系不会断。详见[换软件，不用从头再来](#换软件不用从头再来)。
- **在浏览器里完成安装。** 首次启动会打开安装向导（7 种语言），选择全新安装或迁移、填写域名和数据库即可。安装完成后向导自动锁定。
- **说你的语言。** 网页界面和错误提示支持 English、简体中文、繁體中文、日本語、Русский、Français、Deutsch。
- **常用的管理工具都在。** 中继（Relay）、自定义表情管理、域名屏蔽、内容过滤、S3 / Cloudflare R2 / MinIO 存储、远端图片的媒体代理，以及 Mastodon 风格的 `tootctl` 命令行。

## 换软件，不用从头再来

很多人一直没有离开现在的 Fediverse 软件，原因通常是这样的：每个账号都有一对加密密钥和一个固定地址（Actor URL），每条帖子也有固定的地址。远端服务器用这些密钥校验签名。一旦换了软件，密钥和地址都变了，签名校验失败，所有关注关系都会断掉。于是管理员只好放弃，让大家从零开始，或者一个个手动迁移账号。

Community 解决的正是这个问题。它会把**整个 Misskey 或 Mastodon 站点在同一域名下导入**，并保留：

- 每个账号**原来的 Actor 地址和 RSA 密钥对**（以及实例 / 系统 Actor 和它的密钥），
- **原来的帖子地址**（Misskey 的 `/notes/<id>`、Mastodon 的 `/users/<name>/statuses/<id>`）和旧的媒体链接，

所以远端服务器会继续信任你，**关注你的人还在关注你**。谁都不需要执行"迁移账号（Move）"。

**会搬过来的**

- 账号、个人资料、资料字段、头像和封面、账号隐私设置
- 密码（兼容的 bcrypt 哈希，用户用原密码就能登录）
- 帖子、回复、转发、内容警告、仅本站可见的帖子、附件（真实文件，带校验）以及支持的投票
- 私信及其会话，收件人保持私密
- 关注和待审批的关注请求、拉黑、静音（含期限）、域名屏蔽、列表、书签、喜欢
- 自定义表情和表情回应
- 其余内容保存在私有归档中，预检报告会明确告诉你哪些被归档了

**不会搬过来的**

- 登录会话和应用（OAuth）令牌：用户需要重新登录
- 两步验证和安全密钥：如果某个账号启用了两步验证，预检会停下来告诉你，而不是悄悄关掉它
- 历史通知；历史内容也不会重新发送给其他服务器
- 部分 Misskey 独有的功能（某些频道、引用和聊天功能）可能只能归档
- DNS：准备好之后由你自己切换域名

**可以放心演练。** 导出工具只读取旧数据库（使用短小的只读事务，中断后可以续传），从不修改它。你可以先在单独的 Community 数据库上演练，随时回退到旧服务器。重复执行同一份导入也不会产生重复数据。

**怎么做：** 在安装向导中选择"从 Misskey 迁移"或"从 Mastodon 迁移"，向导会自动完成导出和导入。命令行分两步：先用 `scripts/migration/export_misskey.py`（Mastodon 用 `export_mastodon_db.py`）导出，再用 `community-transfer --scope site --input <迁移包> --report …` 导入（先预检，再加 `--apply` 执行）。默认只导出本站自己的数据（本地用户及其帖子、文件、关注，以及它们引用到的远端账号和帖子），不导出整个远端内容缓存；加 `--include-remote-cache` 可导出全部。普通用户也可以把自己的 Misskey / Mastodon 个人备份导入到自己的账号。已用 Misskey 2026.10.0 和 Mastodon 4.7.3 测试。完整说明、检查清单和限制见 [docs/data-migration.md](docs/data-migration.md)。

**大型实例推荐直连模式：** `community-transfer --source misskey --source-dsn 'host=… dbname=… user=…' --source-base-url https://你的域名 --work-dir /private/migration` 直接读取源数据库并流式导入（250 MB 实例峰值 RSS 约 110 MiB，打包方式约 1.3 GiB），支持先 `--fetch-only` 再短暂停机 `--final-sync`。Mastodon 把 `--source` 换成 `mastodon`；密码放在 `PGPASSFILE`，不要写进 DSN。操作手册：[docs/migration.md](docs/migration.md)。

## 和其他软件比较

一个简单而诚实的概览。这里的每个项目都有各自的长处，选适合你的就好。

| | Community | Mastodon | Misskey | GoToSocial | Akkoma |
| --- | --- | --- | --- | --- | --- |
| 编程语言 | Go | Ruby + Node.js | TypeScript (Node.js) | Go | Elixir |
| 需要运行的进程 | 1 个程序 + Redis | web、streaming、Sidekiq + Redis | 程序 + Redis | 1 个程序 | 1 个程序 |
| 数据库 | PostgreSQL、MySQL、SQLite | PostgreSQL | PostgreSQL | PostgreSQL、SQLite | PostgreSQL |
| Mastodon 客户端 | ✅（见下方说明） | ✅ 原生 | ❌ | ✅ | ✅ |
| 表情回应 | ✅ | ❌ | ✅ | ❌ | ✅ |
| 内置网页端 | ✅ | ✅ | ✅ | 简单（建议用 App） | ✅ |
| 内置 Matrix 聊天 | ✅ | ❌ | ❌ | ❌ | ❌ |
| 网页安装向导 | ✅ | ❌（命令行） | ❌ | ❌ | ❌（命令行） |
| 同域名导入整个 Misskey/Mastodon 站点 | ✅ | ❌ | ❌ | ❌ | ❌ |

**如果你喜欢 GoToSocial 的轻量：** Community 和它处在同一个量级，另外提供完整的网页端、Misskey 式表情回应、Matrix 聊天和整站迁移。GoToSocial 不需要 Redis，Community 需要。

**切换前值得知道的：**

- 编辑帖子的方式是"删除并重发"：会生成新帖子，原来的喜欢和回复不会带过去。Mastodon 的编辑接口会返回 `405`，依赖编辑功能的 App 需要适配。
- 表情回应使用 Community 的 API 扩展；普通 Mastodon App 会把它显示为普通收藏，或者不显示。
- 这是一个年轻的项目。具体和哪些软件测试过什么，见 [docs/federation.md](docs/federation.md)。

## 快速开始

需要 Docker 和 Compose v2.24 或更新版本。

```bash
git clone https://github.com/kasuganosora/community.git
cd community
cp .env.setup.example .env
# 将 COMPOSE_PROFILES 设为 postgres 或 mysql，并替换 BUNDLED_DB_PASSWORD。
# 不要在这里添加 DB_*，否则会跳过安装向导。
docker compose up -d --build
docker compose logs community | grep -A3 "install token"
```

然后打开 `http://<你的服务器>:8080/setup`，粘贴 token，数据库选择「内置数据库」，按向导操作即可。公开部署时请在前面加一个 HTTPS 反向代理，并使用最终域名：它会成为每个账号身份的一部分，之后不应更改。

更多选项（手动编写 `.env`、外部数据库、S3）：[docs/setup.md](docs/setup.md) 和 [docs/configuration.md](docs/configuration.md)。

## 运行需求

| 档位 | 硬件 | 配置 | 效果 |
| --- | --- | --- | --- |
| **最低**（功能缩减） | 1 vCPU、1 GB 内存 | SQLite + Redis，不用 Meilisearch，少量用户 | 功能都能用，但没有全文搜索（退回数据库搜索），媒体处理和联邦回填较慢 |
| **推荐** | 2 vCPU、2–4 GB 内存 | PostgreSQL + Redis，Meilisearch 可选 | 全部功能 |
| **中型及以上** | 取决于用户数和联邦流量 | | 见 [docs/tuning.md](docs/tuning.md) |

实测结果（Docker 中设置硬性 CPU / 内存限制并禁用 swap，2026 年 10 月 17 日；10 个用户、8 个并发客户端持续 10 分钟浏览时间线、发帖、发起投票、上传媒体和点赞）：

- **最低档**（整套服务限制在 1 CPU / 1 GB）：Community 空闲 78 MiB，峰值 196 MiB，Redis 不到 10 MiB。没有发生内存不足被杀。约 97 请求/秒，中位数 71 ms，95 分位 198 ms。
- **推荐档**（PostgreSQL + Redis + Meilisearch，2 CPU）：Community 峰值 215–250 MiB，PostgreSQL 约 300 MiB，Redis 约 30 MiB。总内存 2 GB 时，Meilisearch（限制 704 MiB）因内存不足被杀，Community 照常运行，搜索退回数据库；4 GB 时全部正常，Meilisearch 峰值 733 MiB。**如果要用 Meilisearch，请按 4 GB 规划**（或者单独给它至少 1 GB）。

演示站（2026 年 10 月）上 Community 约 82 MiB、Redis 141 MiB、Meilisearch 932 MiB。Meilisearch 是最占内存的部分，所以它是可选的。原始数据和复测方法：[docs/benchmarks.md](docs/benchmarks.md)。视频缩略图需要 FFmpeg（Docker 镜像已包含）。

## 兼容性

- **Fediverse：** 已和 Misskey、Mastodon 测试（关注、回复、私信、删除、自定义表情、表情回应、媒体）。也支持 ActivityPub 中继。
- **Matrix：** 已和 Synapse、Element Web 测试，包括加密房间和附件。
- **Mastodon App：** 已在演示站用 **Toot!（iOS）** 验证（登录和时间线，2026 年 10 月）。Community 实现了 Mastodon 客户端 API，所以大多数 Mastodon App 应该也能使用（注意上面提到的编辑差异）。
- **Matrix App：** 已用 Element Web 验证。

测试版本和已知限制：[docs/federation.md](docs/federation.md)。

## 升级

`git pull && docker compose up -d --build`。数据库迁移会在启动时自动执行。**请先备份数据库和 `uploads/`。** 详见 [docs/setup.md](docs/setup.md#upgrading-an-existing-deployment-ops)。

## 文档

- [安装向导与配置文件](docs/setup.md)
- [安装与配置](docs/configuration.md)
- [数据迁移](docs/data-migration.md)
- [功能详解](docs/features.md)
- [联邦与兼容性](docs/federation.md)
- [管理后台](docs/admin-console.md) · [tootctl](docs/tootctl.md)
- [调优](docs/tuning.md) · [性能测试](docs/benchmarks.md)
- [架构与代码导航](docs/architecture.md) · [开发与测试](docs/development.md)
- 深入阅读：[Feed 运行说明](docs/feed-runtime.md)、[推荐](docs/recommendations.md)、[链接预览](docs/link-previews.md)、[网页群聊](docs/matrix-web-groups.md)、[Matrix 与 ActivityPub 私信](docs/matrix-activitypub-direct.md)

## 参与贡献

欢迎在 [GitHub Issues](https://github.com/kasuganosora/community/issues) 提交问题和 Pull Request。报告兼容性问题时，请附上客户端或服务器版本、复现步骤和脱敏日志。本地构建和运行测试见 [docs/development.md](docs/development.md)。

## 许可证

Community 以 [MIT 许可证](LICENSE)发布。`third_party/gomatrixserverlib` 中内置的 gomatrixserverlib 副本仍使用 Apache License 2.0（见其 [LICENSE](third_party/gomatrixserverlib/LICENSE) 与 [NOTICE](NOTICE)）。

## 致谢

Community 站在许多项目的肩膀上：[Mastodon](https://github.com/mastodon/mastodon)、[Misskey](https://github.com/misskey-dev/misskey)、[GoToSocial](https://github.com/superseriousbusiness/gotosocial)、[Akkoma](https://akkoma.dev/AkkomaGang/akkoma)、[Matrix](https://matrix.org) 规范以及 [Synapse](https://github.com/element-hq/synapse) / [Element](https://element.io)，还有整个 ActivityPub 社区。谢谢你们。
