# email (邮件服务)

基于 SMTP 协议的邮件发送服务模块，支持纯文本邮件、HTML 模板邮件以及内嵌附件邮件的发送。内置模板加载、`cid://` 内联资源自动提取和 BCC 密送转发。

## 核心功能

- **SMTP 邮件发送**：通过 `smtp` 子包的 `Drive` 实现，支持隐式 TLS（端口 465）和 STARTTLS（端口 587）两种加密模式。
- **多收件人 / 抄送 / 密送**：`to` / `cc` 字段以分号 `;` 分隔多个地址，`bcc` 会被单独转发一封邮件。
- **HTML 模板邮件**：基于 `html/template` 渲染 `mails/` 目录下的模板，并自动将 `src="cid://..."` 内联资源转为 MIME 内嵌附件。
- **附件支持**：支持普通附件与内嵌附件（带 `ContentId`）。
- **模板白名单**：仅允许预定义的模板前缀，防止路径注入。

## 配置

### 环境变量配置

```bash
# 邮件发送模式（当前仅支持 smtp）
EMAIL_MODE=smtp

# SMTP 服务器地址（留空则禁用邮件发送）
SMTP_SERVER=smtp.example.com

# SMTP 端口（默认 587，隐式 TLS 一般用 465）
SMTP_PORT=587

# SMTP 用户名（同时作为发件人地址）
SMTP_USERNAME=noreply@example.com

# SMTP 密码
SMTP_PASSWORD=your-password

# 发件人地址
SMTP_FROM=noreply@example.com

# 认证方式（默认 PLAIN）
SMTP_AUTH_METHOD=PLAIN

# 连接超时时间，单位秒（默认 10）
SMTP_TIMEOUT=10

# 是否启用 TLS 加密传输（默认 true，设为 false 则尝试 STARTTLS）
SMTP_TLS=true
```

> 当 `SMTP_SERVER` 为空时，邮件服务不会初始化（`useDrive == nil`），所有发送函数将静默返回，不会报错。

### 配置说明

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `EMAIL_MODE` | `smtp` | 邮件发送模式 |
| `SMTP_SERVER` | （空） | SMTP 服务器地址，留空禁用邮件 |
| `SMTP_PORT` | `587` | SMTP 端口 |
| `SMTP_USERNAME` | （空） | SMTP 用户名，同时作为发件人 |
| `SMTP_PASSWORD` | （空） | SMTP 密码 |
| `SMTP_FROM` | （空） | 发件人地址 |
| `SMTP_AUTH_METHOD` | `PLAIN` | SMTP 认证方式 |
| `SMTP_TIMEOUT` | `10` | 连接超时（秒） |
| `SMTP_TLS` | `true` | 是否启用 TLS |

### TLS 模式说明

| `SMTP_TLS` | 行为 | 典型端口 |
|------------|------|----------|
| `true`（默认） | 隐式 TLS，建立连接时即握手加密 | 465 |
| `false` | 明文连接，若服务器支持则升级为 STARTTLS，否则以明文发送（产生警告日志） | 587 |

## 后端使用

```go
import "github.com/kasuganosora/community/email"
```

### 发送纯文本邮件

```go
err := email.SendText(
    "user@example.com",            // to
    "admin@example.com",           // cc
    "",                            // bcc
    "欢迎注册",                     // subject
    "请点击以下链接验证邮箱",        // textBody（纯文本）
    "<p>请点击以下链接验证邮箱</p>", // body（HTML）
)
if err != nil {
    // 处理发送失败
    log.Error("发送邮件失败: %v", err)
}
```

### 发送 HTML 模板邮件

```go
err := email.SendHtmlTemplate(
    "user@example.com",  // to
    "",                  // cc
    "",                  // bcc
    "验证你的邮箱",        // subject
    "请验证邮箱",         // textBody
    "verify_email_en",   // templateName（mails/verify_email_en.html）
    map[string]any{
        "Username": "Alice",
        "Link":     "https://example.com/verify?token=xxx",
    },
)
```

### 发送 HTML 模板邮件（含额外附件）

```go
import "github.com/kasuganosora/community/email/types"

file, _ := os.Open("report.pdf")

err := email.SendHtmlTemplateAndAttachments(
    "user@example.com",
    "",
    "",
    "月度报告",
    "请查收附件",
    "welcome_mail_en",
    map[string]any{"Month": "2025-01"},
    []*types.MailAttachment{
        {
            File: file,
            Name: "report.pdf",
        },
    },
)
```

### 内嵌资源说明

HTML 模板中可使用 `cid://` 引用内嵌资源，发送时会自动读取 `mails/` 目录下对应文件并转为 MIME 内嵌附件：

```html
<!-- mails/welcome_mail.html -->
<img src="cid://images/logo.png" alt="logo" />
```

渲染后，`cid://images/logo.png` 会被替换为带 `ContentId` 的内嵌引用。

## 支持的模板

模板名必须匹配以下白名单前缀（可追加语言后缀，如 `_en`、`_zh`）：

| 模板前缀 | 用途 |
|----------|------|
| `email_confirmation` | 邮箱确认 |
| `recover_password_verify_email` | 找回密码验证 |
| `verify_email` | 邮箱验证 |
| `welcome_mail` | 欢迎邮件 |

模板文件存放于 `mails/` 目录下（通过 `internalfs` 内嵌文件系统加载）。

## API 参考

### 邮件发送函数

| 函数 | 说明 |
|------|------|
| `SendText(to, cc, bcc, subject, textBody, body string) error` | 发送纯文本/HTML 邮件 |
| `SendHtmlTemplate(to, cc, bcc, subject, textBody, templateName string, data map[string]any) error` | 使用 HTML 模板发送邮件 |
| `SendHtmlTemplateAndAttachments(to, cc, bcc, subject, textBody, templateName string, data map[string]any, attachments []*types.MailAttachment) error` | 使用 HTML 模板并发送额外附件 |

### types 子包

```go
// Mail 邮件对象
type Mail struct {
    To          string
    Cc          string
    Subject     string
    Attachments []*MailAttachment
    Body        string
    TextBody    string
}

// MailAttachment 邮件附件
type MailAttachment struct {
    File      fs.File
    Name      string
    ContentId string
}

// Drive 邮件发送驱动接口
type Drive interface {
    SendMail(to string, cc string, mailObject *Mail) (err error)
}
```

### smtp 子包

| API | 说明 |
|-----|------|
| `smtp.NewDrive() *Drive` | 根据配置创建 SMTP 驱动，未配置 SMTP 时返回 `nil` |
| `(*Drive).Send(to, cc string, body []byte) error` | 发送原始邮件字节 |
| `(*Drive).SendMail(to, cc string, mailObject *types.Mail) error` | 发送 `Mail` 对象（自动构建 MIME） |

## 依赖关系

### 外部依赖

| 依赖 | 用途 |
|------|------|
| `github.com/jhillyerd/enmime` | MIME 邮件构建（multipart、附件、内嵌资源） |
| `github.com/google/uuid` | 内嵌附件 `ContentId` 生成 |
| `github.com/pkg/errors` | 错误包装与堆栈追踪 |

### 内部依赖

| 包 | 用途 |
|----|------|
| `config` | SMTP 配置（`config.C.SmtpServer` 等） |
| `internalfs` | 内嵌模板与附件文件系统（`mails/` 目录） |
| `log` | 警告与错误日志 |
| `utils` | 收件人地址去重与空值过滤 |
