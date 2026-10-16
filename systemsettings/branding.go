package systemsettings

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
)

// Branding keys mirror Misskey's meta fields edited on admin/branding
// (entrancePageStyle, showTimelineForVisitor, showActivitiesForVisitor,
// iconUrl, app192IconUrl, app512IconUrl, bannerUrl, backgroundImageUrl,
// notFoundImageUrl, infoImageUrl, serverErrorImageUrl, themeColor,
// defaultLightTheme, defaultDarkTheme, repositoryUrl, feedbackUrl,
// manifestJsonOverride). They live in the key/value system_settings table,
// so no schema migration is required.
const (
	KeyBrandEntrancePageStyle     = "branding.entrance_page_style"
	KeyBrandShowTimelineVisitor   = "branding.show_timeline_for_visitor"
	KeyBrandShowActivitiesVisitor = "branding.show_activities_for_visitor"
	KeyBrandIconURL               = "branding.icon_url"
	KeyBrandApp192IconURL         = "branding.app192_icon_url"
	KeyBrandApp512IconURL         = "branding.app512_icon_url"
	KeyBrandBannerURL             = "branding.banner_url"
	KeyBrandBackgroundImageURL    = "branding.background_image_url"
	KeyBrandNotFoundImageURL      = "branding.not_found_image_url"
	KeyBrandInfoImageURL          = "branding.info_image_url"
	KeyBrandServerErrorImageURL   = "branding.server_error_image_url"
	KeyBrandThemeColor            = "branding.theme_color"
	KeyBrandDefaultLightTheme     = "branding.default_light_theme"
	KeyBrandDefaultDarkTheme      = "branding.default_dark_theme"
	KeyBrandRepositoryURL         = "branding.repository_url"
	KeyBrandFeedbackURL           = "branding.feedback_url"
	KeyBrandManifestJSONOverride  = "branding.manifest_json_override"
)

// DefaultRepositoryURL is used when the admin leaves repositoryUrl empty,
// like Misskey falls back to its own repository.
const DefaultRepositoryURL = "https://github.com/kasuganosora/community"

// Branding is the public branding document (camelCase like Misskey's meta).
type Branding struct {
	EntrancePageStyle        string `json:"entrancePageStyle"`
	ShowTimelineForVisitor   bool   `json:"showTimelineForVisitor"`
	ShowActivitiesForVisitor bool   `json:"showActivitiesForVisitor"`
	IconURL                  string `json:"iconUrl"`
	App192IconURL            string `json:"app192IconUrl"`
	App512IconURL            string `json:"app512IconUrl"`
	BannerURL                string `json:"bannerUrl"`
	BackgroundImageURL       string `json:"backgroundImageUrl"`
	NotFoundImageURL         string `json:"notFoundImageUrl"`
	InfoImageURL             string `json:"infoImageUrl"`
	ServerErrorImageURL      string `json:"serverErrorImageUrl"`
	ThemeColor               string `json:"themeColor"`
	DefaultLightTheme        string `json:"defaultLightTheme"`
	DefaultDarkTheme         string `json:"defaultDarkTheme"`
	RepositoryURL            string `json:"repositoryUrl"`
	FeedbackURL              string `json:"feedbackUrl"`
}

// GetBranding reads the current branding settings with Misskey defaults.
func GetBranding() Branding {
	s := func(k string) string { return strings.TrimSpace(GetString(k, "")) }
	b := Branding{
		EntrancePageStyle:        s(KeyBrandEntrancePageStyle),
		ShowTimelineForVisitor:   GetBool(KeyBrandShowTimelineVisitor, true),
		ShowActivitiesForVisitor: GetBool(KeyBrandShowActivitiesVisitor, true),
		IconURL:                  s(KeyBrandIconURL),
		App192IconURL:            s(KeyBrandApp192IconURL),
		App512IconURL:            s(KeyBrandApp512IconURL),
		BannerURL:                s(KeyBrandBannerURL),
		BackgroundImageURL:       s(KeyBrandBackgroundImageURL),
		NotFoundImageURL:         s(KeyBrandNotFoundImageURL),
		InfoImageURL:             s(KeyBrandInfoImageURL),
		ServerErrorImageURL:      s(KeyBrandServerErrorImageURL),
		ThemeColor:               s(KeyBrandThemeColor),
		DefaultLightTheme:        s(KeyBrandDefaultLightTheme),
		DefaultDarkTheme:         s(KeyBrandDefaultDarkTheme),
		RepositoryURL:            s(KeyBrandRepositoryURL),
		FeedbackURL:              s(KeyBrandFeedbackURL),
	}
	if b.EntrancePageStyle != "simple" {
		b.EntrancePageStyle = "classic"
	}
	if !ValidThemeColor(b.ThemeColor) {
		b.ThemeColor = ""
	}
	if b.RepositoryURL == "" {
		b.RepositoryURL = DefaultRepositoryURL
	}
	return b
}

var themeColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// ValidThemeColor accepts CSS hex colors (#rgb, #rgba, #rrggbb, #rrggbbaa).
func ValidThemeColor(v string) bool { return themeColorRe.MatchString(v) }

// ValidateJSONObject reports whether v is empty or a JSON object.
func ValidateJSONObject(v string) error {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("not an object")
	}
	return nil
}

// ValidateTheme validates a Misskey theme code: a JSON object whose optional
// "base" is light/dark and whose optional "props" is an object of strings.
func ValidateTheme(v string) error {
	if err := ValidateJSONObject(v); err != nil || strings.TrimSpace(v) == "" {
		return err
	}
	var t struct {
		Base  *string        `json:"base"`
		Props map[string]any `json:"props"`
	}
	if err := json.Unmarshal([]byte(v), &t); err != nil {
		return err
	}
	if t.Base != nil && *t.Base != "light" && *t.Base != "dark" {
		return fmt.Errorf("base must be light or dark")
	}
	for _, p := range t.Props {
		if _, ok := p.(string); !ok {
			return fmt.Errorf("props must be strings")
		}
	}
	return nil
}

// themePropVars maps Misskey theme props to the frontend's CSS tokens.
var themePropVars = map[string][]string{
	"accent":        {"--color-accent", "--color-accent-hover", "--color-accent-pressed", "--color-primary"},
	"bg":            {"--color-bg", "--color-bg-grouped"},
	"fg":            {"--color-label"},
	"fgHighlighted": {"--color-label-vibrant"},
	"panel":         {"--color-bg-elevated", "--color-bg-row"},
	"divider":       {"--color-separator", "--color-separator-opaque"},
	"link":          {"--color-accent-text"},
	"error":         {"--color-danger"},
	"success":       {"--color-success"},
	"warn":          {"--color-warning"},
	"like":          {"--color-like"},
	"renote":        {"--color-repost"},
}

var safeCSSColorRe = regexp.MustCompile(`^(?:#[0-9a-fA-F]{3,8}|(?:rgb|rgba|hsl|hsla)\([0-9.,%\s/-]+\)|[a-zA-Z]{3,20})$`)

// themeDeclarations turns a theme code into CSS declarations. Props that
// reference other props (Misskey's "@accent"/":darken<..." syntax) or are
// not plain colors are skipped: only literal colors are applied.
func themeDeclarations(code string) string {
	if strings.TrimSpace(code) == "" {
		return ""
	}
	var t struct {
		Props map[string]any `json:"props"`
	}
	if json.Unmarshal([]byte(code), &t) != nil {
		return ""
	}
	keys := make([]string, 0, len(t.Props))
	for k := range t.Props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		vars, ok := themePropVars[k]
		v, isStr := t.Props[k].(string)
		if !ok || !isStr || !safeCSSColorRe.MatchString(strings.TrimSpace(v)) {
			continue
		}
		for _, name := range vars {
			sb.WriteString(name + ":" + strings.TrimSpace(v) + ";")
		}
	}
	return sb.String()
}

// DefaultThemeCSS renders the instance default light/dark themes as a
// stylesheet keyed on <html theme-mode>. Empty when no theme is set.
func DefaultThemeCSS(b Branding) string {
	var css strings.Builder
	if d := themeDeclarations(b.DefaultLightTheme); d != "" {
		css.WriteString(`:root[theme-mode="light"]{` + d + `}`)
	}
	if d := themeDeclarations(b.DefaultDarkTheme); d != "" {
		css.WriteString(`:root[theme-mode="dark"]{` + d + `}`)
	}
	return css.String()
}

// Manifest builds the PWA manifest like Misskey's manifestHandler: the
// built-in document with branding applied, then manifestJsonOverride merged
// on top (top-level keys replace).
func Manifest() map[string]any {
	b := GetBranding()
	name := strings.TrimSpace(GetString(KeyTitle, ""))
	if name == "" {
		name = "Community"
	}
	desc := strings.TrimSpace(GetString(KeyDescription, ""))
	if desc == "" {
		desc = name
	}
	theme := b.ThemeColor
	if theme == "" {
		theme = "#ffffff"
	}
	icons := []any{}
	if b.App192IconURL != "" {
		icons = append(icons, map[string]any{"src": b.App192IconURL, "sizes": "192x192", "type": "image/png", "purpose": "maskable"})
	} else {
		icons = append(icons, map[string]any{"src": "/icons/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"})
	}
	if b.App512IconURL != "" {
		icons = append(icons, map[string]any{"src": b.App512IconURL, "sizes": "512x512", "type": "image/png", "purpose": "maskable"})
	} else {
		icons = append(icons,
			map[string]any{"src": "/icons/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
			map[string]any{"src": "/icons/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"})
	}
	if b.IconURL != "" {
		icons = append(icons, map[string]any{"src": b.IconURL, "sizes": "any", "purpose": "any"})
	} else {
		icons = append(icons, map[string]any{"src": "/logo.svg", "sizes": "any", "type": "image/svg+xml", "purpose": "any"})
	}
	m := map[string]any{
		"name": name, "short_name": name, "description": desc, "lang": "zh-CN",
		"id": "/", "start_url": "/", "scope": "/", "display": "standalone",
		"orientation": "portrait", "theme_color": theme, "background_color": "#ffffff",
		"icons": icons,
	}
	var override map[string]any
	if raw := strings.TrimSpace(GetString(KeyBrandManifestJSONOverride, "")); raw != "" && json.Unmarshal([]byte(raw), &override) == nil {
		for k, v := range override {
			m[k] = v
		}
	}
	return m
}

var (
	iconLinkRe  = regexp.MustCompile(`<link rel="icon"[^>]*>`)
	appleLinkRe = regexp.MustCompile(`<link rel="apple-touch-icon"[^>]*>`)
	themeMetaRe = regexp.MustCompile(`\s*<meta name="theme-color"[^>]*>`)
	titleRe     = regexp.MustCompile(`<title>[^<]*</title>`)
	appTitleRe  = regexp.MustCompile(`<meta name="(apple-mobile-web-app-title|application-name)" content="[^"]*"\s*/?>`)
)

// BrandIndexHTML applies branding to the SPA shell's <head>: favicon,
// apple-touch-icon, theme-color, title and the default theme stylesheet.
// Remote servers (Misskey's FetchInstanceMetadataService) read these too.
func BrandIndexHTML(raw []byte) []byte {
	b := GetBranding()
	out := string(raw)
	esc := html.EscapeString
	if b.IconURL != "" {
		out = iconLinkRe.ReplaceAllLiteralString(out, `<link rel="icon" href="`+esc(b.IconURL)+`" />`)
	}
	if apple := firstNonEmpty(b.App192IconURL, b.IconURL); apple != "" {
		out = appleLinkRe.ReplaceAllLiteralString(out, `<link rel="apple-touch-icon" href="`+esc(apple)+`" />`)
	}
	if b.ThemeColor != "" {
		replaced := false
		out = themeMetaRe.ReplaceAllStringFunc(out, func(string) string {
			if replaced {
				return ""
			}
			replaced = true
			return "\n    " + `<meta name="theme-color" content="` + esc(b.ThemeColor) + `" />`
		})
	}
	if title := strings.TrimSpace(GetString(KeyTitle, "")); title != "" {
		out = titleRe.ReplaceAllLiteralString(out, "<title>"+esc(title)+"</title>")
		out = appTitleRe.ReplaceAllStringFunc(out, func(m string) string {
			name := appTitleRe.FindStringSubmatch(m)[1]
			return `<meta name="` + name + `" content="` + esc(title) + `" />`
		})
	}
	if css := DefaultThemeCSS(b); css != "" {
		out = strings.Replace(out, "</head>", `<style id="instance-default-theme">`+css+"</style>\n  </head>", 1)
	}
	return []byte(out)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
