package providers

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/kasuganosora/community/captcha/types"
	"github.com/kasuganosora/community/httpclient"
)

const (
	hcaptchaVerifyURL = "https://hcaptcha.com/siteverify"
)

// HCaptchaProvider hCaptcha 实现.
type HCaptchaProvider struct {
	siteKey   string
	secretKey string
	client    *httpclient.Client
}

// hcaptchaResponse hCaptcha 响应结构.
type hcaptchaResponse struct {
	Success     bool     `json:"success"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
	Credit      bool     `json:"credit"`
	ErrorCodes  []string `json:"error-codes"`
	Score       float64  `json:"score"`
	ScoreReason string   `json:"score_reason"`
}

// NewHCaptchaProvider 创建 hCaptcha 供应商.
func NewHCaptchaProvider(siteKey, secretKey string) *HCaptchaProvider {
	return &HCaptchaProvider{
		siteKey:   siteKey,
		secretKey: secretKey,
		client:    httpclient.NewClient(),
	}
}

// Name 返回供应商名称.
func (h *HCaptchaProvider) Name() string {
	return "hcaptcha"
}

// Verify 验证用户响应.
func (h *HCaptchaProvider) Verify(ctx context.Context, response string, remoteIP string) (*types.VerificationResult, error) {
	if h.secretKey == "" {
		return nil, errors.New("hcaptcha secret key is not configured")
	}

	if response == "" {
		return &types.VerificationResult{
			Success:    false,
			Provider:   h.Name(),
			ErrorCodes: []string{"missing-input-response"}, //nolint:goconst
		}, nil
	}

	// 构建请求参数
	data := url.Values{}
	data.Set("secret", h.secretKey)
	data.Set("response", response)

	if remoteIP != "" {
		data.Set("remoteip", remoteIP)
	}
	// hCaptcha 支持 sitekey 参数用于额外验证
	if h.siteKey != "" {
		data.Set("sitekey", h.siteKey)
	}

	// 发送验证请求
	req := h.client.NewRequest("POST", hcaptchaVerifyURL)
	req.SetFormUrlencoded(data)
	req.SetTimeout(10)

	resp, err := req.Do() //nolint:contextcheck // custom httpclient type, context handled internally
	if err != nil {
		return nil, fmt.Errorf("hcaptcha verify request failed: %w", err)
	}

	var result hcaptchaResponse
	if err := resp.Json(&result); err != nil {
		return nil, fmt.Errorf("failed to parse hcaptcha response: %w", err)
	}

	return &types.VerificationResult{
		Success:     result.Success,
		Score:       result.Score,
		ChallengeTS: result.ChallengeTS,
		Hostname:    result.Hostname,
		ErrorCodes:  result.ErrorCodes,
		Provider:    h.Name(),
	}, nil
}

// GetSiteKey 获取站点密钥.
func (h *HCaptchaProvider) GetSiteKey() string {
	return h.siteKey
}

// IsConfigured 检查是否已配置.
func (h *HCaptchaProvider) IsConfigured() bool {
	return h.siteKey != "" && h.secretKey != ""
}
