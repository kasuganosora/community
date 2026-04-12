package captcha

import (
	"context"
	"fmt"

	"github.com/kasuganosora/community/captcha/types"
)

// VerificationResult 是 types.VerificationResult 的别名
type VerificationResult = types.VerificationResult

// Provider 验证码供应商接口
type Provider interface {
	// Name 返回供应商名称
	Name() string

	// Verify 验证用户响应
	// ctx: 上下文
	// response: 用户提交的验证码响应（如 reCAPTCHA 的 g-recaptcha-response）
	// remoteIP: 用户 IP 地址（可选）
	// 返回: 是否验证通过，以及错误信息
	Verify(ctx context.Context, response string, remoteIP string) (*types.VerificationResult, error)

	// GetSiteKey 获取站点密钥（用于前端展示）
	GetSiteKey() string

	// IsConfigured 检查供应商是否已正确配置
	IsConfigured() bool
}

// ProviderType 供应商类型
type ProviderType string

const (
	// ProviderRecaptcha Google reCAPTCHA
	ProviderRecaptcha ProviderType = "recaptcha"

	// ProviderCloudflare Cloudflare Turnstile
	ProviderCloudflare ProviderType = "cloudflare"

	// ProviderHCaptcha hCaptcha
	ProviderHCaptcha ProviderType = "hcaptcha"

	// ProviderNone 不使用验证码
	ProviderNone ProviderType = "none"
)

// String 返回字符串表示
func (pt ProviderType) String() string {
	return string(pt)
}

// ParseProviderType 解析供应商类型
func ParseProviderType(s string) (ProviderType, error) {
	switch s {
	case "recaptcha", "google":
		return ProviderRecaptcha, nil
	case "cloudflare", "turnstile":
		return ProviderCloudflare, nil
	case "hcaptcha":
		return ProviderHCaptcha, nil
	case "none", "":
		return ProviderNone, nil
	default:
		return "", fmt.Errorf("unknown captcha provider: %s", s)
	}
}

// Config 验证码配置
type Config struct {
	// Provider 供应商类型
	Provider ProviderType

	// SiteKey 站点密钥
	SiteKey string

	// SecretKey 密钥
	SecretKey string

	// Enabled 是否启用
	Enabled bool

	// ScoreThreshold 分数阈值（用于行为分析型验证码，0.0 - 1.0）
	ScoreThreshold float64

	// AdditionalConfig 额外配置（供应商特定）
	AdditionalConfig map[string]string
}
