package translation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/kasuganosora/community/log"
)

// Provider 翻译服务提供商接口
type Provider interface {
	Translate(text, sourceLang, targetLang string) (*Result, error)
	DetectLanguage(text string) (string, error)
	Name() string
}

// Result 翻译结果
type Result struct {
	Text           string
	DetectedSource string
	Provider       string
}

// Service 翻译服务
type Service struct {
	provider Provider
}

// NewService 创建翻译服务
func NewService() *Service {
	// 根据环境变量选择提供商
	providerType := os.Getenv("TRANSLATION_PROVIDER")
	if providerType == "" {
		providerType = "libretranslate" // 默认使用开源方案
	}

	service := &Service{}

	switch providerType {
	case "deepl":
		service.provider = NewDeepLProvider()
	case "google":
		service.provider = NewGoogleProvider()
	case "libretranslate":
		service.provider = NewLibreTranslateProvider()
	default:
		// 使用模拟翻译器作为回退
		service.provider = &MockProvider{}
	}

	return service
}

// Translate 翻译文本
func (s *Service) Translate(text, sourceLang, targetLang string) (*Result, error) {
	if s.provider == nil {
		return nil, fmt.Errorf("no translation provider configured")
	}
	return s.provider.Translate(text, sourceLang, targetLang)
}

// DetectLanguage 检测语言
func (s *Service) DetectLanguage(text string) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("no translation provider configured")
	}
	return s.provider.DetectLanguage(text)
}

// GetProviderName 获取当前提供商名称
func (s *Service) GetProviderName() string {
	if s.provider == nil {
		return "none"
	}
	return s.provider.Name()
}

// MockProvider 模拟翻译提供商（用于测试或作为回退）
type MockProvider struct{}

func (m *MockProvider) Translate(text, sourceLang, targetLang string) (*Result, error) {
	return &Result{
		Text:           text,
		DetectedSource: sourceLang,
		Provider:       "mock",
	}, nil
}

func (m *MockProvider) DetectLanguage(text string) (string, error) {
	return "en", nil
}

func (m *MockProvider) Name() string {
	return "mock"
}

// LibreTranslateProvider LibreTranslate 翻译提供商
type LibreTranslateProvider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewLibreTranslateProvider 创建 LibreTranslate 提供商
func NewLibreTranslateProvider() *LibreTranslateProvider {
	baseURL := os.Getenv("LIBRETRANSLATE_URL")
	if baseURL == "" {
		baseURL = "https://libretranslate.de" // 默认公共实例
	}

	return &LibreTranslateProvider{
		baseURL: baseURL,
		apiKey:  os.Getenv("LIBRETRANSLATE_API_KEY"),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (l *LibreTranslateProvider) Name() string {
	return "libretranslate"
}

func (l *LibreTranslateProvider) Translate(text, sourceLang, targetLang string) (*Result, error) {
	if text == "" {
		return &Result{Text: "", Provider: l.Name()}, nil
	}

	// 构建请求
	data := url.Values{}
	data.Set("q", text)
	data.Set("source", sourceLang)
	data.Set("target", targetLang)
	data.Set("format", "html")
	if l.apiKey != "" {
		data.Set("api_key", l.apiKey)
	}

	req, err := http.NewRequest("POST", l.baseURL+"/translate", bytes.NewBufferString(data.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("translation API error: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		TranslatedText string `json:"translatedText"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &Result{
		Text:           result.TranslatedText,
		DetectedSource: sourceLang,
		Provider:       l.Name(),
	}, nil
}

func (l *LibreTranslateProvider) DetectLanguage(text string) (string, error) {
	if text == "" {
		return "en", nil
	}

	data := url.Values{}
	data.Set("q", text)
	if l.apiKey != "" {
		data.Set("api_key", l.apiKey)
	}

	req, err := http.NewRequest("POST", l.baseURL+"/detect", bytes.NewBufferString(data.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := l.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("language detection API error: %d", resp.StatusCode)
	}

	var result []struct {
		Language   string  `json:"language"`
		Confidence float64 `json:"confidence"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result) > 0 {
		return result[0].Language, nil
	}

	return "en", nil
}

// DeepLProvider DeepL 翻译提供商
type DeepLProvider struct {
	apiKey string
	client *http.Client
}

// NewDeepLProvider 创建 DeepL 提供商
func NewDeepLProvider() *DeepLProvider {
	return &DeepLProvider{
		apiKey: os.Getenv("DEEPL_API_KEY"),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (d *DeepLProvider) Name() string {
	return "deepl"
}

func (d *DeepLProvider) Translate(text, sourceLang, targetLang string) (*Result, error) {
	if text == "" {
		return &Result{Text: "", Provider: d.Name()}, nil
	}

	if d.apiKey == "" {
		log.Warn("DeepL API key not configured, using mock translation")
		return (&MockProvider{}).Translate(text, sourceLang, targetLang)
	}

	// 使用 DeepL API (简化实现)
	// 实际实现需要调用 DeepL API
	log.Warn("DeepL translation not fully implemented, using mock")
	return (&MockProvider{}).Translate(text, sourceLang, targetLang)
}

func (d *DeepLProvider) DetectLanguage(text string) (string, error) {
	return "en", nil
}

// GoogleProvider Google Translate 提供商
type GoogleProvider struct {
	apiKey string
	client *http.Client
}

// NewGoogleProvider 创建 Google Translate 提供商
func NewGoogleProvider() *GoogleProvider {
	return &GoogleProvider{
		apiKey: os.Getenv("GOOGLE_TRANSLATE_API_KEY"),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (g *GoogleProvider) Name() string {
	return "google"
}

func (g *GoogleProvider) Translate(text, sourceLang, targetLang string) (*Result, error) {
	if text == "" {
		return &Result{Text: "", Provider: g.Name()}, nil
	}

	if g.apiKey == "" {
		log.Warn("Google Translate API key not configured, using mock translation")
		return (&MockProvider{}).Translate(text, sourceLang, targetLang)
	}

	// 使用 Google Translate API (简化实现)
	// 实际实现需要调用 Google Cloud Translation API
	log.Warn("Google translation not fully implemented, using mock")
	return (&MockProvider{}).Translate(text, sourceLang, targetLang)
}

func (g *GoogleProvider) DetectLanguage(text string) (string, error) {
	return "en", nil
}

// Global translation service instance
var globalService *Service

func init() {
	globalService = NewService()
}

// GetService 获取全局翻译服务实例
func GetService() *Service {
	return globalService
}

// Translate 使用全局服务翻译文本
func Translate(text, sourceLang, targetLang string) (*Result, error) {
	return globalService.Translate(text, sourceLang, targetLang)
}

// DetectLanguage 使用全局服务检测语言
func DetectLanguage(text string) (string, error) {
	return globalService.DetectLanguage(text)
}
