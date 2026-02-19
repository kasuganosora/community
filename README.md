# Community

一个基于 [ActivityPub](https://www.w3.org/TR/activitypub/) 协议的去中心化社交网络平台，完全兼容 [Mastodon API](https://docs.joinmastodon.org/api/)，并支持 [Matrix](https://matrix.org/) 协议桥接。

## ✨ 特性

### 核心功能
- 🌐 **ActivityPub 联邦** - 与 Mastodon、Pleroma、Misskey 等联邦平台互通
- 🐘 **Mastodon API 兼容** - 支持所有主流 Mastodon 客户端
- 💬 **Matrix 桥接** - 支持 Matrix 协议，实现跨平台即时通讯
- 📱 **流式 API** - WebSocket 实时推送通知和更新

### 社交功能
- 📝 发布嘟文（支持多媒体、投票、定时发布）
- 🔄 转发、收藏、书签
- 👥 关注/粉丝系统
- 🏷️ 标签跟踪
- 📋 列表管理
- 🔔 通知系统
- 🛡️ 内容过滤器和屏蔽
- 📨 私信和对话

### 管理功能
- 👨‍💼 管理员面板
- 📊 统计分析
- 🔧 实例配置
- 😀 自定义表情
- 📡 联邦中继管理
- 📜 实例规则

## 📋 系统要求

- Go 1.21+
- MySQL 8.0+ / MariaDB 10.5+
- Redis 6.0+

## 🚀 快速开始

### 1. 克隆项目

```bash
git clone https://github.com/kasuganosora/community.git
cd community
```

### 2. 安装依赖

```bash
go mod download
```

### 3. 配置环境变量

首次运行会自动生成 `.env` 配置文件模板：

```bash
cp .env.example .env
# 或直接运行，程序会自动创建配置文件
go run cmd/community/main.go
```

### 4. 编辑配置文件

编辑 `.env` 文件，填写必要的配置：

```env
# 服务监听地址
LISTEN=:8080

# 应用基础 URL（联邦访问地址，必须修改）
APP_BASE_URL=http://localhost:8080

# 存储类型: local / s3
STORAGE_TYPE=local

# 数据库配置
DB_CONNECTION=mysql
DB_HOST=localhost
DB_PORT=3306
DB_DATABASE=community
DB_USERNAME=root
DB_PASSWORD=your_password

# Redis 配置
REDIS_0_HOST=localhost
REDIS_0_PORT=6379
REDIS_0_PASSWORD=
REDIS_0_DB=0

# 邮件服务配置
SMTP_SERVER=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=your_email@example.com
SMTP_PASSWORD=your_smtp_password
SMTP_FROM=noreply@example.com

# 开发模式（true/false）
DEV_MODE=false
```

### 5. 运行服务

```bash
# 开发模式
go run cmd/community/main.go

# 或编译后运行
go build -o community cmd/community/main.go
./community
```

服务将在 `http://localhost:8080` 启动。

## ⚙️ 配置说明

### 基础配置

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `LISTEN` | 服务监听地址 | `:8080` |
| `APP_BASE_URL` | 应用基础 URL（联邦地址） | `http://localhost:8080` |
| `DEV_MODE` | 开发模式（加载本地静态文件） | `false` |
| `SESSION_EXPIRE` | Session 过期时间（秒） | `1382400` (16天) |

### 数据库配置

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `DB_CONNECTION` | 数据库类型 | `mysql` |
| `DB_HOST` | 数据库主机 | - |
| `DB_PORT` | 数据库端口 | `3306` |
| `DB_DATABASE` | 数据库名称 | `community` |
| `DB_USERNAME` | 数据库用户名 | `root` |
| `DB_PASSWORD` | 数据库密码 | - |

### Redis 配置

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `REDIS_0_HOST` | Redis 主机 | - |
| `REDIS_0_PORT` | Redis 端口 | `6379` |
| `REDIS_0_PASSWORD` | Redis 密码 | - |
| `REDIS_0_DB` | Redis 数据库 | `0` |

支持多 Redis 配置，可添加 `REDIS_1_*`、`REDIS_2_*` 等。

### 存储配置

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `STORAGE_TYPE` | 存储类型 (`local`/`s3`) | `local` |
| `ENCRYPTED_KEY` | 加密密钥（32字节） | 自动生成 |

### 邮件配置

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `SMTP_SERVER` | SMTP 服务器 | - |
| `SMTP_PORT` | SMTP 端口 | - |
| `SMTP_USERNAME` | SMTP 用户名 | - |
| `SMTP_PASSWORD` | SMTP 密码 | - |
| `SMTP_FROM` | 发件人地址 | - |
| `SMTP_AUTH_METHOD` | 认证方式 | `PLAIN` |
| `SMTP_TIMEOUT` | 超时时间（秒） | `10` |
| `EMAIL_MODE` | 邮件模式 | `smtp` |

### 代理配置

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `HTTP_PROXY` | HTTP 代理地址 | - |

## 📚 API 文档

### Mastodon API

完全兼容 Mastodon API v1/v2，支持以下端点：

- `/api/v1/accounts/*` - 账户相关
- `/api/v1/statuses/*` - 嘟文相关
- `/api/v1/timelines/*` - 时间线
- `/api/v1/notifications/*` - 通知
- `/api/v1/media/*` - 媒体上传
- `/api/v2/search` - 搜索

详细 API 文档请参考 [Mastodon API 官方文档](https://docs.joinmastodon.org/api/)。

### ActivityPub 端点

- `/.well-known/webfinger` - WebFinger
- `/.well-known/host-meta` - Host Metadata
- `/.well-known/nodeinfo` - NodeInfo
- `/users/:username` - Actor Profile
- `/users/:username/inbox` - Inbox
- `/users/:username/outbox` - Outbox

### Matrix 端点

- `/_matrix/client/*` - Client-Server API
- `/_matrix/federation/*` - Federation API
- `/_matrix/app/*` - Application Service API

## 🧪 测试

```bash
# 运行所有测试
go test ./...

# 运行带覆盖率的测试
go test ./... -cover

# 运行特定包的测试
go test ./feed/ -v
```

## 📦 项目结构

```
community/
├── activitypub/        # ActivityPub 协议实现
├── cache/              # 缓存层
├── config/             # 配置管理
├── controllers/        # HTTP 控制器
│   └── api/            # API 端点
├── db/                 # 数据库连接
├── email/              # 邮件服务
├── event/              # 事件系统
├── feed/               # Feed 时间线
├── log/                # 日志系统
├── matrix/             # Matrix 桥接
├── media/              # 媒体处理
├── middleware/         # HTTP 中间件
├── migrate/            # 数据库迁移
├── models/             # 数据模型
├── notifications/      # 通知系统
├── serializer/         # 响应序列化
├── storage/            # 存储后端
├── tests/              # 测试工具和集成测试
└── utils/              # 工具函数
```

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

## 📄 许可证

MIT License
