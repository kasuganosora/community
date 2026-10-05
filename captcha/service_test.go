package captcha

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/captcha/types"
)

// mockProvider 是一个用于测试的模拟供应商.
type mockProvider struct {
	name       string
	siteKey    string
	secretKey  string
	verifyFunc func(ctx context.Context, response string, remoteIP string) (*types.VerificationResult, error)
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) Verify(ctx context.Context, response string, remoteIP string) (*types.VerificationResult, error) {
	if m.verifyFunc != nil {
		return m.verifyFunc(ctx, response, remoteIP)
	}

	return &types.VerificationResult{
		Success:  true,
		Provider: m.name,
	}, nil
}

func (m *mockProvider) GetSiteKey() string {
	return m.siteKey
}

func (m *mockProvider) IsConfigured() bool {
	return m.siteKey != "" && m.secretKey != ""
}

func TestNewService(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name: "none provider",
			cfg: &Config{
				Provider: ProviderNone,
				Enabled:  false,
			},
			wantErr: false,
		},
		{
			name: "recaptcha without keys",
			cfg: &Config{
				Provider: ProviderRecaptcha,
				Enabled:  true,
			},
			wantErr: false, // 创建服务不会报错，使用时才会
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewService(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewService() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if service == nil {
				t.Error("NewService() returned nil service")
			}
		})
	}
}

func TestService_Verify(t *testing.T) {
	tests := []struct {
		name         string
		enabled      bool
		response     string
		wantSuccess  bool
		wantProvider string
		wantErr      bool
	}{
		{
			name:         "disabled",
			enabled:      false,
			response:     "",
			wantSuccess:  true,
			wantProvider: "none", //nolint:goconst
		},
		{
			name:         "enabled with empty response",
			enabled:      true,
			response:     "",
			wantSuccess:  false,
			wantProvider: "mock", //nolint:goconst
			wantErr:      true,
		},
		{
			name:         "enabled with valid response",
			enabled:      true,
			response:     "valid-response",
			wantSuccess:  true,
			wantProvider: "mock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &Service{
				enabled: tt.enabled,
				provider: &mockProvider{
					name:      "mock",
					siteKey:   "mock-site-key",
					secretKey: "mock-secret-key",
				},
			}

			result, err := service.Verify(context.Background(), tt.response, "127.0.0.1")
			if (err != nil) != tt.wantErr {
				t.Errorf("Verify() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if result == nil {
				return
			}

			if result.Success != tt.wantSuccess {
				t.Errorf("Verify() Success = %v, want %v", result.Success, tt.wantSuccess)
			}

			if result.Provider != tt.wantProvider {
				t.Errorf("Verify() Provider = %v, want %v", result.Provider, tt.wantProvider)
			}
		})
	}
}

func TestParseProviderType(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ProviderType
		wantErr bool
	}{
		{"recaptcha", "recaptcha", ProviderRecaptcha, false}, //nolint:goconst
		{"google alias", "google", ProviderRecaptcha, false},
		{"cloudflare", "cloudflare", ProviderCloudflare, false}, //nolint:goconst
		{"turnstile alias", "turnstile", ProviderCloudflare, false},
		{"hcaptcha", "hcaptcha", ProviderHCaptcha, false}, //nolint:goconst
		{"local", "local", ProviderLocal, false},
		{"builtin alias", "builtin", ProviderLocal, false},
		{"none", "none", ProviderNone, false},
		{"empty", "", ProviderNone, false},
		{"unknown", "unknown", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProviderType(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseProviderType() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if got != tt.want {
				t.Errorf("ParseProviderType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestService_IsEnabled(t *testing.T) {
	service := &Service{enabled: true}
	if !service.IsEnabled() {
		t.Error("IsEnabled() should return true")
	}

	service.enabled = false
	if service.IsEnabled() {
		t.Error("IsEnabled() should return false")
	}
}

func TestProviderType_String(t *testing.T) {
	tests := []struct {
		pt   ProviderType
		want string
	}{
		{ProviderRecaptcha, "recaptcha"},
		{ProviderCloudflare, "cloudflare"},
		{ProviderHCaptcha, "hcaptcha"},
		{ProviderLocal, "local"},
		{ProviderNone, "none"},
	}

	for _, tt := range tests {
		t.Run(string(tt.pt), func(t *testing.T) {
			if got := tt.pt.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}
