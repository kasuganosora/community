package captcha

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	case ProviderNone:
		return nil, nil //nolint:nilnil // no provider when disabled
	default:
		return nil, fmt.Errorf("unknown captcha provider: %s", cfg.Provider)
	}
}

// loadConfigFromSettings 从系统设置加载配置.
func loadConfigFromSettings() *Config {
	// 从环境变量或系统设置中读取配置
	providerStr := os.Getenv("CAPTCHA_PROVIDER")
	if providerStr == "" {
		providerStr = systemsettings.GetString("config.captcha.provider", "")
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
	switch provider {
	case ProviderRecaptcha:
		cfg.SiteKey = getConfigValue("CAPTCHA_SITE_KEY", systemsettings.KeyRecaptchaSiteKey, "")
		cfg.SecretKey = getConfigValue("CAPTCHA_SECRET_KEY", systemsettings.KeyRecaptchaSecretKey, "")
		cfg.ScoreThreshold = systemsettings.GetFloat64("config.captcha.score_threshold", 0.5)
	case ProviderCloudflare:
		cfg.SiteKey = getConfigValue("CLOUDFLARE_TURNSTILE_SITE_KEY", "config.cloudflare.turnstile.site_key", "")
		cfg.SecretKey = getConfigValue("CLOUDFLARE_TURNSTILE_SECRET_KEY", "config.cloudflare.turnstile.secret_key", "")
	case ProviderHCaptcha:
		cfg.SiteKey = getConfigValue("HCAPTCHA_SITE_KEY", "config.hcaptcha.site_key", "")
		cfg.SecretKey = getConfigValue("HCAPTCHA_SECRET_KEY", "config.hcaptcha.secret_key", "")
	}

	// 如果没有启用标记但有密钥配置，自动启用
	if !cfg.Enabled && cfg.SiteKey != "" && cfg.SecretKey != "" {
		cfg.Enabled = true
	}

	return cfg
}

// getConfigValue 获取配置值（优先环境变量）.
func getConfigValue(envKey, settingsKey, defaultValue string) string {
	if value := os.Getenv(envKey); value != "" {
		return value
	}

	return systemsettings.GetString(settingsKey, defaultValue)
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
