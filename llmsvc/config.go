// Package llmsvc manages the instance's LLM providers (stored in the
// database-backed system settings, API keys encrypted at rest), builds
// clients on top of ThinkBot's llm library and records usage/cost.
package llmsvc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"github.com/kasuganosora/thinkbot/llm"
	"github.com/kasuganosora/thinkbot/llm/anthropic"
	"github.com/kasuganosora/thinkbot/llm/google"
	"github.com/kasuganosora/thinkbot/llm/grok"
	"github.com/kasuganosora/thinkbot/llm/openai"
)

// Setting keys. All values live in system_settings; nothing is read from .env.
const (
	KeyConfig = "config.llm.config" // JSON document, see Config

	ServiceTranslation = "translation"
)

// Provider types accepted by the admin API.
const (
	TypeOpenAI           = "openai"            // OpenAI Responses API
	TypeOpenAICompatible = "openai_compatible" // Chat Completions (DeepSeek, Moonshot, Ollama, ...)
	TypeAnthropic        = "anthropic"
	TypeGoogle           = "google"
	TypeGrok             = "grok"
)

// ProviderTypes lists the supported provider types in display order.
var ProviderTypes = []string{TypeOpenAI, TypeOpenAICompatible, TypeAnthropic, TypeGoogle, TypeGrok}

// Services lists the features that can use an LLM.
var Services = []string{ServiceTranslation}

// ProviderConfig is one configured LLM endpoint as stored in the database.
// APIKeyEnc holds the AES-encrypted key; the plaintext never leaves this package
// except towards the provider itself.
type ProviderConfig struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url,omitempty"`
	// ChatPath overrides the Chat Completions path of openai_compatible
	// providers: a path relative to BaseURL or a full endpoint URL. Empty =
	// automatic (see ResolveChatPath).
	ChatPath    string   `json:"chat_path,omitempty"`
	Model       string   `json:"model"`
	APIKeyEnc   string   `json:"api_key_enc,omitempty"`
	APIKeySalt  string   `json:"api_key_salt,omitempty"`
	APIKeyHint  string   `json:"api_key_hint,omitempty"` // last 4 characters, for display only
	TimeoutSec  int      `json:"timeout_seconds,omitempty"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	// Prices per 1M tokens, used for cost recording (llm.ComputeCost).
	InputPricePer1M     float64 `json:"input_price_per_1m,omitempty"`
	OutputPricePer1M    float64 `json:"output_price_per_1m,omitempty"`
	CacheReadPricePer1M float64 `json:"cache_read_price_per_1m,omitempty"`
	Currency            string  `json:"currency,omitempty"`
}

// Config is the whole stored LLM configuration.
type Config struct {
	Providers []ProviderConfig `json:"providers"`
	// Services maps a service name (e.g. "translation") to a provider ID.
	Services map[string]string `json:"services"`
}

// PublicProvider is the client-facing view: the key is reduced to a mask.
type PublicProvider struct {
	ProviderConfig
	APIKeyEnc  string `json:"api_key_enc,omitempty"`
	APIKeySalt string `json:"api_key_salt,omitempty"`
	APIKeySet  bool   `json:"api_key_set"`
	APIKeyMask string `json:"api_key_mask,omitempty"`
}

var mu sync.Mutex

// Load returns the stored configuration (never nil maps).
func Load() Config {
	var cfg Config
	switch v := systemsettings.Get(KeyConfig, "").(type) {
	case string:
		if v != "" {
			_ = json.Unmarshal([]byte(v), &cfg)
		}
	case map[string]any:
		raw, _ := json.Marshal(v)
		_ = json.Unmarshal(raw, &cfg)
	}
	if cfg.Services == nil {
		cfg.Services = map[string]string{}
	}
	if cfg.Providers == nil {
		cfg.Providers = []ProviderConfig{}
	}
	return cfg
}

// Save validates cfg and persists it together with extra settings in one
// transaction through systemsettings.SaveChanges (the admin settings path).
func Save(cfg Config, extra map[string]any) error {
	if err := Validate(cfg); err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	// Stored as a JSON object in the json column.
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	values := map[string]any{KeyConfig: doc}
	for k, v := range extra {
		values[k] = v
	}
	mu.Lock()
	defer mu.Unlock()
	if err := systemsettings.SaveChanges(values); err != nil {
		return err
	}
	invalidateClients()
	return nil
}

// Validation errors carry stable codes.
type ValidationError struct{ Code, Field string }

func (e *ValidationError) Error() string { return e.Code + ": " + e.Field }

func invalid(code, field string) error { return &ValidationError{Code: code, Field: field} }

// Validate checks a configuration before it is stored.
func Validate(cfg Config) error {
	if len(cfg.Providers) > 20 {
		return invalid("llm_too_many_providers", "providers")
	}
	seen := map[string]bool{}
	for i, p := range cfg.Providers {
		field := fmt.Sprintf("providers[%d]", i)
		if p.ID == "" || len(p.ID) > 64 || seen[p.ID] {
			return invalid("llm_invalid_provider_id", field+".id")
		}
		seen[p.ID] = true
		if strings.TrimSpace(p.Name) == "" || len([]rune(p.Name)) > 64 {
			return invalid("llm_invalid_name", field+".name")
		}
		if !contains(ProviderTypes, p.Type) {
			return invalid("llm_invalid_type", field+".type")
		}
		if strings.TrimSpace(p.Model) == "" || len(p.Model) > 128 {
			return invalid("llm_invalid_model", field+".model")
		}
		if p.BaseURL != "" {
			u, err := url.Parse(p.BaseURL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(p.BaseURL) > 2048 {
				return invalid("llm_invalid_base_url", field+".base_url")
			}
		} else if p.Type == TypeOpenAICompatible {
			return invalid("llm_base_url_required", field+".base_url")
		}
		if !validChatPath(p.ChatPath) {
			return invalid("llm_invalid_chat_path", field+".chat_path")
		}
		if p.TimeoutSec < 0 || p.TimeoutSec > 600 {
			return invalid("llm_invalid_timeout", field+".timeout_seconds")
		}
		if p.MaxTokens < 0 || p.MaxTokens > 200000 {
			return invalid("llm_invalid_max_tokens", field+".max_tokens")
		}
		if p.Temperature != nil && (*p.Temperature < 0 || *p.Temperature > 2) {
			return invalid("llm_invalid_temperature", field+".temperature")
		}
		if p.InputPricePer1M < 0 || p.OutputPricePer1M < 0 || p.CacheReadPricePer1M < 0 || len(p.Currency) > 8 {
			return invalid("llm_invalid_price", field+".price")
		}
	}
	for service, id := range cfg.Services {
		if !contains(Services, service) {
			return invalid("llm_invalid_service", "services."+service)
		}
		if id != "" && !seen[id] {
			return invalid("llm_unknown_provider", "services."+service)
		}
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// NewProviderID returns a random identifier for a new provider.
func NewProviderID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func keyFor(salt string) string {
	return utils.GetEncryptKey(config.C.EncryptedKey, "llm-provider-api-key:"+salt)
}

// SetAPIKey encrypts plaintext into p. An empty plaintext clears the key.
func (p *ProviderConfig) SetAPIKey(plaintext string) error {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		p.APIKeyEnc, p.APIKeySalt, p.APIKeyHint = "", "", ""
		return nil
	}
	if len(plaintext) > 4096 {
		return invalid("llm_invalid_api_key", "api_key")
	}
	if config.C.EncryptedKey == "" {
		return errors.New("encryption key is not configured")
	}
	p.APIKeySalt = NewProviderID() + NewProviderID()
	enc, err := utils.AesEncrypt([]byte(plaintext), keyFor(p.APIKeySalt))
	if err != nil {
		return err
	}
	p.APIKeyEnc = enc
	if r := []rune(plaintext); len(r) > 8 {
		p.APIKeyHint = string(r[len(r)-4:])
	} else {
		p.APIKeyHint = ""
	}
	return nil
}

// apiKey decrypts the stored key.
func (p ProviderConfig) apiKey() (string, error) {
	if p.APIKeyEnc == "" {
		return "", nil
	}
	plain, err := utils.AesDecrypt(p.APIKeyEnc, keyFor(p.APIKeySalt))
	if err != nil {
		return "", errors.New("stored API key cannot be decrypted")
	}
	return string(plain), nil
}

// Public returns the masked client view of p.
func (p ProviderConfig) Public() PublicProvider {
	out := PublicProvider{ProviderConfig: p, APIKeySet: p.APIKeyEnc != ""}
	out.ProviderConfig.APIKeyEnc, out.ProviderConfig.APIKeySalt, out.ProviderConfig.APIKeyHint = "", "", ""
	if out.APIKeySet {
		out.APIKeyMask = "••••••••"
		if p.APIKeyHint != "" {
			out.APIKeyMask += p.APIKeyHint
		}
	}
	return out
}

// Find returns the provider with the given id.
func (c Config) Find(id string) (ProviderConfig, bool) {
	for _, p := range c.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return ProviderConfig{}, false
}

// ServiceProvider returns the provider configured for a service.
func ServiceProvider(service string) (ProviderConfig, bool) {
	cfg := Load()
	id := cfg.Services[service]
	if id == "" {
		return ProviderConfig{}, false
	}
	return cfg.Find(id)
}

// Price returns the configured price for cost computation.
func (p ProviderConfig) Price() llm.ModelPrice {
	return llm.ModelPrice{InputPer1M: p.InputPricePer1M, OutputPer1M: p.OutputPricePer1M, CacheReadPer1M: p.CacheReadPricePer1M, Currency: p.Currency}
}

// Timeout returns the request timeout (default 60s).
func (p ProviderConfig) Timeout() time.Duration {
	if p.TimeoutSec <= 0 {
		return 60 * time.Second
	}
	return time.Duration(p.TimeoutSec) * time.Second
}

// buildProvider creates a ThinkBot llm.Provider for p.
func buildProvider(p ProviderConfig) (llm.Provider, error) {
	key, err := p.apiKey()
	if err != nil {
		return nil, err
	}
	timeout := p.Timeout()
	switch p.Type {
	case TypeOpenAI, TypeOpenAICompatible:
		opts := []openai.Option{openai.WithAPIKey(key), openai.WithTimeout(timeout)}
		if p.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(p.BaseURL))
		}
		if p.Type == TypeOpenAICompatible {
			opts = append(opts, openai.WithChatMode(), openai.WithChatPath(ResolveChatPath(p.BaseURL, p.ChatPath)))
		}
		return openai.New(opts...), nil
	case TypeAnthropic:
		opts := []anthropic.Option{anthropic.WithAPIKey(key), anthropic.WithTimeout(timeout)}
		if p.BaseURL != "" {
			opts = append(opts, anthropic.WithBaseURL(p.BaseURL))
		}
		return anthropic.New(opts...), nil
	case TypeGoogle:
		opts := []google.Option{google.WithAPIKey(key), google.WithTimeout(timeout)}
		if p.BaseURL != "" {
			opts = append(opts, google.WithBaseURL(p.BaseURL))
		}
		return google.New(opts...), nil
	case TypeGrok:
		opts := []grok.Option{grok.WithAPIKey(key), grok.WithTimeout(timeout)}
		if p.BaseURL != "" {
			opts = append(opts, grok.WithBaseURL(p.BaseURL))
		}
		return grok.New(opts...), nil
	}
	return nil, fmt.Errorf("unsupported provider type %q", p.Type)
}
