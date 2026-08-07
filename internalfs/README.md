# internalfs (内部文件系统)

应用内置静态资源的统一文件系统抽象层。它把邮件模板、i18n 翻译文件等静态资源以 `io/fs.FS` 的形式暴露给调用方，使上层代码无需关心资源究竟来自磁盘还是嵌入二进制。开发模式下支持热重载，生产模式则产出自包含、只读、防篡改的单体二进制。

## 核心功能

| 功能 | 说明 |
|------|------|
| embed.FS 嵌入资源 | 通过 `//go:embed all:resources` 在编译期把 `resources/` 目录整体打进二进制 |
| 双模式切换 | `config.C.DevMode == "true"` 时从磁盘实时读取（可热重载），其余情况返回只读的嵌入副本 |
| 统一虚拟路径 | 所有路径相对于 FS 根，如 `mails/foo.html`、`i18n/en.toml`，与 `os.DirFS` 路径一致 |
| 路径穿越防护 | `SecureJoin` / `SafeOpen` 对输入做清洗并拒绝越界访问，防御 `mails/../../etc/passwd` 等注入 |
| 安全优先 | `init()` 阶段若 `fs.Sub` 失败会立即 `panic`，避免悄悄交付一个空文件系统 |

## 架构 / 结构

```
internalfs/
├── fs.go            # 核心实现：Get / SafeOpen / SecureJoin
├── fs_test.go       # 单元测试，含路径穿越攻击向量
└── resources/       # 被 embed 嵌入的资源目录（顶层 resources/ 的镜像）
    ├── i18n/        # i18n 翻译文件
    │   └── en.toml
    ├── mails/       # HTML 邮件模板
    │   ├── welcome_mail_en.html
    │   ├── verify_email_en.html
    │   └── recover_password_verify_email_en.html
    └── oauth_authorize.html   # OAuth 授权页
```

### 设计要点

- **`//go:embed all:resources`**：`all:` 前缀确保以 `.` 或 `_` 开头的文件也被嵌入（默认规则会排除它们）。
- **sub-rooted FS**：通过 `fs.Sub(embedRoot, "resources")` 把虚拟根重新定锚到 `resources/`，使嵌入模式与开发模式的 `os.DirFS("resources")` 路径完全对齐（统一为 `mails/...`、`i18n/...`）。
- **懒解析**：`embeddedFS` 在 `init()` 中解析一次；失败属编译/构建缺陷，故 fail-fast。
- **模式判定**：`Get()` 只在 `DevMode` 严格等于 `"true"` 时走磁盘，任何其他值（`""`、`"false"`、拼写错误等）都回退到嵌入副本 —— 保证配置缺失时不会意外从磁盘读取未校验文件。

### 运行模式对比

| 维度 | 开发模式（`DevMode == "true"`） | 生产模式（其他值） |
|------|-------------------------------|-------------------|
| 后端 | `os.DirFS("<exe>/resources")` | 嵌入的 `embed.FS`（sub-rooted） |
| 可写性 | 随磁盘，编辑后即时生效 | 只读 |
| 安全性 | 受 `SafeOpen` 保护 | 只读且防篡改 |
| 典型场景 | 本地开发、改模板免重编译 | 容器部署、单体分发 |

## 用法示例

### 读取资源（基本用法）

最常见模式：拿到 `fs.FS` 后直接 `Open`。注意返回的 `fs.File` 需由调用方关闭。

```go
import "github.com/kasuganosora/community/internalfs"

f, err := internalfs.Get().Open("i18n/en.toml")
if err != nil {
    // 处理错误
}
defer func() { _ = f.Close() }()

data, err := io.ReadAll(f)
```

### 渲染邮件模板（真实用法）

取自 `email/send.go`：从嵌入 FS 读取 HTML 模板并执行渲染。

```go
// templateName 形如 "welcome_mail_en"
templateFile, err := internalfs.Get().Open("mails/" + templateName + ".html")
if err != nil {
    return err
}
defer func() { _ = templateFile.Close() }()

templateContent, err := io.ReadAll(templateFile)
if err != nil {
    return err
}

tpl, err := template.New(templateName).Parse(string(templateContent))
if err != nil {
    return err
}

err = tpl.ExecuteTemplate(buf, templateName, data)
```

> 模板内嵌的图片资源通过 `cid://<path>` 引用，`email` 包在 `extractAttachments` 中同样用 `internalfs.Get().Open("mails/" + ref)` 读取（见 `email/send.go`）。

### 安全打开（防路径穿越）

当文件名来自不可信输入（如用户请求、模板参数）时，使用 `SafeOpen` 而非直接 `Open`：

```go
// 先用 SecureJoin 清洗路径（拒绝越界），再交给 FS 打开
f, err := internalfs.SafeOpen(internalfs.Get(), userInput)
if err != nil {
    // err 为 fs.ErrInvalid（越界）或 fs.ErrNotExist
    return err
}
defer func() { _ = f.Close() }()
```

`SafeOpen` 内部调用 `SecureJoin`，会拒绝以下攻击向量：

| 输入 | 结果 |
|------|------|
| `mails/../../etc/passwd` | `fs.ErrInvalid` |
| `../../../etc/passwd` | `fs.ErrInvalid` |
| `/etc/passwd` | `fs.ErrInvalid`（绝对路径） |
| `mails\..\..\etc\passwd` | `fs.ErrInvalid`（反斜杠已归一化） |
| `mails/foo.html` | 正常打开 |

> 这是纵深防御：`embed.FS` 和 `os.DirFS` 本身也会拒绝 `..`，但显式、可审计的检查使安全契约清晰，并能保护未来的任何后端实现。

### 仅清洗路径（不打开）

如需在打开前做自定义校验，可单独调用 `SecureJoin`：

```go
clean, err := internalfs.SecureJoin(untrustedName)
if err != nil {
    return err
}
// clean 已归一化，例如 "mails//./foo.html" -> "mails/foo.html"
```

## 配置说明

`internalfs` 的行为仅由一项全局配置控制：

| 配置项 | 来源 | 默认值 | 作用 |
|--------|------|--------|------|
| `config.C.DevMode` | `DEV_MODE` 环境变量 | `"false"` | 为 `"true"` 时从磁盘读取 `resources/`，否则读取嵌入副本 |

磁盘模式下，资源根目录通过 `utils.GetFullPath("resources")` 解析（位于可执行文件同级）。无需数据库配置。

## 核心类型与函数

### 变量

| 变量 | 类型 | 说明 |
|------|------|------|
| `embedRoot` | `embed.FS` | `//go:embed all:resources` 嵌入的完整资源树（包内使用） |
| `embeddedFS` | `fs.FS` | sub-rooted 到 `"resources"` 的嵌入 FS，生产模式下由 `Get()` 返回（包内使用） |

### 函数

| 函数 | 签名 | 说明 |
|------|------|------|
| `Get` | `func Get() fs.FS` | 返回资源文件系统：`DevMode=="true"` 返回磁盘 `os.DirFS`，否则返回嵌入副本 |
| `SafeOpen` | `func SafeOpen(fsys fs.FS, name string) (fs.File, error)` | 经 `SecureJoin` 清洗后打开文件，防止路径穿越；返回的 `fs.File` 由调用方关闭 |
| `SecureJoin` | `func SecureJoin(name string) (string, error)` | 清洗不可信的 FS 根相对路径，验证其不越界；返回归一化路径，越界返回 `fs.PathError` |

`SecureJoin` 的清洗规则：

1. 空字符串 → `fs.ErrNotExist`。
2. 反斜杠统一替换为正斜杠（防 Windows 输入绕过）。
3. 绝对路径 → `fs.ErrInvalid`。
4. `path.Clean` 词法解析 `.`/`..`，若结果以 `..` 开头 → `fs.ErrInvalid`。
5. 清洗后为 `.`（根）→ `fs.ErrNotExist`（根不是文件）。

## 依赖关系

### 内部依赖

| 依赖 | 用途 |
|------|------|
| `config` | 读取 `config.C.DevMode` 决定运行模式 |
| `utils` | `utils.GetFullPath("resources")` 解析开发模式下的磁盘资源根路径 |

### 标准库

| 包 | 用途 |
|----|------|
| `embed` | `//go:embed` 编译期资源嵌入 |
| `io/fs` | `fs.FS` / `fs.Sub` / `fs.PathError` 抽象 |
| `os` | 开发模式下的 `os.DirFS` |
| `path` | 虚拟路径的 `path.Clean` / `path.IsAbs`（跨平台一致，始终用正斜杠） |

### 主要调用方

| 调用方 | 用途 |
|--------|------|
| [`i18nz`](../i18nz) | `init()` 中通过 `internalfs.Get()` 加载 `i18n/en.toml`；`GetMailTemplateName` 检查模板文件是否存在 |
| `email/send.go` | 读取 `mails/*.html` 邮件模板及 `cid://` 内嵌图片附件 |
