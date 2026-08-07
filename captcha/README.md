# 验证码服务 (Captcha Service)

支持多供应商的验证码服务模块，包括 Google reCAPTCHA、Cloudflare Turnstile 和 hCaptcha。

## 支持的供应商

| 供应商 | ID | 别名 | 描述 |
|--------|-----|------|------|
| Google reCAPTCHA | `recaptcha` | `google` | Google reCAPTCHA v2/v3 |
| Cloudflare Turnstile | `cloudflare` | `turnstile` | 隐私友好的验证码替代方案 |
| hCaptcha | `hcaptcha` | - | 注重隐私的验证码服务 |
| 禁用 | `none` | - | 不使用验证码 |

> 供应商 ID 与别名均可通过 [`ParseProviderType`](#核心类型与函数) 解析。

## 配置

### 环境变量配置

验证码配置优先读取环境变量，其次读取系统设置。

```bash
# 验证码供应商: recaptcha, cloudflare, hcaptcha, none
CAPTCHA_PROVIDER=cloudflare

# Cloudflare Turnstile
CLOUDFLARE_TURNSTILE_SITE_KEY=your-site-key
CLOUDFLARE_TURNSTILE_SECRET_KEY=your-secret-key

# Google reCAPTCHA
CAPTCHA_SITE_KEY=your-site-key
CAPTCHA_SECRET_KEY=your-secret-key

# hCaptcha
HCAPTCHA_SITE_KEY=your-site-key
HCAPTCHA_SECRET_KEY=your-secret-key
```

> **自动启用**：当未显式设置 `config.captcha.enabled`，但已配置 `site_key` 与 `secret_key` 时，验证码服务会自动启用。

### 数据库配置

通过系统设置表配置：

```sql
-- 验证码供应商
INSERT INTO system_settings (`key`, `val`) VALUES ('config.captcha.provider', 'cloudflare');

-- 站点密钥
INSERT INTO system_settings (`key`, `val`) VALUES ('config.captcha.site_key', 'your-site-key');

-- 密钥
INSERT INTO system_settings (`key`, `val`) VALUES ('config.captcha.secret_key', 'your-secret-key');

-- 是否启用
INSERT INTO system_settings (`key`, `val`) VALUES ('config.captcha.enabled', 'true');

-- 分数阈值（仅适用于支持分数的供应商如 reCAPTCHA v3，默认 0.5）
INSERT INTO system_settings (`key`, `val`) VALUES ('config.captcha.score_threshold', '0.5');
```

各供应商对应的系统设置键：

| 供应商 | site_key 键 | secret_key 键 |
|--------|-------------|---------------|
| reCAPTCHA | `config.recaptcha.site_key` | `config.recaptcha.secret_key` |
| Cloudflare | `config.cloudflare.turnstile.site_key` | `config.cloudflare.turnstile.secret_key` |
| hCaptcha | `config.hcaptcha.site_key` | `config.hcaptcha.secret_key` |

## API 端点

### 公开端点

#### 获取验证码配置
```
GET /api/v1/captcha/config
```

响应：
```json
{
  "enabled": true,
  "provider": "cloudflare",
  "site_key": "your-site-key"
}
```

#### 获取支持的供应商列表
```
GET /api/v1/captcha/providers
```

响应：
```json
[
  {
    "id": "recaptcha",
    "name": "Google reCAPTCHA",
    "description": "Google reCAPTCHA v2/v3",
    "website": "https://www.google.com/recaptcha"
  },
  {
    "id": "cloudflare",
    "name": "Cloudflare Turnstile",
    "description": "Cloudflare Turnstile - Privacy-preserving CAPTCHA alternative",
    "website": "https://www.cloudflare.com/products/turnstile/"
  },
  {
    "id": "hcaptcha",
    "name": "hCaptcha",
    "description": "hCaptcha - Privacy-focused CAPTCHA",
    "website": "https://www.hcaptcha.com/"
  },
  {
    "id": "none",
    "name": "Disabled",
    "description": "No CAPTCHA verification",
    "website": ""
  }
]
```

### 管理员端点

#### 读取验证码配置（需要 `admin:read` 权限）
```
GET /api/v1/admin/captcha/config
```

#### 更新验证码配置（需要 `admin:write` 权限）
```
PUT /api/v1/admin/captcha/config
```

请求体：
```json
{
  "provider": "cloudflare",
  "site_key": "your-site-key",
  "secret_key": "your-secret-key",
  "enabled": true,
  "score_threshold": 0.5
}
```

- `provider`：必填，支持 `recaptcha`、`cloudflare`、`hcaptcha`、`none`
- `site_key`：站点密钥
- `secret_key`：密钥（只写，更新后不会回显）
- `enabled`：可选，是否启用
- `score_threshold`：可选，分数阈值

更新成功后服务会自动调用 `Reload()` 重新加载配置，无需重启。

## 前端集成

### Cloudflare Turnstile

```html
<!-- 在表单中添加 Turnstile -->
<div class="cf-turnstile" data-sitekey="your-site-key"></div>

<!-- 加载 Turnstile 脚本 -->
<script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script>
```

```javascript
// 提交表单时获取响应
const turnstileResponse = document.querySelector('[name="cf-turnstile-response"]').value;

// 发送到后端（注册接口使用 grecaptcha[response] 字段名）
fetch('/api/v1/accounts', {
  method: 'POST',
  body: JSON.stringify({
    // ... 其他字段
    'grecaptcha[response]': turnstileResponse
  })
});
```

### Google reCAPTCHA

```html
<!-- reCAPTCHA v2 -->
<div class="g-recaptcha" data-sitekey="your-site-key"></div>

<script src="https://www.google.com/recaptcha/api.js" async defer></script>
```

### hCaptcha

```html
<!-- hCaptcha -->
<div class="h-captcha" data-sitekey="your-site-key"></div>

<script src="https://js.hcaptcha.com/1/api.js" async defer></script>
```

## 后端使用

### 基本用法

```go
import "github.com/kasuganosora/community/captcha"

// 检查验证码是否启用
if captcha.IsCaptchaEnabled() {
    // 验证用户响应
    result, err := captcha.GetService().Verify(ctx, response, remoteIP)
    if err != nil {
        // 处理错误
        return err
    }

    if !result.Success {
        // 验证失败
        return errors.New("captcha verification failed")
    }
}
```

> 当验证码未启用时，`Verify` 会直接返回 `Success: true` 的结果，无需额外判断。

### 便捷函数

```go
// 快速验证（使用 context.Background()）
ok, err := captcha.SiteVerify(response, remoteIP)
if err != nil || !ok {
    // 验证失败
}

// 获取当前站点密钥
siteKey := captcha.GetCurrentSiteKey()

// 获取当前供应商（返回字符串，如 "cloudflare"）
provider := captcha.GetCurrentProvider()
```

### 自定义服务实例

```go
cfg := &captcha.Config{
    Provider:       captcha.ProviderCloudflare,
    SiteKey:        "your-site-key",
    SecretKey:      "your-secret-key",
    Enabled:        true,
    ScoreThreshold: 0.5,
}

service, err := captcha.NewService(cfg)
if err != nil {
    // 处理错误
}

// 运行时重新加载配置（从系统设置/环境变量）
if err := service.Reload(); err != nil {
    // 处理错误
}
```

## 核心类型与函数

### 类型

#### `Provider`（接口）

验证码供应商接口，所有供应商实现均位于 `captcha/providers` 子包：

```go
type Provider interface {
    Name() string
    Verify(ctx context.Context, response string, remoteIP string) (*VerificationResult, error)
    GetSiteKey() string
    IsConfigured() bool
}
```

#### `ProviderType`

供应商类型，定义了以下常量：

| 常量 | 值 |
|------|----|
| `ProviderRecaptcha` | `"recaptcha"` |
| `ProviderCloudflare` | `"cloudflare"` |
| `ProviderHCaptcha` | `"hcaptcha"` |
| `ProviderNone` | `"none"` |

#### `Config`

验证码配置结构体：

| 字段 | 类型 | 说明 |
|------|------|------|
| `Provider` | `ProviderType` | 供应商类型 |
| `SiteKey` | `string` | 站点密钥 |
| `SecretKey` | `string` | 密钥 |
| `Enabled` | `bool` | 是否启用 |
| `ScoreThreshold` | `float64` | 分数阈值（用于行为分析型验证码，0.0 - 1.0） |
| `AdditionalConfig` | `map[string]string` | 额外配置（供应商特定） |

#### `VerificationResult`

验证结果结构体，所有供应商共用：

| 字段 | 类型 | 说明 |
|------|------|------|
| `Success` | `bool` | 是否验证通过 |
| `Score` | `float64` | 分数（0.0 - 1.0，用于行为分析型验证码如 reCAPTCHA v3） |
| `Action` | `string` | 动作标识（如 reCAPTCHA v3 的 action） |
| `ChallengeTS` | `string` | 挑战时间戳 |
| `Hostname` | `string` | 验证的主机名 |
| `ErrorCodes` | `[]string` | 错误代码列表 |
| `Provider` | `string` | 供应商名称 |

#### `Service`

验证码服务，线程安全（内部使用 `sync.RWMutex` 保护）。

### 函数

#### 包级函数

| 函数 | 说明 |
|------|------|
| `NewService(cfg *Config) (*Service, error)` | 创建验证码服务，`cfg` 为 nil 时从系统设置加载 |
| `GetService() *Service` | 获取全局验证码服务实例（单例，使用 `sync.Once` 初始化） |
| `ParseProviderType(s string) (ProviderType, error)` | 解析供应商类型，支持 ID 与别名（`google`→`recaptcha`、`turnstile`→`cloudflare`） |
| `SiteVerify(response, remoteIP string) (bool, error)` | 便捷验证函数（使用全局服务） |
| `IsCaptchaEnabled() bool` | 便捷的启用状态检查 |
| `GetCurrentSiteKey() string` | 便捷的获取站点密钥 |
| `GetCurrentProvider() string` | 便捷的获取供应商类型（字符串形式） |

#### `Service` 方法

| 方法 | 说明 |
|------|------|
| `Verify(ctx, response, remoteIP) (*VerificationResult, error)` | 验证用户响应；未启用时直接返回成功 |
| `IsEnabled() bool` | 检查验证码是否启用 |
| `GetProvider() Provider` | 获取当前供应商实例 |
| `GetProviderType() ProviderType` | 获取当前供应商类型 |
| `GetSiteKey() string` | 获取站点密钥 |
| `Reload() error` | 从系统设置重新加载配置 |

## 供应商特定说明

### Google reCAPTCHA

- 支持 reCAPTCHA v2 和 v3
- v3 返回分数（0.0 - 1.0），可配置阈值
- 免费额度：每月 100 万次验证

### Cloudflare Turnstile

- 隐私友好，不收集用户数据
- 无需显示验证码挑战（通常）
- 免费使用
- 不支持分数，验证结果中 `Score` 恒为 `0.0`，只返回成功/失败

### hCaptcha

- 注重隐私保护
- 支持分数（企业版）
- 免费额度：每月 100 万次验证

## 迁移指南

### 从旧版 reCAPTCHA 迁移

旧版配置仍然兼容（在未配置新格式 `config.captcha.provider` 时，若 `config.recaptcha.enabled` 为真，会自动识别为 reCAPTCHA 供应商）：

- `config.recaptcha.enabled` → `config.captcha.enabled`
- `config.recaptcha.site_key` → `config.captcha.site_key`
- `config.recaptcha.secret_key` → `config.captcha.secret_key`

建议迁移到新配置格式以获得更好的多供应商支持。
