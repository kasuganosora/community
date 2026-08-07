# systemsettings (系统设置)

基于数据库键值表（`system_settings`）的系统设置管理模块，提供带内存缓存的类型化读取（typed getter）、持久化保存、以及批量导入/导出能力。是站点配置、实例信息、注册策略、验证码配置等运行时可调参数的统一来源。

> 验证码（captcha）相关的读取与持久化也通过本包完成，详见 [captcha/README.md](../captcha/README.md)。

## 核心功能

| 功能 | 说明 | 主要入口 |
|------|------|----------|
| 加载缓存 | 从数据库 `system_settings` 表一次性加载全部键值到内存 | [`Load`](#load) |
| 类型化读取 | `Get` / `GetInt` / `GetBool` / `GetString` / `GetStrings` / `GetInts` 等带类型转换的 getter | [类型化 getter](#类型化-getter) |
| 内存写入 | 更新内存缓存（不自动落库） | [`Set`](#set) |
| 持久化 | 将指定 keys 的内存值写入数据库（upsert） | [`SaveToDB`](#savetodb) |
| 导入/导出 | 扁平 / 树形两种结构导出全部设置 | [`GetAllFlatStyle`](#getallflatstyle)、[`GetAllTreeStyle`](#getalltreestyle) |
| 便捷访问 | 站点域名、默认分页大小等常用值 | [`GetDomain`](#getdomain)、[`GetDefaultPageSize`](#getdefaultpagesize) |

## 用法示例

### 读取配置

所有 `GetXxx` 函数均为**包级函数**，内部自动调用 `Load()`（双重检查锁定，已加载时仅持读锁，快速路径无开销）。读取优先级：**`.env` 配置 > 数据库缓存 > 默认值**。

```go
import "github.com/kasuganosora/community/systemsettings"

// 字符串
domain := systemsettings.GetString(systemsettings.KeyDomain, "")

// 布尔（注册是否需要邀请码）
inviteRequired := systemsettings.GetBool(systemsettings.KeyRegistrationInviteRequired, false)

// 整数（推文最大字符数）
maxChars := systemsettings.GetInt(systemsettings.KeyStatusMaxCharacters, 500)

// 字符串切片（支持的语言）
langs := systemsettings.GetStrings(systemsettings.KeyLanguages, []string{"zh-CN", "en-US"})
```

> 所有 getter 的第二参数为默认值（`defaultValue any`）：当键不存在（既不在 `.env` 也不在数据库）时返回。

### 修改并持久化配置

`Set` 只更新**内存缓存**，需显式调用 `SaveToDB` 才会写入数据库。典型流程是「先批量 `Set`，再一次性 `SaveToDB`」：

```go
// 更新内存
systemsettings.Set(systemsettings.KeyCaptchaProvider, "cloudflare")
systemsettings.Set(systemsettings.KeyCaptchaSiteKey, "your-site-key")
systemsettings.Set(systemsettings.KeyCaptchaEnabled, true)

// 持久化到数据库（upsert：已存在则 UPDATE，不存在则 INSERT）
keys := []string{
    systemsettings.KeyCaptchaProvider,
    systemsettings.KeyCaptchaSiteKey,
    systemsettings.KeyCaptchaEnabled,
}
if err := systemsettings.SaveToDB(keys...); err != nil {
    // 处理错误
}
```

> 持久化后，依赖该配置的服务通常需要自行 reload。例如验证码服务在 `SaveToDB` 成功后会调用 `captcha.GetService().Reload()` 重新加载配置（见 `controllers/api/common/captcha.go`）。

### 缓存失效 / 重新加载

`Load()` 使用 `sync.Once` 风格的双重检查锁定，**首次加载后不会重复加载**。若需要强制重新读取数据库（例如其它实例/进程修改了数据库中的设置），需要重启进程或重新触发加载——当前实现中缓存为进程级内存态。

对于「修改 → 立即生效」的场景，推荐使用本进程内的 `Set` 直接更新内存缓存（上面的示例），这样无需重新 `Load` 即可生效。

### 导入 / 导出

#### 扁平结构导出

返回内部缓存的一份**深拷贝**（调用方修改不会影响内部状态），键保持原始的点分形式：

```go
flat := systemsettings.GetAllFlatStyle()
// map[string]any{
//     "domain":                       "example.com",
//     "config.statuses.max_characters": 500,
//     ...
// }
```

#### 树形结构导出

将点分键（如 `a.b.c`）展开为嵌套 map：

```go
tree := systemsettings.GetAllTreeStyle()
// map[string]any{
//     "domain": "example.com",
//     "config": map[string]any{
//         "statuses": map[string]any{
//             "max_characters": 500,
//         },
//     },
// }
```

> 树形导出按键升序排序后逐层构建，对键冲突（如同时存在 `a=1` 与 `a.b=2`）安全处理：用新的 map 覆盖标量占用的中间节点，不会 panic。

## 配置说明

### 优先级

读取顺序（先命中者返回）：

1. `.env` 环境变量（`config.C.Other[key]`）——最高优先级
2. 数据库 `system_settings` 表（加载到内存缓存）
3. 调用方传入的 `defaultValue`

> 这意味着 `.env` 中的同名配置会**覆盖**数据库中的设置，适用于强制覆盖运维场景。

### 配置键（Key）

所有键均定义为包级常量，避免拼写错误。下表按类别列出：

#### 站点信息

| 常量 | 值 | 说明 |
|------|------|------|
| `KeyDomain` | `domain` | 网站域名 |
| `KeyTitle` | `title` | 网站标题 |
| `KeyDescription` | `description` | 网站描述 |
| `KeyThumbnail` | `thumbnail` | 网站缩略图 |
| `KeyLanguages` | `languages` | 网站语言 |
| `KeySiteContactEmail` | `contact.email` | 网站联系邮箱 |

#### 实例配置（limits / limits API）

| 常量 | 值 | 说明 |
|------|------|------|
| `KeyAccountMaxFeaturedTags` | `config.accounts.max_featured_tags` | 每个账号最多标签数 |
| `KeyStatusMaxCharacters` | `config.statuses.max_characters` | 每条推文最多字符数（`0` 为不限） |
| `KeyStatusMaxMedia` | `config.statuses.max_media_attachments` | 每条推文最多附件数（`0` 为不限） |
| `KeyStatusCharactersPerURL` | `config.statuses.characters_reserved_per_url` | 每条推文中每个 URL 占用字符数 |
| `KeyMediaMimeTypes` | `config.media_attachments.supported_mime_types` | 支持的附件类型 |
| `KeyMediaImageSizeLimit` | `config.media_attachments.image_size_limit` | 图片大小限制 |
| `KeyMediaImageMatrixLimit` | `config.media_attachments.image_matrix_limit` | 图片矩阵限制 |
| `KeyMediaVideoSizeLimit` | `config.media_attachments.video_size_limit` | 视频大小限制 |
| `KeyMediaVideoFrameRate` | `config.media_attachments.video_frame_rate_limit` | 视频帧率限制 |
| `KeyMediaVideoMatrixLimit` | `config.media_attachments.video_matrix_limit` | 视频矩阵限制 |
| `KeyPollMaxOptions` | `config.polls.max_options` | 投票最多选项 |
| `KeyPollMaxCharactersPerOption` | `config.polls.max_characters_per_option` | 每个投票选项最多字符数 |
| `KeyPollMinExpiration` | `config.polls.min_expiration` | 投票最短有效期 |
| `KeyPollMaxExpiration` | `config.polls.max_expiration` | 投票最长有效期 |
| `KeyDefaultPageSize` | `config.default_page_limit` | 默认分页大小 |

#### 注册与认证

| 常量 | 值 | 说明 |
|------|------|------|
| `KeyIsEnableRegistration` | `config.registration.enabled` | 是否启用注册 |
| `KeyRegistrationApprovalRequired` | `config.registration.approval_required` | 是否需要管理员审核 |
| `KeyRegistrationInviteRequired` | `config.registration.invite_required` | 是否需要邀请码 |
| `KeyRegistrationCheckEmail` | `config.registration.check_email` | 是否检查邮箱 |
| `KeyRegistrationMessage` | `config.registration.message` | 注册页面提示信息 |
| `KeyIsEnableTranslation` | `config.translations.enabled` | 是否启用翻译 |

#### 验证码（CAPTCHA）

| 常量 | 值 | 说明 |
|------|------|------|
| `KeyCaptchaProvider` | `config.captcha.provider` | 验证码供应商：`recaptcha` / `cloudflare` / `hcaptcha` / `none` |
| `KeyCaptchaSiteKey` | `config.captcha.site_key` | 验证码站点密钥 |
| `KeyCaptchaSecretKey` | `config.captcha.secret_key` | 验证码密钥 |
| `KeyCaptchaEnabled` | `config.captcha.enabled` | 是否启用验证码 |
| `KeyCaptchaScoreThreshold` | `config.captcha.score_threshold` | 验证码分数阈值（`0.0 - 1.0`） |

> 旧的 reCAPTCHA 专属键（`KeyRecaptchaSiteKey` / `KeyRecaptchaSecretKey` / `KeyRecaptchaEnabled`）仍保留以向后兼容，但已标记 deprecated，建议使用上表的通用 `KeyCaptcha*` 键。

#### 其它

| 常量 | 值 | 说明 |
|------|------|------|
| `KeyMediaAttachmentsBaseURL` | `config.media_attachments.base_url` | 附件基础 URL |
| `KeyMediaAttachmentsCacheBaseURL` | `config.media_attachments.cache_base_url` | 附件缓存基础 URL |

## 核心类型与函数

### 加载与缓存

#### `Load`

```go
func Load()
```

从数据库 `system_settings` 表加载全部键值到内存缓存。使用双重检查锁定：已加载时仅需读锁（快速路径），避免在频繁调用的 `Get` 中序列化所有读操作。数据库不可用时（standalone/test 模式）记录错误并标记为已加载，不 panic。

### 类型化 getter

所有 getter 内部均调用 `Get`，`defaultValue` 为 `any` 类型（可传具体类型或 `nil`）：

| 函数 | 返回类型 | 说明 |
|------|----------|------|
| `Get(key string, defaultValue any) any` | `any` | 原始值（`.env` > 数据库 > 默认值） |
| `GetInt(key, defaultValue any) int` | `int` | 经 `utils.ToInt` 转换 |
| `GetInt64(key, defaultValue any) int64` | `int64` | 经 `utils.ToInt64` 转换 |
| `GetFloat64(key, defaultValue any) float64` | `float64` | 经 `utils.ToFloat64` 转换 |
| `GetBool(key, defaultValue any) bool` | `bool` | 经 `utils.ToBool` 转换 |
| `GetString(key, defaultValue string) string` | `string` | 经 `utils.ToString` 转换（注意默认值类型为 `string`） |
| `GetStrings(key, defaultValue any) []string` | `[]string` | 安全处理 `[]string` / `[]any` / 单个 `string` |
| `GetInts(key, defaultValue any) []int` | `[]int` | 安全处理 `[]int` / `[]any` / 单个 `int` |
| `GetInt64s(key, defaultValue any) []int64` | `[]int64` | 安全处理 `[]int64` / `[]any` / 单个 `int64` |
| `GetMap(key, defaultValue any) map[string]any` | `map[string]any` | 仅当值为 `map[string]any` 时返回，否则 `nil` |

### 写入与持久化

#### `Set`

```go
func Set(key string, val any)
```

更新内存缓存（**不**写数据库）。线程安全（写锁保护）。需配合 [`SaveToDB`](#savetodb) 持久化。

#### `SaveToDB`

```go
func SaveToDB(keys ...string) error
```

将指定 `keys` 对应的内存值持久化到数据库（upsert：键存在则 `UPDATE val`，不存在则 `INSERT`），在单个事务内完成。

- 在读锁保护下拷贝待保存键值**快照**，事务内部使用快照写入，避免事务执行期间与并发 `Set` 产生 data race（Go map 并发读写会 fatal crash）。
- 数据库未初始化时（`db.GetDB() == nil`）返回错误。
- 空参数列表直接返回 `nil`。

### 导入 / 导出

#### `GetAllFlatStyle`

```go
func GetAllFlatStyle() map[string]any
```

返回内部缓存的一份**拷贝**（避免调用方意外修改内部状态），键保持原始点分形式（如 `config.statuses.max_characters`）。

#### `GetAllTreeStyle`

```go
func GetAllTreeStyle() map[string]any
```

将点分键展开为嵌套树形 map（如 `a.b.c = 1` → `map[a][b][c]=1`）。按键升序排序后逐层构建，对键冲突（标量占用中间节点）安全覆盖。

### 便捷函数

| 函数 | 说明 |
|------|------|
| `GetDefaultPageSize() int` | 默认分页大小（`GetInt(KeyDefaultPageSize, 20)`） |
| `GetDomain() string` | 站点域名（`GetString(KeyDomain, "")`） |

### `models.SystemSetting`

数据库模型（定义于 `models/system_setting.go`，表名 `system_settings`）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `ID` | `int64` | 主键，自增 |
| `Key` | `string` | 键（`varchar(64)`，唯一、非空） |
| `Val` | `any` | 值（JSON 序列化，`serializer:json`） |

## 并发安全说明

- 内部缓存使用 `sync.RWMutex` 保护：`Get*` 持读锁，`Set` / `Load` 持写锁。
- `Load` 采用双重检查锁定，已加载时走只读快速路径。
- `SaveToDB` / `GetAllFlatStyle` / `GetAllTreeStyle` 均在持锁期间构建快照/拷贝，返回的数据可安全被调用方修改。

## 依赖关系

| 依赖 | 用途 |
|------|------|
| `github.com/kasuganosora/community/config` | 读取 `.env` 配置（`config.C.Other`），作为最高优先级来源 |
| `github.com/kasuganosora/community/db` | `db.GetDB()` 获取数据库实例 |
| `github.com/kasuganosora/community/models` | `SystemSetting` 模型 |
| `github.com/kasuganosora/community/utils` | 类型转换辅助（`ToInt` / `ToBool` / `ToString` 等） |
| `github.com/kasuganosora/community/log` | 日志 |
| `gorm.io/gorm` | 数据库 ORM |
| `github.com/pkg/errors` | 错误包装 |

### 集成位置（部分）

| 调用方 | 使用 |
|--------|------|
| `controllers/api/common/instance.go` | 通过 `Get*` 读取站点信息与实例配置，组装 `/api/v2/instance` 响应 |
| `controllers/api/common/captcha.go` | 通过 `Set` + `SaveToDB` 更新验证码配置，再调用 `captcha.GetService().Reload()` |
| `controllers/api/account/login.go` / `oauth/oauth.go` | 读取 `KeyRegistrationCheckEmail` 判断是否需要邮箱验证 |
| `controllers/api/account/invites.go` | 读取 `KeyDomain` 拼接实例 API URL |
| `invitecode` 包 | 读取 `KeyRegistrationApprovalRequired` / `KeyRegistrationCheckEmail` 决定是否执行注册后动作 |
