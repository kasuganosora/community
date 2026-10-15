# config (配置管理)

集中管理应用配置的包，支持通过 `.env` 文件、环境变量加载配置，并统一暴露应用版本信息。配置在包初始化时（`init`）自动加载，全局通过变量 `C` 访问。

## 核心功能

- **多来源加载**：优先读取运行目录下的 `.env` 文件，不存在时回退到系统环境变量。
- **自动生成 `.env`**：首次启动且无任何配置来源时，自动基于结构体标签生成默认 `.env` 文件。
- **默认值机制**：通过结构体 `default` tag 提供默认值，未配置时自动填充。
- **反射映射**：通过 `env` tag 将环境变量自动映射到 `Config` 字段，未识别的变量收集到 `Other` map。
- **启动校验**：生产环境强制校验 `ENCRYPTED_KEY`（至少 16 位）和 `DB_PASSWORD`，测试模式自动跳过。
- **版本信息**：内置应用版本与兼容的 Mastodon API 版本常量。

## 配置加载机制

```
init() → loadConfig()
              ├─ .env 文件存在? → loadConfigFromEnvFile() (godotenv 解析)
              └─ 否              → loadConfigFromEnv()    (读取 os.Environ())
                                      └─ 若两者皆无 → createDefaultEnvFile() 生成默认 .env
```

`loadMapToConfig()` 通过反射遍历 `Config` 结构体字段，依据 `env` tag 名称匹配键值；未匹配 `env` tag 的配置项会被收集到 `Config.Other` map 中。

> `env:"-"` 标记的字段（如 `Other`）不参与环境变量映射。

## 配置项列表

### 应用基础配置

| 字段 | 环境变量 | 默认值 | 说明 |
|------|----------|--------|------|
| `Listen` | `LISTEN` | `:8080` | HTTP 监听地址 |
| `AppBaseURL` | `APP_BASE_URL` | `http://localhost:8080` | 应用基础 URL |
| `StorageType` | `STORAGE_TYPE` | `local` | 存储类型 |
| `EncryptedKey` | `ENCRYPTED_KEY` | _(无)_ | 加密密钥，必须通过环境变量配置，至少 16 位 |
| `DevMode` | `DEV_MODE` | `false` | 开发模式，`true` 时从本地加载内嵌静态文件 |
| `SessionExpire` | `SESSION_EXPIRE` | `1382400` | Session 过期时间（秒），默认 16 天 |
| `TrustedProxies` | `TRUSTED_PROXIES` | `127.0.0.1,::1,172.16.0.0/12,10.0.0.0/8,192.168.0.0/16` | 逗号分隔的可信反向代理 IP/CIDR。仅当直连对端在此列表内时才采信转发头；否则忽略 `X-Real-IP`/`X-Forwarded-For`，客户端无法伪造 `c.ClientIP()`（限流、日志、注册/登录 IP 均使用它） |
| `TrustedProxyHeaders` | `TRUSTED_PROXY_HEADERS` | `X-Real-IP,X-Forwarded-For` | 可信代理下按顺序采用的转发头。`X-Real-IP`（nginx 的 `$remote_addr`）优先；`X-Forwarded-For` 取最右侧不可信地址 |
| `CORSAllowedOrigins` | `CORS_ALLOWED_ORIGINS` | _(无)_ | 逗号分隔的允许来源列表，为空则允许所有（仅开发模式） |
| `MediaProxyEnabled` | `MEDIA_PROXY_ENABLED` | `true` | 远程头像/附件/表情/卡片图经 `/proxy/media` 流式转发（不落盘、不缓存） |
| `MediaProxyKey` | `MEDIA_PROXY_KEY` | _(服务端加密密钥)_ | 代理令牌（AES-256-GCM，HKDF 派生）的密钥。令牌内含过期时间（API 生成的链接有效 3000–3600 秒，按 600 秒分桶保持稳定），过期返回 410；任何站点均可使用有效令牌（CORP: cross-origin），以令牌本身作为授权；每个 IP 限速/限并发 |
| `MediaProxyPreviousKey` | `MEDIA_PROXY_PREVIOUS_KEY` | _(无)_ | 轮换密钥时仍可解密的旧密钥；新令牌始终用当前密钥 |
| `MediaProxyMaxBytes` | `MEDIA_PROXY_MAX_BYTES` | `20971520` | 转发图片的大小上限 |
| `MediaProxyMaxStreamBytes` | `MEDIA_PROXY_MAX_STREAM_BYTES` | `536870912` | 转发视频/音频的大小上限 |
| `AvatarCacheEnabled` | `AVATAR_CACHE_ENABLED` | `true` | 头像磁盘缓存（本站 `/media/..?size=N` 与远程代理 `/proxy/media/..` 头像）；关闭后按原样转发 |
| `AvatarCacheDir` | `AVATAR_CACHE_DIR` | `avatar-cache` | 缓存目录（相对路径按工作目录解析），需可写、建议持久化 |
| `AvatarCacheMaxBytes` | `AVATAR_CACHE_MAX_BYTES` | `5368709120` | 缓存总大小上限（5 GiB），超出时按最近访问时间 LRU 淘汰到 90% |
| `AvatarCacheRemoteTTL` | `AVATAR_CACHE_REMOTE_TTL` | `24h` | 远程头像过期后重新拉取（失败时继续使用旧图）；本站头像按媒体 ID/更新时间换键，无需 TTL |
| `WebSocketAllowedOrigins` | `WEBSOCKET_ALLOWED_ORIGINS` | _(无)_ | WebSocket 允许的来源 |
| `HttpProxy` | `HTTP_PROXY` | _(无)_ | HTTP 代理地址 |

### 数据库配置

| 字段 | 环境变量 | 默认值 | 说明 |
|------|----------|--------|------|
| `DbConnection` | `DB_CONNECTION` | `mysql` | 数据库连接类型：`mysql` / `postgres`（`postgresql`、`pgsql`）/ `sqlite` / `xml` |
| `DbHost` | `DB_HOST` | _(无)_ | 数据库主机 |
| `DbPort` | `DB_PORT` | `3306` | 数据库端口（PostgreSQL 未显式设置时使用 `5432`） |
| `DbDatabase` | `DB_DATABASE` | `community` | 数据库名称 |
| `DbUsername` | `DB_USERNAME` | `root` | 数据库用户名 |
| `DbPassword` | `DB_PASSWORD` | _(无)_ | 数据库密码（生产环境必填） |
| `DbSSLMode` | `DB_SSLMODE` | `disable` | 仅 PostgreSQL：`sslmode` |
| `DbDSN` | `DB_DSN` | _(无)_ | 仅 PostgreSQL：完整 DSN / URL，设置后忽略其他 `DB_*` 连接字段 |

### Redis 配置

| 字段 | 环境变量 | 默认值 | 说明 |
|------|----------|--------|------|
| `Redis0Host` | `REDIS_0_HOST` | _(无)_ | Redis 主机（多个 Redis 请依次递增编号） |
| `Redis0Port` | `REDIS_0_PORT` | `6379` | Redis 端口 |
| `Redis0Password` | `REDIS_0_PASSWORD` | _(无)_ | Redis 密码 |
| `Redis0Db` | `REDIS_0_DB` | `0` | Redis 数据库编号 |

### 邮件配置

| 字段 | 环境变量 | 默认值 | 说明 |
|------|----------|--------|------|
| `EmailMode` | `EMAIL_MODE` | `smtp` | 邮件发送模式 |
| `SmtpServer` | `SMTP_SERVER` | _(无)_ | SMTP 服务器地址 |
| `SmtpPort` | `SMTP_PORT` | _(无)_ | SMTP 端口 |
| `SmtpUsername` | `SMTP_USERNAME` | _(无)_ | SMTP 用户名 |
| `SmtpPassword` | `SMTP_PASSWORD` | _(无)_ | SMTP 密码 |
| `SmtpFrom` | `SMTP_FROM` | _(无)_ | 发件人地址 |
| `SmtpAuthMethod` | `SMTP_AUTH_METHOD` | `PLAIN` | SMTP 认证方式 |
| `SmtpTimeout` | `SMTP_TIMEOUT` | `10` | SMTP 超时时间（秒） |
| `SmtpTLS` | `SMTP_TLS` | `true` | 是否启用 TLS 加密传输 |

### 其他

| 字段 | 环境变量 | 默认值 | 说明 |
|------|----------|--------|------|
| `Other` | `-`（不参与映射） | _(无)_ | 存放未在 `Config` 结构体中定义的额外配置项 |

## 版本信息

| 常量 | 值 | 说明 |
|------|----|------|
| `Version` | `1.0.0` | 应用版本号 |
| `MastodonAPI` | `4.0.0` | 兼容的 Mastodon API 版本 |

## 用法示例

### 读取配置

配置在包导入时自动加载，直接使用全局变量 `config.C` 即可：

```go
import "github.com/kasuganosora/community/config"

// 读取数据库配置
dbHost := config.C.DbHost
dbPort := config.C.DbPort

// 读取监听地址
listen := config.C.Listen

// 读取其他（未定义）配置项
if val, ok := config.C.Other["MY_CUSTOM_CONFIG"]; ok {
    // ...
}
```

### 获取版本信息

```go
// 应用版本
version := config.GetVersion() // "1.0.0"

// 兼容的 Mastodon API 版本
mastodonAPI := config.GetMastodonAPIVersion() // "4.0.0"

// 也可以直接使用常量
fmt.Println(config.Version, config.MastodonAPI)
```

### `.env` 文件示例

```bash
# 应用配置
LISTEN=:8080
APP_BASE_URL=http://localhost:8080
DEV_MODE=false
ENCRYPTED_KEY=your-at-least-16-char-secret-key

# 数据库
DB_CONNECTION=mysql
DB_HOST=localhost
DB_PORT=3306
DB_DATABASE=community
DB_USERNAME=root
DB_PASSWORD=your-password

# Redis
REDIS_0_HOST=localhost
REDIS_0_PORT=6379

# 存储
STORAGE_TYPE=local
```

> 首次启动时若无 `.env` 文件且无环境变量，系统会自动生成包含默认值的 `.env` 文件。

## 核心类型与函数

### 类型

#### `Config`

全局配置结构体，所有字段均为 `string` 类型，通过 `default` 和 `env` tag 定义默认值与环境变量映射。全局实例为 `config.C`。

### 函数

| 函数 | 说明 |
|------|------|
| `GetVersion() string` | 返回应用版本号 |
| `GetMastodonAPIVersion() string` | 返回兼容的 Mastodon API 版本 |

> `loadConfig()`、`loadConfigFromEnv()`、`loadConfigFromEnvFile()`、`loadMapToConfig()` 等函数为内部实现，在 `init()` 阶段自动调用，通常无需手动调用。

## 依赖关系

| 依赖 | 用途 |
|------|------|
| [`github.com/joho/godotenv`](https://github.com/joho/godotenv) | 解析 `.env` 文件 |
| `github.com/kasuganosora/community/log` | 日志记录 |
| `github.com/kasuganosora/community/utils` | 文件路径与存在性检查 |
