package providers

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/kasuganosora/community/captcha/types"
	"github.com/kasuganosora/community/httpclient"
)

const (
	recaptchaVerifyURL = "https://www.google.com/recaptcha/api/siteverify"
)

// RecaptchaProvider Google reCAPTCHA 实现
type RecaptchaProvider struct {
	siteKey   string
	secretKey string
	client    *httpclient.Client
}

// recaptchaResponse Google reCAPTCHA 响应结构
type recaptchaResponse struct {
	Success     bool      `json:"success"`
	Score       float64   `json:"score"`
	Action      string    `json:"action"`
	ChallengeTS time.Time `json:"challenge_ts"`
	Hostname    string    `json:"hostname"`
	ErrorCodes  []string  `json:"error-codes"`
}

// NewRecaptchaProvider 创建 reCAPTCHA 供应商
func NewRecaptchaProvider(siteKey, secretKey string) *RecaptchaProvider {
	return &RecaptchaProvider{
		siteKey:   siteKey,
		secretKey: secretKey,
		client:    httpclient.NewClient(),
	}
}

// Name 返回供应商名称
func (r *RecaptchaProvider) Name() string {
	return "recaptcha"
}

// Verify 验证用户响应
func (r *RecaptchaProvider) Verify(ctx context.Context, response string, remoteIP string) (*types.VerificationResult, error) {
	if r.secretKey == "" {
		return nil, fmt.Errorf("recaptcha secret key is not configured")
	}

	if response == "" {
		return &types.VerificationResult{
			Success:    false,
			Provider:   r.Name(),
			ErrorCodes: []string{"missing-input-response"},
		}, nil
	}

	// 构建请求参数
	data := url.Values{}
	data.Set("secret", r.secretKey)
	data.Set("response", response)
	if remoteIP != "" {
		data.Set("remoteip", remoteIP)
	}

	// 发送验证请求
	req := r.client.NewRequest("POST", recaptchaVerifyURL)
	req.SetFormUrlencoded(data)
	req.SetTimeout(10)

	resp, err := req.Do()
	if err != nil {
		return nil, fmt.Errorf("recaptcha verify request failed: %w", err)
	}

	var result recaptchaResponse
	if err := resp.Json(&result); err != nil {
		return nil, fmt.Errorf("failed to parse recaptcha response: %w", err)
	}

	return &types.VerificationResult{
		Success:     result.Success,
		Score:       result.Score,
		Action:      result.Action,
		ChallengeTS: result.ChallengeTS.Format(time.RFC3339),
		Hostname:    result.Hostname,
		ErrorCodes:  result.ErrorCodes,
		Provider:    r.Name(),
	}, nil
}

// GetSiteKey 获取站点密钥
func (r *RecaptchaProvider) GetSiteKey() string {
	return r.siteKey
}

// IsConfigured 检查是否已配置
func (r *RecaptchaProvider) IsConfigured() bool {
	return r.siteKey != "" && r.secretKey != ""
}
