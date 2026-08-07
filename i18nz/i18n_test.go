package i18nz //nolint:goconst // acceptable string literal usage

import (
	"sync"
	"testing"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/stretchr/testify/assert"
	"golang.org/x/text/language"
)

func TestGetLocalizer(t *testing.T) {
	tests := []struct {
		name   string
		lang   string
		accept string
	}{
		{"empty lang and accept", "", ""},
		{"with lang only", "en", ""},
		{"with accept only", "", "en-US,en;q=0.9"}, //nolint:goconst
		{"with both", "zh", "en-US,en;q=0.9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := GetLocalizer(tt.lang, tt.accept)
			assert.NotNil(t, loc)
		})
	}
}

func TestGetMailTemplateName(t *testing.T) {
	tests := []struct {
		name         string
		lang         string
		templateName string
	}{
		{"english template", "en", "welcome"}, //nolint:goconst
		{"chinese template", "zh", "welcome"},
		{"japanese template", "ja", "welcome"},
		{"empty lang", "", "welcome"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetMailTemplateName(tt.lang, tt.templateName)
			assert.NotEmpty(t, result)
		})
	}
}

func TestBundle(t *testing.T) {
	assert.NotNil(t, Bundle)
}

func TestOStruct(t *testing.T) {
	o := O{
		Key:   "PluralCount",
		Value: "5",
	}
	assert.Equal(t, "PluralCount", o.Key)
	assert.Equal(t, "5", o.Value)
}

func TestLocalizerStruct(t *testing.T) {
	loc := &Localizer{}
	assert.NotNil(t, loc)
}

func TestLocalizer_DifferentLanguages(t *testing.T) {
	languages := []string{"en", "zh", "ja", "ko", "es", "fr", "de"}

	for _, lang := range languages {
		t.Run("language_"+lang, func(t *testing.T) {
			loc := GetLocalizer(lang, "")
			assert.NotNil(t, loc)
		})
	}
}

func TestLocalizer_AcceptLanguageParsing(t *testing.T) {
	acceptHeaders := []string{
		"en-US,en;q=0.9",
		"zh-CN,zh;q=0.9,en;q=0.8",
		"ja,en-US;q=0.9,en;q=0.8",
		"*",
		"",
	}

	for _, accept := range acceptHeaders {
		t.Run("accept_"+accept, func(t *testing.T) {
			loc := GetLocalizer("", accept)
			assert.NotNil(t, loc)
		})
	}
}

func TestIsTestMode(t *testing.T) {
	// In test mode, this should return true
	assert.True(t, isTestMode())
}

func TestGetMailTemplateName_DefaultLang(t *testing.T) {
	result := GetMailTemplateName("", "welcome")
	assert.Contains(t, result, "welcome")
}

func TestOStruct_ZeroValues(t *testing.T) {
	o := O{}
	assert.Empty(t, o.Key)
	assert.Empty(t, o.Value)
}

// TestT_FallbackToID verifies that T() never panics on missing keys and
// returns the message ID itself as a fallback.
func TestT_FallbackToID(t *testing.T) {
	loc := GetLocalizer("en", "")
	result := loc.T("this_key_does_not_exist", map[string]any{"foo": "bar"})
	assert.Equal(t, "this_key_does_not_exist", result)
}

// TestT_FallbackToDefault verifies that T() returns the "Default" option
// value when provided and the key is missing.
func TestT_FallbackToDefault(t *testing.T) {
	loc := GetLocalizer("en", "")
	result := loc.T("another_missing_key", nil, O{Key: "Default", Value: "Fallback Subject"})
	assert.Equal(t, "Fallback Subject", result)
}

// TestGetMatcher_Cached verifies that getMatcher is thread-safe and returns
// the same instance across concurrent calls.
func TestGetMatcher_Cached(t *testing.T) {
	var wg sync.WaitGroup
	const goroutines = 10
	results := make([]language.Matcher, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = getMatcher()
		}(i)
	}
	wg.Wait()

	// All goroutines must have received the same matcher pointer.
	for i := 1; i < goroutines; i++ {
		assert.Same(t, results[0], results[i], "matcher instances differ across goroutines")
	}
}

// TestGetMailTemplateName_AcceptLanguageHeader verifies that GetMailTemplateName
// handles full Accept-Language header strings correctly.
func TestGetMailTemplateName_AcceptLanguageHeader(t *testing.T) {
	result := GetMailTemplateName("zh-CN,zh;q=0.9", "welcome")
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "welcome")
}

// TestGetMailTemplateName_FallbackChain verifies that the base-language
// fallback works: "zh-CN" with no region-specific template falls back to "en".
func TestGetMailTemplateName_FallbackChain(t *testing.T) {
	// No zh template exists, so it should fall back to English.
	result := GetMailTemplateName("zh-CN", "welcome")
	assert.NotEmpty(t, result)
	// Since no zh or zh-CN template files exist in test mode, expect en.
	assert.Equal(t, "welcome_en", result)
}

// TestT_UsesDefaultLanguageOnMissingKey is a regression test for the critical
// bug where T() discarded a valid message. When the requested language (zh) is
// loaded but a specific message exists only in the default language (en),
// go-i18n returns the rendered English message together with a
// MessageNotFoundErr. T() must return the rendered message, not the raw ID.
func TestT_UsesDefaultLanguageOnMissingKey(t *testing.T) {
	// Ensure zh is a known tag in the bundle by adding at least one zh message,
	// so the localizer actually resolves to zh (not the default). The key under
	// test then exists only in English, exercising the fallback-with-error path.
	if err := Bundle.AddMessages(language.Chinese, &i18n.Message{
		ID:    "regression_zh_marker",
		Other: "zh marker",
	}); err != nil {
		t.Fatalf("AddMessages(zh) failed: %v", err)
	}
	if err := Bundle.AddMessages(language.English, &i18n.Message{
		ID:    "regression_key_en_only",
		Other: "English value {{.Name}}",
	}); err != nil {
		t.Fatalf("AddMessages(en) failed: %v", err)
	}

	loc := GetLocalizer("zh", "")
	result := loc.T("regression_key_en_only", map[string]any{"Name": "World"})
	// Before the fix this returned the raw id "regression_key_en_only".
	assert.Equal(t, "English value World", result,
		"T() must return the rendered default-language message, not the raw id")
}

// TestT_StillFallsBackToIDWhenTotallyMissing verifies that when a key is
// missing from ALL languages (including the default), T() returns the id.
func TestT_StillFallsBackToIDWhenTotallyMissing(t *testing.T) {
	loc := GetLocalizer("zh", "")
	result := loc.T("totally_nonexistent_key_12345", nil)
	assert.Equal(t, "totally_nonexistent_key_12345", result)
}

// TestT_FallbackHonouredWhenNoMessage verifies that the "Default" option is
// used only when no rendered message is available at all.
func TestT_FallbackHonouredWhenNoMessage(t *testing.T) {
	loc := GetLocalizer("zh", "")
	result := loc.T("no_such_key_anywhere", nil, O{Key: "Default", Value: "Fallback Value"})
	assert.Equal(t, "Fallback Value", result)
}

// TestResolveLanguage_DefaultsToEnglish verifies that empty/unparseable input
// resolves to English rather than "und".
func TestResolveLanguage_DefaultsToEnglish(t *testing.T) {
	assert.Equal(t, "en", resolveLanguage(""))
}

// TestBaseLanguageTag verifies script-subtag preservation in base extraction.
func TestBaseLanguageTag(t *testing.T) {
	cases := []struct {
		in   string
		base string
		ok   bool
	}{
		{"zh-CN", "zh", true},
		{"zh-Hans-CN", "zh-Hans", true},
		{"zh-Hant-TW", "zh-Hant", true},
		{"en-US", "en", true},
		{"sr-Latn-RS", "sr-Latn", true},
		{"en", "", false},  // no region to strip
		{"und", "", false}, // invalid
	}
	for _, c := range cases {
		base, ok := baseLanguageTag(c.in)
		assert.Equal(t, c.ok, ok, "ok mismatch for %q", c.in)
		assert.Equal(t, c.base, base, "base mismatch for %q", c.in)
	}
}
