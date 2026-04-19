package translation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ─────────────────────────────────────────────────────────────
// MockProvider
// ─────────────────────────────────────────────────────────────

func TestMockProvider_Translate(t *testing.T) {
	m := &MockProvider{}

	result, err := m.Translate("hello world", "en", "zh")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "hello world", result.Text)
	assert.Equal(t, "en", result.DetectedSource)
	assert.Equal(t, "mock", result.Provider)
}

func TestMockProvider_DetectLanguage(t *testing.T) {
	m := &MockProvider{}

	lang, err := m.DetectLanguage("any text")

	assert.NoError(t, err)
	assert.Equal(t, "en", lang)
}

func TestMockProvider_DetectLanguage_EmptyText(t *testing.T) {
	m := &MockProvider{}

	lang, err := m.DetectLanguage("")

	assert.NoError(t, err)
	assert.Equal(t, "en", lang)
}

func TestMockProvider_Name(t *testing.T) {
	m := &MockProvider{}

	assert.Equal(t, "mock", m.Name())
}

// ─────────────────────────────────────────────────────────────
// Service – nil provider
// ─────────────────────────────────────────────────────────────

func TestService_Translate_NilProvider(t *testing.T) {
	s := &Service{}

	result, err := s.Translate("hello", "en", "zh")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "no translation provider configured", err.Error())
}

func TestService_DetectLanguage_NilProvider(t *testing.T) {
	s := &Service{}

	_, err := s.DetectLanguage("hello")

	assert.Error(t, err)
	assert.Equal(t, "no translation provider configured", err.Error())
}

func TestService_GetProviderName_NilProvider(t *testing.T) {
	s := &Service{}

	assert.Equal(t, "none", s.GetProviderName())
}

func TestService_GetProviderName_WithProvider(t *testing.T) {
	s := &Service{provider: &MockProvider{}}

	assert.Equal(t, "mock", s.GetProviderName())
}

// ─────────────────────────────────────────────────────────────
// Service – delegation with MockProvider
// ─────────────────────────────────────────────────────────────

func TestService_Translate_Delegates(t *testing.T) {
	s := &Service{provider: &MockProvider{}}

	result, err := s.Translate("bonjour", "fr", "en")

	assert.NoError(t, err)
	assert.Equal(t, "bonjour", result.Text)
	assert.Equal(t, "fr", result.DetectedSource)
	assert.Equal(t, "mock", result.Provider)
}

func TestService_DetectLanguage_Delegates(t *testing.T) {
	s := &Service{provider: &MockProvider{}}

	lang, err := s.DetectLanguage("something")

	assert.NoError(t, err)
	assert.Equal(t, "en", lang)
}

// ─────────────────────────────────────────────────────────────
// NewService – provider selection via env var
// ─────────────────────────────────────────────────────────────

func TestNewService_DeepL(t *testing.T) {
	t.Setenv("TRANSLATION_PROVIDER", "deepl")
	s := NewService()

	assert.NotNil(t, s)
	assert.IsType(t, &DeepLProvider{}, s.provider)
	assert.Equal(t, "deepl", s.GetProviderName())
}

func TestNewService_Google(t *testing.T) {
	t.Setenv("TRANSLATION_PROVIDER", "google")
	s := NewService()

	assert.NotNil(t, s)
	assert.IsType(t, &GoogleProvider{}, s.provider)
	assert.Equal(t, "google", s.GetProviderName())
}

func TestNewService_LibreTranslate(t *testing.T) {
	t.Setenv("TRANSLATION_PROVIDER", "libretranslate")
	s := NewService()

	assert.NotNil(t, s)
	assert.IsType(t, &LibreTranslateProvider{}, s.provider)
	assert.Equal(t, "libretranslate", s.GetProviderName())
}

func TestNewService_UnknownProvider(t *testing.T) {
	t.Setenv("TRANSLATION_PROVIDER", "unknown_provider_xyz")
	s := NewService()

	assert.NotNil(t, s)
	assert.IsType(t, &MockProvider{}, s.provider)
	assert.Equal(t, "mock", s.GetProviderName())
}

func TestNewService_EmptyEnv_DefaultsToLibreTranslate(t *testing.T) {
	// Clear the env var so it falls through to the default.
	os.Unsetenv("TRANSLATION_PROVIDER")
	s := NewService()

	assert.NotNil(t, s)
	assert.IsType(t, &LibreTranslateProvider{}, s.provider)
	assert.Equal(t, "libretranslate", s.GetProviderName())
}

// ─────────────────────────────────────────────────────────────
// LibreTranslateProvider – construction & Name
// ─────────────────────────────────────────────────────────────

func TestNewLibreTranslateProvider_DefaultURL(t *testing.T) {
	os.Unsetenv("LIBRETRANSLATE_URL")
	p := NewLibreTranslateProvider()

	assert.Equal(t, "https://libretranslate.de", p.baseURL)
	assert.Equal(t, "libretranslate", p.Name())
	assert.NotNil(t, p.client)
}

func TestNewLibreTranslateProvider_CustomURL(t *testing.T) {
	t.Setenv("LIBRETRANSLATE_URL", "http://localhost:5000")
	p := NewLibreTranslateProvider()

	assert.Equal(t, "http://localhost:5000", p.baseURL)
}

func TestLibreTranslateProvider_Name(t *testing.T) {
	p := NewLibreTranslateProvider()
	assert.Equal(t, "libretranslate", p.Name())
}

// ─────────────────────────────────────────────────────────────
// LibreTranslateProvider – Translate
// ─────────────────────────────────────────────────────────────

func TestLibreTranslateProvider_Translate_EmptyText(t *testing.T) {
	p := NewLibreTranslateProvider()

	result, err := p.Translate("", "en", "zh")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "", result.Text)
	assert.Equal(t, "libretranslate", result.Provider)
}

func TestLibreTranslateProvider_Translate_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request shape
		assert.Equal(t, "/translate", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")

		_ = r.ParseForm()
		assert.Equal(t, "hello", r.FormValue("q"))
		assert.Equal(t, "en", r.FormValue("source"))
		assert.Equal(t, "zh", r.FormValue("target"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"translatedText": "你好",
		})
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL

	result, err := p.Translate("hello", "en", "zh")

	assert.NoError(t, err)
	assert.Equal(t, "你好", result.Text)
	assert.Equal(t, "en", result.DetectedSource)
	assert.Equal(t, "libretranslate", result.Provider)
}

func TestLibreTranslateProvider_Translate_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "internal server error")
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL

	result, err := p.Translate("hello", "en", "zh")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "internal server error")
}

func TestLibreTranslateProvider_Translate_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json-at-all"))
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL

	result, err := p.Translate("hello", "en", "zh")

	assert.Error(t, err)
	assert.Nil(t, result)
}

// ─────────────────────────────────────────────────────────────
// LibreTranslateProvider – DetectLanguage
// ─────────────────────────────────────────────────────────────

func TestLibreTranslateProvider_DetectLanguage_EmptyText(t *testing.T) {
	p := NewLibreTranslateProvider()

	lang, err := p.DetectLanguage("")

	assert.NoError(t, err)
	assert.Equal(t, "en", lang)
}

func TestLibreTranslateProvider_DetectLanguage_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/detect", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		_ = r.ParseForm()
		assert.Equal(t, "bonjour", r.FormValue("q"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"language": "fr", "confidence": 0.95},
		})
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL

	lang, err := p.DetectLanguage("bonjour")

	assert.NoError(t, err)
	assert.Equal(t, "fr", lang)
}

func TestLibreTranslateProvider_DetectLanguage_EmptyArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL

	lang, err := p.DetectLanguage("something")

	assert.NoError(t, err)
	assert.Equal(t, "en", lang) // fallback when no results
}

func TestLibreTranslateProvider_DetectLanguage_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL

	lang, err := p.DetectLanguage("hello")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "503")
	assert.Equal(t, "", lang)
}

func TestLibreTranslateProvider_DetectLanguage_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{invalid"))
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL

	lang, err := p.DetectLanguage("hello")

	assert.Error(t, err)
	assert.Equal(t, "", lang)
}

// ─────────────────────────────────────────────────────────────
// LibreTranslateProvider – with API key
// ─────────────────────────────────────────────────────────────

func TestLibreTranslateProvider_Translate_WithAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		assert.Equal(t, "secret-key-123", r.FormValue("api_key"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"translatedText": "翻译结果",
		})
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL
	p.apiKey = "secret-key-123"

	result, err := p.Translate("test", "en", "zh")

	assert.NoError(t, err)
	assert.Equal(t, "翻译结果", result.Text)
}

func TestLibreTranslateProvider_DetectLanguage_WithAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		assert.Equal(t, "secret-key-456", r.FormValue("api_key"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"language": "de", "confidence": 0.9},
		})
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL
	p.apiKey = "secret-key-456"

	lang, err := p.DetectLanguage("Guten Tag")

	assert.NoError(t, err)
	assert.Equal(t, "de", lang)
}

// ─────────────────────────────────────────────────────────────
// DeepLProvider
// ─────────────────────────────────────────────────────────────

func TestDeepLProvider_Name(t *testing.T) {
	p := NewDeepLProvider()
	assert.Equal(t, "deepl", p.Name())
}

func TestDeepLProvider_Translate_EmptyText(t *testing.T) {
	p := NewDeepLProvider()

	result, err := p.Translate("", "en", "zh")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "", result.Text)
	assert.Equal(t, "deepl", result.Provider)
}

func TestDeepLProvider_Translate_NoAPIKey_FallsBackToMock(t *testing.T) {
	os.Unsetenv("DEEPL_API_KEY")
	p := NewDeepLProvider()
	assert.Empty(t, p.apiKey)

	result, err := p.Translate("hello", "en", "zh")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Falls back to MockProvider → returns text as-is
	assert.Equal(t, "hello", result.Text)
	assert.Equal(t, "mock", result.Provider)
}

func TestDeepLProvider_Translate_WithAPIKey_FallsBackToMock(t *testing.T) {
	t.Setenv("DEEPL_API_KEY", "test-key")
	p := NewDeepLProvider()
	assert.Equal(t, "test-key", p.apiKey)

	result, err := p.Translate("hello", "en", "zh")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Not fully implemented → still falls back to MockProvider
	assert.Equal(t, "hello", result.Text)
	assert.Equal(t, "mock", result.Provider)
}

func TestDeepLProvider_DetectLanguage(t *testing.T) {
	p := NewDeepLProvider()

	lang, err := p.DetectLanguage("anything")

	assert.NoError(t, err)
	assert.Equal(t, "en", lang)
}

func TestDeepLProvider_DetectLanguage_EmptyText(t *testing.T) {
	p := NewDeepLProvider()

	lang, err := p.DetectLanguage("")

	assert.NoError(t, err)
	assert.Equal(t, "en", lang)
}

// ─────────────────────────────────────────────────────────────
// GoogleProvider
// ─────────────────────────────────────────────────────────────

func TestGoogleProvider_Name(t *testing.T) {
	p := NewGoogleProvider()
	assert.Equal(t, "google", p.Name())
}

func TestGoogleProvider_Translate_EmptyText(t *testing.T) {
	p := NewGoogleProvider()

	result, err := p.Translate("", "en", "zh")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "", result.Text)
	assert.Equal(t, "google", result.Provider)
}

func TestGoogleProvider_Translate_NoAPIKey_FallsBackToMock(t *testing.T) {
	os.Unsetenv("GOOGLE_TRANSLATE_API_KEY")
	p := NewGoogleProvider()
	assert.Empty(t, p.apiKey)

	result, err := p.Translate("hello", "en", "zh")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Falls back to MockProvider → returns text as-is
	assert.Equal(t, "hello", result.Text)
	assert.Equal(t, "mock", result.Provider)
}

func TestGoogleProvider_Translate_WithAPIKey_FallsBackToMock(t *testing.T) {
	t.Setenv("GOOGLE_TRANSLATE_API_KEY", "test-google-key")
	p := NewGoogleProvider()
	assert.Equal(t, "test-google-key", p.apiKey)

	result, err := p.Translate("hello", "en", "zh")

	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Not fully implemented → still falls back to MockProvider
	assert.Equal(t, "hello", result.Text)
	assert.Equal(t, "mock", result.Provider)
}

func TestGoogleProvider_DetectLanguage(t *testing.T) {
	p := NewGoogleProvider()

	lang, err := p.DetectLanguage("anything")

	assert.NoError(t, err)
	assert.Equal(t, "en", lang)
}

func TestGoogleProvider_DetectLanguage_EmptyText(t *testing.T) {
	p := NewGoogleProvider()

	lang, err := p.DetectLanguage("")

	assert.NoError(t, err)
	assert.Equal(t, "en", lang)
}

// ─────────────────────────────────────────────────────────────
// GetService – global singleton
// ─────────────────────────────────────────────────────────────

func TestGetService_NotNil(t *testing.T) {
	s := GetService()

	assert.NotNil(t, s, "GetService should return a non-nil global singleton")
}

func TestGetService_ReturnsSameInstance(t *testing.T) {
	s1 := GetService()
	s2 := GetService()

	assert.Same(t, s1, s2, "GetService should return the same instance each time")
}

// ─────────────────────────────────────────────────────────────
// LibreTranslateProvider – request details
// ─────────────────────────────────────────────────────────────

func TestLibreTranslateProvider_Translate_SetsCorrectHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"translatedText": "ok"})
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL

	result, err := p.Translate("test", "en", "de")

	assert.NoError(t, err)
	assert.Equal(t, "ok", result.Text)
}

func TestLibreTranslateProvider_Translate_SendsFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		assert.Equal(t, "html", r.FormValue("format"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"translatedText": "done"})
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL

	result, err := p.Translate("test", "en", "de")

	assert.NoError(t, err)
	assert.Equal(t, "done", result.Text)
}

func TestLibreTranslateProvider_DetectLanguage_SetsCorrectHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"language": "es", "confidence": 0.88},
		})
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL

	lang, err := p.DetectLanguage("hola")

	assert.NoError(t, err)
	assert.Equal(t, "es", lang)
}

// ─────────────────────────────────────────────────────────────
// Edge cases – Service created via constructor
// ─────────────────────────────────────────────────────────────

func TestService_Translate_EmptyText(t *testing.T) {
	s := &Service{provider: &MockProvider{}}

	result, err := s.Translate("", "en", "zh")

	assert.NoError(t, err)
	assert.Equal(t, "", result.Text)
}

func TestService_DetectLanguage_EmptyText(t *testing.T) {
	s := &Service{provider: &MockProvider{}}

	lang, err := s.DetectLanguage("")

	assert.NoError(t, err)
	assert.Equal(t, "en", lang)
}

// ─────────────────────────────────────────────────────────────
// MockProvider satisfies Provider interface at compile time
// ─────────────────────────────────────────────────────────────

func TestMockProvider_ImplementsProvider(t *testing.T) {
	var _ Provider = &MockProvider{}
}

func TestLibreTranslateProvider_ImplementsProvider(t *testing.T) {
	var _ Provider = &LibreTranslateProvider{}
}

func TestDeepLProvider_ImplementsProvider(t *testing.T) {
	var _ Provider = &DeepLProvider{}
}

func TestGoogleProvider_ImplementsProvider(t *testing.T) {
	var _ Provider = &GoogleProvider{}
}

// ─────────────────────────────────────────────────────────────
// Service.Translate / DetectLanguage error messages are consistent
// ─────────────────────────────────────────────────────────────

func TestService_NilProvider_ErrorMessagesConsistent(t *testing.T) {
	s := &Service{}

	_, translateErr := s.Translate("x", "en", "zh")
	_, detectErr := s.DetectLanguage("x")

	assert.Equal(t, translateErr.Error(), detectErr.Error(),
		"both methods should return the same error message when provider is nil")
}

// ─────────────────────────────────────────────────────────────
// LibreTranslate – verifies body on HTTP error
// ─────────────────────────────────────────────────────────────

func TestLibreTranslateProvider_Translate_HTTPError_IncludesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error": "bad input"}`)
	}))
	defer srv.Close()

	p := NewLibreTranslateProvider()
	p.baseURL = srv.URL

	_, err := p.Translate("test", "en", "zh")

	assert.Error(t, err)
	errMsg := err.Error()
	assert.True(t, strings.Contains(errMsg, "400"), "should include status code: %s", errMsg)
	assert.True(t, strings.Contains(errMsg, `{"error": "bad input"}`), "should include response body: %s", errMsg)
}
