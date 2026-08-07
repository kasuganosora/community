package i18nz

import (
	"io/fs"
	"os"
	"strings"
	"sync"

	"github.com/kasuganosora/community/internalfs"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/text/language"
)

var Bundle *i18n.Bundle

// matcher caches the language.Matcher built from the Bundle's loaded language
// tags. It is initialised lazily on first use via sync.Once to avoid
// reconstructing the matcher on every GetLocalizer / GetMailTemplateName call.
//
// NOTE: all message files must be loaded into Bundle before the first call to
// getMatcher(). In this codebase that happens in init(), so this is safe.
var (
	matcherOnce   sync.Once
	cachedMatcher language.Matcher
)

// getMatcher returns the cached language matcher, creating it on first use.
// This is safe for concurrent access.
func getMatcher() language.Matcher {
	matcherOnce.Do(func() {
		cachedMatcher = language.NewMatcher(Bundle.LanguageTags())
	})
	return cachedMatcher
}

func init() {
	Bundle = i18n.NewBundle(language.English)
	Bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)

	// Skip i18n loading in test mode
	if isTestMode() {
		return
	}

	_, err := Bundle.LoadMessageFileFS(internalfs.Get(), "i18n/en.toml")
	if err != nil {
		panic(err)
	}
}

// isTestMode detects if we're running in test mode.
func isTestMode() bool {
	// Check command line arguments
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "-test.") {
			return true
		}
	}
	// Check environment variable
	if os.Getenv("GO_TEST_MODE") == "1" {
		return true
	}

	return false
}

func GetLocalizer(lang string, accept string) *Localizer {
	langs := make([]string, 0)

	if accept != "" {
		tag, _ := language.MatchStrings(getMatcher(), accept)
		langs = append(langs, tag.String())
	}

	if lang != "" {
		langs = append(langs, lang)
	}

	return &Localizer{*i18n.NewLocalizer(Bundle, langs...)}
}

type O struct {
	Key   string
	Value string
}

type Localizer struct {
	i18n.Localizer
}

// T translates the given message ID using the provided template data.
//
// Fallback behaviour (in priority order):
//  1. A fully localised message, including the case where the requested
//     language lacks the key but the bundle's default language (English)
//     has it. The go-i18n library returns a valid rendered message together
//     with a MessageNotFoundErr in that case, so we must inspect the message
//     rather than rely solely on the error.
//  2. The "Default" option value (if provided).
//  3. The message ID itself.
//
// T never panics on missing translations.
func (l *Localizer) T(id string, data map[string]any, options ...O) string {
	config := &i18n.LocalizeConfig{
		MessageID:    id,
		TemplateData: data,
	}

	var fallback string
	for _, option := range options {
		switch option.Key {
		case "PluralCount":
			config.PluralCount = option.Value
		case "Default":
			fallback = option.Value
		}
	}

	msg, err := l.Localize(config)
	// go-i18n may return a usable message even when err != nil: specifically
	// when the requested language tag is missing the key but the default
	// language supplies it. Prefer any non-empty rendered message over the
	// fallbacks below.
	if msg != "" {
		return msg
	}
	if err != nil {
		if fallback != "" {
			return fallback
		}
		return id
	}
	return msg
}

// resolveLanguage normalises a language string into a canonical BCP 47 tag.
// The input can be a single language code ("en", "zh-CN") or a full
// Accept-Language header ("en-US,en;q=0.9"). An empty or unparseable input
// falls back to the default language ("en").
func resolveLanguage(lang string) string {
	if lang == "" {
		return "en"
	}

	// If the input looks like an Accept-Language header (contains commas or
	// quality values), use the matcher to extract the best supported language.
	if strings.ContainsAny(lang, ",;") {
		tag, _ := language.MatchStrings(getMatcher(), lang)
		if tag == language.Und {
			return "en"
		}
		return tag.String()
	}

	// Single language code — canonicalise it. language.Make returns Und for
	// empty/unparseable input; fall back to English in that case.
	tag := language.Make(lang)
	if tag == language.Und {
		return "en"
	}
	return tag.String()
}

// GetMailTemplateName resolves the appropriate mail template name for the
// given language, implementing a fallback chain:
//
//	full tag (e.g. "zh-CN") → base language (e.g. "zh") → "en"
//
// The lang parameter accepts either a simple language code ("en", "zh") or a
// full Accept-Language header string ("en-US,en;q=0.9").
func GetMailTemplateName(lang string, templateName string) string {
	fsys := internalfs.Get()
	resolved := resolveLanguage(lang)

	// Try the resolved language tag (e.g. "zh-CN")
	if name := tryMailTemplate(fsys, templateName, resolved); name != "" {
		return name
	}

	// Fallback to base language (e.g. "zh" for "zh-CN", "zh-Hans" for
	// "zh-Hans-CN"). Use the structured tag so the script subtag is preserved
	// when only the region is stripped.
	if base, ok := baseLanguageTag(resolved); ok {
		if name := tryMailTemplate(fsys, templateName, base); name != "" {
			return name
		}
	}

	// Default fallback to English
	return templateName + "_en"
}

// baseLanguageTag returns the base language (language [+ script]) of a BCP 47
// tag string, e.g. "zh-CN" -> "zh", "zh-Hans-CN" -> "zh-Hans". It returns
// (base, false) when no region can be stripped or the tag is invalid.
func baseLanguageTag(tag string) (string, bool) {
	t := language.Make(tag)
	if t == language.Und {
		return "", false
	}
	b, s, r := t.Raw()
	if !r.IsCountry() && r.String() == "ZZ" {
		// No region present; nothing to strip.
		return "", false
	}
	base := b.String()
	if s.String() != "Zzzz" {
		base += "-" + s.String()
	}
	return base, true
}

// tryMailTemplate checks whether a mail template file exists for the given
// language and returns the template name (without path or extension) if found,
// or an empty string otherwise.
func tryMailTemplate(fsys fs.FS, templateName, lang string) string {
	path := "mails/" + templateName + "_" + lang + ".html"
	if _, err := fs.Stat(fsys, path); err != nil {
		return ""
	}
	return templateName + "_" + lang
}
