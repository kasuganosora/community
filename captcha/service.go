package captcha

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/kasuganosora/community/captcha/providers"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/systemsettings"
)

// Service 验证码服务.
type Service struct {
	provider       Provider
	providerType   ProviderType
	enabled        bool
	scoreThreshold float64
	mu             sync.RWMutex
}

// globalService 全局验证码服务实例.
var globalService *Service
var once sync.Once

// NewService 创建验证码服务.
func NewService(cfg *Config) (*Service, error) {
	if cfg == nil {
		cfg = loadConfigFromSettings()
	}

	service := &Service{
		enabled:        cfg.Enabled,
		providerType:   cfg.Provider,
		scoreThreshold: cfg.ScoreThreshold,
	}

	if cfg.Provider != ProviderNone && cfg.Enabled {
		provider, err := createProvider(cfg)
		if err != nil {
			return nil, err
		}

		service.provider = provider
	}

	return service, nil
}

// GetService 获取全局验证码服务实例.
func GetService() *Service {
	once.Do(func() {
		cfg := loadConfigFromSettings()

		service, err := NewService(cfg)
		if err != nil {
			log.Error("Failed to initialize captcha service: %v", err)
			// 返回一个禁用状态的服务
			service = &Service{
				enabled:      false,
				providerType: ProviderNone,
			}
		}

		globalService = service
	})

	return globalService
}

// Reload 重新加载配置.
func (s *Service) Reload() error {
	cfg := loadConfigFromSettings()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.enabled = cfg.Enabled
	s.providerType = cfg.Provider
	s.scoreThreshold = cfg.ScoreThreshold

	if cfg.Provider != ProviderNone && cfg.Enabled {
		provider, err := createProvider(cfg)
		if err != nil {
			return err
		}

		s.provider = provider
	} else {
		s.provider = nil
	}

	return nil
}

// Verify 验证用户响应.
func (s *Service) Verify(ctx context.Context, response string, remoteIP string) (*VerificationResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.enabled {
		// 如果验证码未启用，直接返回成功
		return &VerificationResult{
			Success:  true,
			Provider: "none", //nolint:goconst
		}, nil
	}

	if s.provider == nil {
		return nil, errors.New("captcha provider is not initialized")
	}

	if response == "" {
		return &VerificationResult{
			Success:    false,
			Provider:   s.provider.Name(),
			ErrorCodes: []string{"missing-input-response"},
		}, errors.New("captcha response is empty")
	}

	result, err := s.provider.Verify(ctx, response, remoteIP)
	if err != nil {
		return nil, err
	}

	// 如果配置了分数阈值，进行额外检查
	if s.scoreThreshold > 0 && result.Score > 0 && result.Score < s.scoreThreshold {
		result.Success = false
		result.ErrorCodes = append(result.ErrorCodes, "score-too-low")
	}

	return result, nil
}

// IsEnabled 检查验证码是否启用.
func (s *Service) IsEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.enabled
}

// GetProvider 获取当前供应商.
func (s *Service) GetProvider() Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.provider
}

// GetProviderType 获取当前供应商类型.
func (s *Service) GetProviderType() ProviderType {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.providerType
}

// GetSiteKey 获取站点密钥.
func (s *Service) GetSiteKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.provider == nil {
		return ""
	}

	return s.provider.GetSiteKey()
}

// createProvider 根据配置创建供应商实例.
func createProvider(cfg *Config) (Provider, error) {
	switch cfg.Provider {
	case ProviderRecaptcha:
		return providers.NewRecaptchaProvider(cfg.SiteKey, cfg.SecretKey), nil
	case ProviderCloudflare:
		return providers.NewCloudflareProvider(cfg.SiteKey, cfg.SecretKey), nil
	case ProviderHCaptcha:
		return providers.NewHCaptchaProvider(cfg.SiteKey, cfg.SecretKey), nil
	case ProviderLocal:
		return providers.NewLocalProvider(), nil
	case ProviderNone:
		return nil, nil //nolint:nilnil // no provider when disabled
	default:
		return nil, fmt.Errorf("unknown captcha provider: %s", cfg.Provider)
	}
}

// loadConfigFromSettings 从系统设置加载配置.
// 数据库中已保存的 config.captcha.* 优先；环境变量只在对应键还没保存时作为旧部署的回退。
func loadConfigFromSettings() *Config {
	providerStr := ""
	if value, ok := systemsettings.Stored(systemsettings.KeyCaptchaProvider); ok && value != nil {
		providerStr = strings.TrimSpace(fmt.Sprint(value))
	} else {
		providerStr = strings.TrimSpace(os.Getenv("CAPTCHA_PROVIDER"))
	}

	// 如果没有配置新格式，尝试兼容旧格式
	if providerStr == "" {
		if systemsettings.GetBool(systemsettings.KeyRecaptchaEnabled, false) {
			providerStr = "recaptcha" //nolint:goconst
		}
	}

	provider, err := ParseProviderType(providerStr)
	if err != nil {
		log.Warn("Unknown captcha provider '%s', using none", providerStr)

		provider = ProviderNone
	}

	cfg := &Config{
		Provider: provider,
		Enabled:  systemsettings.GetBool("config.captcha.enabled", false),
	}

	// 根据供应商加载特定配置
	siteEnv, secretEnv := "CAPTCHA_SITE_KEY", "CAPTCHA_SECRET_KEY"
	legacySite, legacySecret := systemsettings.KeyRecaptchaSiteKey, systemsettings.KeyRecaptchaSecretKey
	switch provider {
	case ProviderRecaptcha:
		cfg.ScoreThreshold = systemsettings.GetFloat64("config.captcha.score_threshold", 0.5)
	case ProviderCloudflare:
		siteEnv, secretEnv = "CLOUDFLARE_TURNSTILE_SITE_KEY", "CLOUDFLARE_TURNSTILE_SECRET_KEY"
		legacySite, legacySecret = "config.cloudflare.turnstile.site_key", "config.cloudflare.turnstile.secret_key"
	case ProviderHCaptcha:
		siteEnv, secretEnv = "HCAPTCHA_SITE_KEY", "HCAPTCHA_SECRET_KEY"
		legacySite, legacySecret = "config.hcaptcha.site_key", "config.hcaptcha.secret_key"
	}
	cfg.SiteKey = captchaCredential(systemsettings.KeyCaptchaSiteKey, siteEnv, legacySite)
	cfg.SecretKey = captchaCredential(systemsettings.KeyCaptchaSecretKey, secretEnv, legacySecret)

	// Legacy installations may auto-enable from credentials, but an explicit
	// administrator disable must remain disabled.
	if systemsettings.Get(systemsettings.KeyCaptchaEnabled, nil) == nil && cfg.SiteKey != "" && cfg.SecretKey != "" {
		cfg.Enabled = true
	}

	return cfg
}

// captchaCredential reads the admin key when it has been saved (including an
// explicit empty value). Otherwise it uses the legacy environment variable,
// then an older provider-specific settings key.
func captchaCredential(settingsKey, envKey, legacyKey string) string {
	if value, ok := systemsettings.Stored(settingsKey); ok && value != nil {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return value
	}
	if envKey != "CAPTCHA_SITE_KEY" && envKey != "CAPTCHA_SECRET_KEY" {
		generic := "CAPTCHA_SITE_KEY"
		if strings.Contains(envKey, "SECRET") {
			generic = "CAPTCHA_SECRET_KEY"
		}
		if value := strings.TrimSpace(os.Getenv(generic)); value != "" {
			return value
		}
	}
	if legacyKey != "" && legacyKey != settingsKey {
		if value, ok := systemsettings.Stored(legacyKey); ok && value != nil {
			return strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return ""
}

// SiteVerify 便捷的验证函数（使用全局服务）.
func SiteVerify(response string, remoteIP string) (bool, error) {
	result, err := GetService().Verify(context.Background(), response, remoteIP)
	if err != nil {
		return false, err
	}

	return result.Success, nil
}

// IsCaptchaEnabled 便捷的启用状态检查.
func IsCaptchaEnabled() bool {
	return GetService().IsEnabled()
}

// GetCurrentSiteKey 便捷的获取站点密钥.
func GetCurrentSiteKey() string {
	return GetService().GetSiteKey()
}

// GetCurrentProvider 便捷的获取供应商类型.
func GetCurrentProvider() string {
	return GetService().GetProviderType().String()
}
