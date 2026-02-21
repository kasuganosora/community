package providers

import (
	"context"
	"fmt"
	"net/url"

	"github.com/kasuganosora/community/captcha/types"
	"github.com/kasuganosora/community/httpclient"
)

const (
	cloudflareVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
)

// CloudflareProvider Cloudflare Turnstile 实现
type CloudflareProvider struct {
	siteKey   string
	secretKey string
	client    *httpclient.Client
}

// cloudflareResponse Cloudflare Turnstile 响应结构
type cloudflareResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
	ChallengeTS string  `json:"challenge_ts"`
	Hostname   string   `json:"hostname"`
	Action     string   `json:"action"`
	CData      string   `json:"cdata"`
	Metadata   struct {
		EphemeralID string `json:"ephemeral_id"`
	} `json:"metadata"`
}

// NewCloudflareProvider 创建 Cloudflare Turnstile 供应商
func NewCloudflareProvider(siteKey, secretKey string) *CloudflareProvider {
	return &CloudflareProvider{
		siteKey:   siteKey,
		secretKey: secretKey,
		client:    httpclient.NewClient(),
	}
}

// Name 返回供应商名称
func (c *CloudflareProvider) Name() string {
	return "cloudflare"
}

// Verify 验证用户响应
func (c *CloudflareProvider) Verify(ctx context.Context, response string, remoteIP string) (*types.VerificationResult, error) {
	if c.secretKey == "" {
		return nil, fmt.Errorf("cloudflare turnstile secret key is not configured")
	}

	if response == "" {
		return &types.VerificationResult{
			Success:    false,
			Provider:   c.Name(),
			ErrorCodes: []string{"missing-input-response"},
		}, nil
	}

	// 构建请求参数
	data := url.Values{}
	data.Set("secret", c.secretKey)
	data.Set("response", response)
	if remoteIP != "" {
		data.Set("remoteip", remoteIP)
	}

	// 发送验证请求
	req := c.client.NewRequest("POST", cloudflareVerifyURL)
	req.SetFormUrlencoded(data)
	req.SetTimeout(10)

	resp, err := req.Do()
	if err != nil {
		return nil, fmt.Errorf("cloudflare turnstile verify request failed: %w", err)
	}

	var result cloudflareResponse
	if err := resp.Json(&result); err != nil {
		return nil, fmt.Errorf("failed to parse cloudflare response: %w", err)
	}

	return &types.VerificationResult{
		Success:     result.Success,
		Score:       0.0, // Cloudflare Turnstile 不提供分数
		Action:      result.Action,
		ChallengeTS: result.ChallengeTS,
		Hostname:    result.Hostname,
		ErrorCodes:  result.ErrorCodes,
		Provider:    c.Name(),
	}, nil
}

// GetSiteKey 获取站点密钥
func (c *CloudflareProvider) GetSiteKey() string {
	return c.siteKey
}

// IsConfigured 检查是否已配置
func (c *CloudflareProvider) IsConfigured() bool {
	return c.siteKey != "" && c.secretKey != ""
}

// GetVerifyURL 获取验证 URL（用于前端）
func (c *CloudflareProvider) GetVerifyURL() string {
	return cloudflareVerifyURL
}
