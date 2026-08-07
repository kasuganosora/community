# i18nz (国际化系统)

基于 [go-i18n](https://github.com/nicksnyder/go-i18n) 与 `golang.org/x/text` 的多语言翻译模块，为应用提供消息翻译、语言匹配（language matching）以及邮件模板的本地化解析能力。所有翻译资源通过 [`internalfs`](../internalfs) 统一加载，无需在调用方处理文件路径。

## 核心功能

| 功能 | 说明 |
|------|------|
| 多语言支持 | 以 TOML 翻译文件驱动，通过 [`GetLocalizer`](#核心类型与函数) 创建指定语言的翻译器 |
| 热重载 | 翻译文件经 `internalfs` 加载；开发模式下可直接编辑磁盘上的 `resources/i18n/*.toml` 而无需重新编译 |
| 复数化（Pluralization） | 通过 `O{Key: "PluralCount", Value: "5"}` 选项启用 go-i18n 的 CLDR 复数规则 |
| 安全回退 | 翻译缺失时，[`T`](#t) 永不 panic，按「已渲染消息 → `Default` 选项 → 消息 ID」的顺序回退 |
| 邮件模板解析 | [`GetMailTemplateName`](#getmailtemplatename) 按「完整 tag → 基础语言 → `en`」三级链路解析模板名 |
| Accept-Language 解析 | `lang` / `accept` 参数既支持 `"zh-CN"` 简单代码，也支持完整 header（如 `"en-US,en;q=0.9"`） |
| 线程安全 | `language.Matcher` 通过 `sync.Once` 懒加载并缓存，并发调用安全 |

## 资源文件

翻译消息以 TOML 格式存放于 `resources/i18n/` 目录（经 `internalfs` 嵌入二进制），文件名为 BCP 47 语言标签，例如：

```
resources/i18n/
├── en.toml    # 默认语言（English），必须存在
├── zh-CN.toml # 简体中文
└── ja.toml    # 日语
```

每个文件是一个由消息 ID 索引的 TOML 表，支持 `other`/`one`/`other` 等 CLDR 复数形式：

```toml
[welcome_mail_subject]
other = "[{{.SiteName}}] Welcome"
description = "欢迎邮件标题"

[items_count]
one   = "You have {{.Count}} item."
other = "You have {{.Count}} items."
```

> **注意**：`en.toml` 是默认语言，在 `init()` 阶段强制加载；若该文件缺失或解析失败会直接 `panic`。其余语言文件按需加载。

## 用法示例

### 基本翻译

```go
import "github.com/kasuganosora/community/i18nz"

// 根据请求的 Accept-Language 创建翻译器
acceptLanguage := c.Request.Header.Get("Accept-Language") // 如 "zh-CN,zh;q=0.9,en;q=0.8"
loc := i18nz.GetLocalizer("", acceptLanguage)

// 翻译消息（模板变量通过 data 传入）
subject := loc.T("welcome_mail_subject", map[string]any{
    "SiteName": "Community",
})
```

### 复数化（Pluralization）

通过 `O` 选项传入 `PluralCount`，触发 go-i18n 的复数规则匹配：

```go
msg := loc.T("items_count",
    map[string]any{"Count": 5},
    i18nz.O{Key: "PluralCount", Value: "5"},
)
// => "You have 5 items."
```

### 提供默认值

当消息 ID 在所有语言中都缺失时，可指定 `Default` 选项作为兜底：

```go
msg := loc.T("some_optional_key", nil,
    i18nz.O{Key: "Default", Value: "Fallback Subject"},
)
// => "Fallback Subject"
```

### 解析邮件模板名（实际用法）

完整示例取自 `controllers/api/account/register.go`，根据请求语言自动选择本地化邮件模板与标题：

```go
acceptLanguage := c.Request.Header.Get("Accept-Language")

// 1) 解析目标模板名（含语言后缀），实现模板的多语言回退
mailTemplate := i18nz.GetMailTemplateName(acceptLanguage, "welcome_mail")
// => "welcome_mail_en" / "welcome_mail_zh-CN" / ...

// 2) 用同一个语言翻译邮件标题
subject := i18nz.GetLocalizer("", acceptLanguage).T("welcome_mail_subject", map[string]any{
    "UserName": user.UserName,
    "SiteName": "Community",
})

// 3) 模板名直接交给 email 包（内部通过 internalfs 读取）
err := email.SendHtmlTemplate(req.Email, "", "", subject, "", mailTemplate, mailData)
```

> `GetMailTemplateName` 同时支持用户偏好语言：当 `user.Properties["language"]` 存在时，优先使用该值而非 header（见 `recover_password.go`）。

## 配置说明

i18nz 没有独立的数据库或环境变量配置，其行为由以下因素决定：

| 配置项 | 来源 | 作用 |
|--------|------|------|
| 翻译文件 | `resources/i18n/*.toml`（经 `internalfs`） | 提供各语言的翻译消息 |
| 开发模式 | `config.C.DevMode == "true"` | 由 `internalfs` 决定是从磁盘热重载还是读取嵌入副本 |
| `GO_TEST_MODE` | 环境变量 | 设为 `"1"`（或命令行含 `-test.` 参数）时，`init()` 跳过翻译文件加载，便于单元测试 |

> 默认语言固定为 English（`language.English`），在 `init()` 中作为 `Bundle` 的基础语言。

## 核心类型与函数

### 类型

#### `Localizer`

对 `i18n.Localizer` 的轻量包装，由 [`GetLocalizer`](#getlocalizer) 创建：

```go
type Localizer struct {
    i18n.Localizer
}
```

#### `O`

翻译选项，以可变参数形式传给 `T`：

| 字段 | 类型 | 说明 |
|------|------|------|
| `Key` | `string` | 选项键，目前支持 `"PluralCount"` 与 `"Default"` |
| `Value` | `string` | 选项值 |

#### `Bundle`（包级变量）

全局的 `*i18n.Bundle`，在 `init()` 中以 English 为默认语言初始化，并注册 `"toml"` 反序列化函数。所有翻译消息最终都挂载在它上面。

### 函数

#### 包级函数

| 函数 | 说明 |
|------|------|
| `GetLocalizer(lang, accept string) *Localizer` | 创建翻译器；`accept` 经 `language.Matcher` 解析，`lang` 作为补充候选，两者均可为空 |
| `GetMailTemplateName(lang, templateName string) string` | 解析带语言后缀的邮件模板名，三级回退：`<name>_<full>` → `<name>_<base>` → `<name>_en` |

#### `Localizer` 方法

| 方法 | 说明 |
|------|------|
| `T(id string, data map[string]any, options ...O) string` | 翻译消息 ID；支持模板变量、复数化、默认值，缺失时回退到 ID 本身，永不 panic |

## 回退行为详解

`T` 按以下优先级返回结果，确保任何情况下都有可用文案：

1. **已渲染的本地化消息**：包括请求语言缺失某 key 但默认语言（English）提供了它的情况 —— go-i18n 此时会同时返回消息与一个 `MessageNotFoundErr`，`T` 会优先采用非空消息而非错误。
2. **`Default` 选项值**：仅当上一步完全无消息时使用。
3. **消息 ID 本身**：最后的兜底，保证 UI 永远不显示空白。

> 见 `TestT_UsesDefaultLanguageOnMissingKey` 与 `TestT_StillFallsBackToIDWhenTotallyMissing`，二者共同锁定了该回退契约。

## 语言匹配机制

- `getMatcher()` 基于 `Bundle.LanguageTags()` 构建 `language.Matcher`，并通过 `sync.Once` 缓存，所有后续调用复用同一实例。
- **约束**：所有消息文件必须在首次调用 `getMatcher()` 之前加载完毕。本仓库中此加载发生在 `init()`，故安全。
- `resolveLanguage` 将输入规范化为 BCP 47 tag：空或不可解析的输入回退为 `"en"`；形如 `"en-US,en;q=0.9"` 的 header 通过 matcher 提取最佳匹配。
- `baseLanguageTag` 提取基础语言（保留 script 子标签），例如 `"zh-Hans-CN"` → `"zh-Hans"`，供邮件模板回退使用。

## 依赖关系

### 外部依赖

| 依赖 | 用途 |
|------|------|
| `github.com/nicksnyder/go-i18n/v2` | 核心翻译引擎（`Bundle` / `Localizer` / CLDR 复数规则） |
| `github.com/pelletier/go-toml/v2` | TOML 翻译文件反序列化 |
| `golang.org/x/text/language` | BCP 47 语言标签解析与 `Matcher` |

### 内部依赖

| 依赖 | 用途 |
|------|------|
| [`internalfs`](../internalfs) | 通过 `internalfs.Get()` 读取嵌入的 `i18n/*.toml`，实现开发模式热重载 |

### 主要调用方

| 调用方 | 用途 |
|--------|------|
| `controllers/api/account/register.go` | 注册流程发送欢迎邮件 / 邮箱验证邮件的本地化标题与模板 |
| `controllers/api/account/recover_password.go` | 找回密码邮件的本地化（优先使用用户 `language` 偏好） |
