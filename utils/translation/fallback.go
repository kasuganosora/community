package translation

import (
	"net/url"
	"os"
	"strings"
)

// PublicLibreTranslateURL is the built-in public default instance. It is
// never used as a fallback for LLM translation.
const PublicLibreTranslateURL = "https://libretranslate.de"

var fallbackOverride func() (*Service, bool)

// SetFallbackServiceForTest replaces FallbackService (nil restores it).
func SetFallbackServiceForTest(f func() (*Service, bool)) { fallbackOverride = f }

// NewServiceWithProvider wraps p (tests and fallbacks).
func NewServiceWithProvider(p Provider) *Service { return &Service{provider: p} }

// FallbackService returns the traditional backend used when LLM translation
// fails. Only an explicitly configured, non-default LibreTranslate-compatible
// URL (e.g. a self-hosted gateway) qualifies; the public default instance and
// the placeholder DeepL/Google/mock providers never do.
func FallbackService() (*Service, bool) {
	if fallbackOverride != nil {
		return fallbackOverride()
	}
	kind := strings.ToLower(strings.TrimSpace(os.Getenv("TRANSLATION_PROVIDER")))
	if kind != "" && kind != "libretranslate" {
		return nil, false
	}
	raw := strings.TrimSpace(os.Getenv("LIBRETRANSLATE_URL"))
	if !ExplicitTraditionalURL(raw) {
		return nil, false
	}
	return NewServiceWithProvider(NewLibreTranslateProvider()), true
}

// ExplicitTraditionalURL reports whether raw is a usable URL that is not the
// built-in public default.
func ExplicitTraditionalURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	def, _ := url.Parse(PublicLibreTranslateURL)
	return !(strings.EqualFold(u.Hostname(), def.Hostname()) && strings.Trim(u.Path, "/") == "")
}
