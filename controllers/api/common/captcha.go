package common

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/captcha"
	"github.com/kasuganosora/community/systemsettings"
)

// CaptchaConfigResponse 验证码配置响应.
type CaptchaConfigResponse struct {
	// Enabled 是否启用验证码
	Enabled bool `json:"enabled"`

	// Provider 验证码供应商
	Provider string `json:"provider"`

	// SiteKey 站点密钥（前端使用）
	SiteKey string `json:"site_key"`

	// PublicKey 公钥（某些供应商使用）
	PublicKey string `json:"public_key,omitempty"`
}

// GetCaptchaConfig 获取验证码配置
// GET /api/v1/captcha/config.
func GetCaptchaConfig(c *gin.Context) {
	service := captcha.GetService()

	resp := CaptchaConfigResponse{
		Enabled:  service.IsEnabled(),
		Provider: service.GetProviderType().String(),
		SiteKey:  service.GetSiteKey(),
	}

	c.JSON(http.StatusOK, resp)
}

// VerifyCaptchaRequest 验证码验证请求.
type VerifyCaptchaRequest struct {
	Response string `binding:"required" json:"response"`
}

// VerifyCaptchaResponse 验证码验证响应.
type VerifyCaptchaResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// VerifyCaptcha 验证验证码（测试用）
// POST /api/v1/captcha/verify.
func VerifyCaptcha(c *gin.Context) {
	var req VerifyCaptchaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, VerifyCaptchaResponse{
			Success: false,
			Message: "invalid request",
		})

		return
	}

	service := captcha.GetService()
	if !service.IsEnabled() {
		c.JSON(http.StatusOK, VerifyCaptchaResponse{
			Success: true,
			Message: "captcha is disabled",
		})

		return
	}

	result, err := service.Verify(c.Request.Context(), req.Response, c.ClientIP())
	if err != nil {
		c.JSON(http.StatusOK, VerifyCaptchaResponse{
			Success: false,
			Message: err.Error(),
		})

		return
	}

	if !result.Success {
		c.JSON(http.StatusOK, VerifyCaptchaResponse{
			Success: false,
			Message: "verification failed",
		})

		return
	}

	c.JSON(http.StatusOK, VerifyCaptchaResponse{
		Success: true,
		Message: "verification passed",
	})
}

// GetCaptchaProviders 获取支持的验证码供应商列表
// GET /api/v1/captcha/providers.
func GetCaptchaProviders(c *gin.Context) {
	providers := []map[string]string{
		{
			"id":          "recaptcha",                        //nolint:goconst
			"name":        "Google reCAPTCHA",                 //nolint:goconst
			"description": "Google reCAPTCHA v2/v3",           //nolint:goconst
			"website":     "https://www.google.com/recaptcha", //nolint:goconst
		},
		{
			"id":          "cloudflare", //nolint:goconst
			"name":        "Cloudflare Turnstile",
			"description": "Cloudflare Turnstile - Privacy-preserving CAPTCHA alternative",
			"website":     "https://www.cloudflare.com/products/turnstile/",
		},
		{
			"id":          "hcaptcha",
			"name":        "hCaptcha",
			"description": "hCaptcha - Privacy-focused CAPTCHA",
			"website":     "https://www.hcaptcha.com/",
		},
		{
			"id":          "none", //nolint:goconst
			"name":        "Disabled",
			"description": "No CAPTCHA verification",
			"website":     "",
		},
	}

	c.JSON(http.StatusOK, providers)
}

// AdminUpdateCaptchaConfigRequest 管理员更新验证码配置请求.
type AdminUpdateCaptchaConfigRequest struct {
	Provider       string   `json:"provider"`                  // 供应商: recaptcha, cloudflare, hcaptcha, none
	SiteKey        string   `json:"site_key"`                  // 站点密钥
	SecretKey      string   `json:"secret_key"`                // 密钥（只写）
	Enabled        *bool    `json:"enabled,omitempty"`         // 是否启用
	ScoreThreshold *float64 `json:"score_threshold,omitempty"` // 分数阈值
}

// AdminUpdateCaptchaConfig 管理员更新验证码配置
// PUT /api/v1/admin/captcha/config.
func AdminUpdateCaptchaConfig(c *gin.Context) {
	var req AdminUpdateCaptchaConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	// 验证供应商类型
	provider, err := captcha.ParseProviderType(req.Provider)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid provider"})

		return
	}

	// 更新配置
	systemsettings.Set(systemsettings.KeyCaptchaProvider, string(provider))
	systemsettings.Set(systemsettings.KeyCaptchaSiteKey, req.SiteKey)

	if req.SecretKey != "" {
		systemsettings.Set(systemsettings.KeyCaptchaSecretKey, req.SecretKey)
	}

	if req.Enabled != nil {
		systemsettings.Set(systemsettings.KeyCaptchaEnabled, *req.Enabled)
	}

	if req.ScoreThreshold != nil {
		systemsettings.Set(systemsettings.KeyCaptchaScoreThreshold, *req.ScoreThreshold)
	}

	// 保存到数据库
	keys := []string{
		systemsettings.KeyCaptchaProvider,
		systemsettings.KeyCaptchaSiteKey,
		systemsettings.KeyCaptchaSecretKey,
		systemsettings.KeyCaptchaEnabled,
		systemsettings.KeyCaptchaScoreThreshold,
	}

	if err := systemsettings.SaveToDB(keys...); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config"})

		return
	}

	// 重新加载验证码服务
	if err := captcha.GetService().Reload(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reload captcha service"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "captcha config updated"})
}
