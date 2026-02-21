# 验证码服务 (Captcha Service)

支持多供应商的验证码服务模块，包括 Google reCAPTCHA、Cloudflare Turnstile 和 hCaptcha。

## 支持的供应商

| 供应商 | ID | 描述 |
|--------|-----|------|
| Google reCAPTCHA | `recaptcha` | Google reCAPTCHA v2/v3 |
| Cloudflare Turnstile | `cloudflare` | 隐私友好的验证码替代方案 |
| hCaptcha | `hcaptcha` | 注重隐私的验证码服务 |
| 禁用 | `none` | 不使用验证码 |

## 配置

### 环境变量配置

```bash
# 验证码供应商: recaptcha, cloudflare, hcaptcha, none
CAPTCHA_PROVIDER=cloudflare

# 站点密钥（前端使用）
CLOUDFLARE_TURNSTILE_SITE_KEY=your-site-key

# 密钥（后端使用）
CLOUDFLARE_TURNSTILE_SECRET_KEY=your-secret-key
```

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

-- 分数阈值（仅适用于支持分数的供应商如 reCAPTCHA v3）
INSERT INTO system_settings (`key`, `val`) VALUES ('config.captcha.score_threshold', '0.5');
```

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
  }
]
```

### 管理员端点

#### 更新验证码配置
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
cf-turnstile-response = document.querySelector('[name="cf-turnstile-response"]').value;

// 发送到后端
fetch('/api/v1/accounts', {
  method: 'POST',
  body: JSON.stringify({
    // ... 其他字段
    'grecaptcha[response]': cf-turnstile-response
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

### 便捷函数

```go
// 快速验证
ok, err := captcha.SiteVerify(response, remoteIP)
if err != nil || !ok {
    // 验证失败
}

// 获取当前站点密钥
siteKey := captcha.GetCurrentSiteKey()

// 获取当前供应商
provider := captcha.GetCurrentProvider()
```

## 供应商特定说明

### Google reCAPTCHA

- 支持 reCAPTCHA v2 和 v3
- v3 返回分数（0.0 - 1.0），可配置阈值
- 免费额度：每月 100 万次验证

### Cloudflare Turnstile

- 隐私友好，不收集用户数据
- 无需显示验证码挑战（通常）
- 免费使用
- 不支持分数，只返回成功/失败

### hCaptcha

- 注重隐私保护
- 支持分数（企业版）
- 免费额度：每月 100 万次验证

## 迁移指南

### 从旧版 reCAPTCHA 迁移

旧版配置仍然兼容：
- `config.recaptcha.enabled` → `config.captcha.enabled`
- `config.recaptcha.site_key` → `config.captcha.site_key`
- `config.recaptcha.secret_key` → `config.captcha.secret_key`

建议迁移到新配置格式以获得更好的多供应商支持。
