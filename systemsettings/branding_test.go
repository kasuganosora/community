package systemsettings

import (
	"strings"
	"testing"
)

func withSettings(t *testing.T, kv map[string]any) {
	t.Helper()
	*Loaded = true
	old := map[string]any{}
	for k, v := range *Settings {
		old[k] = v
	}
	for k, v := range kv {
		Set(k, v)
	}
	t.Cleanup(func() {
		mu.Lock()
		settings = old
		mu.Unlock()
	})
}

func TestBrandingDefaults(t *testing.T) {
	withSettings(t, map[string]any{KeyBrandEntrancePageStyle: "bogus", KeyBrandThemeColor: "red;}"})
	b := GetBranding()
	if b.EntrancePageStyle != "classic" || !b.ShowTimelineForVisitor || !b.ShowActivitiesForVisitor {
		t.Fatalf("unexpected defaults %+v", b)
	}
	if b.ThemeColor != "" || b.RepositoryURL != DefaultRepositoryURL {
		t.Fatalf("invalid color must be dropped and repository defaulted: %+v", b)
	}
}

func TestValidateTheme(t *testing.T) {
	for _, ok := range []string{"", `{"base":"dark","props":{"accent":"#f00"}}`, `{}`} {
		if ValidateTheme(ok) != nil {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"{", "[]", "null", `{"base":"blue"}`, `{"props":{"accent":1}}`} {
		if ValidateTheme(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestManifestOverrideAndIcons(t *testing.T) {
	withSettings(t, map[string]any{
		KeyTitle:                     "Hime",
		KeyBrandThemeColor:           "#86b300",
		KeyBrandApp192IconURL:        "https://cdn.example/192.png",
		KeyBrandManifestJSONOverride: `{"short_name":"H","display":"minimal-ui"}`,
	})
	m := Manifest()
	if m["name"] != "Hime" || m["short_name"] != "H" || m["display"] != "minimal-ui" || m["theme_color"] != "#86b300" {
		t.Fatalf("manifest not branded/overridden: %v", m)
	}
	icons := m["icons"].([]any)
	if icons[0].(map[string]any)["src"] != "https://cdn.example/192.png" {
		t.Fatalf("192 icon not used: %v", icons)
	}
}

func TestBrandIndexHTML(t *testing.T) {
	withSettings(t, map[string]any{
		KeyTitle:                  `A&B`,
		KeyBrandIconURL:           `https://cdn.example/i.png?a=1&b="2"`,
		KeyBrandThemeColor:        "#123456",
		KeyBrandDefaultDarkTheme:  `{"base":"dark","props":{"accent":"#ff0000","bg":"@panel","fg":"red;}body{x:1"}}`,
		KeyBrandDefaultLightTheme: "",
	})
	raw := `<head>
    <link rel="icon" type="image/svg+xml" href="/logo.svg" />
    <meta name="theme-color" media="(prefers-color-scheme: light)" content="#ffffff" />
    <meta name="theme-color" media="(prefers-color-scheme: dark)" content="#000000" />
    <meta name="apple-mobile-web-app-title" content="Community" />
    <link rel="apple-touch-icon" href="/icons/apple-touch-icon.png" />
    <title>Community</title>
  </head>`
	out := string(BrandIndexHTML([]byte(raw)))
	for _, want := range []string{
		`<link rel="icon" href="https://cdn.example/i.png?a=1&amp;b=&#34;2&#34;" />`,
		`<link rel="apple-touch-icon" href="https://cdn.example/i.png?a=1&amp;b=&#34;2&#34;" />`,
		`<meta name="theme-color" content="#123456" />`,
		`<title>A&amp;B</title>`,
		`<meta name="apple-mobile-web-app-title" content="A&amp;B" />`,
		`:root[theme-mode="dark"]{--color-accent:#ff0000;`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if strings.Count(out, `name="theme-color"`) != 1 || strings.Contains(out, "body{") || strings.Contains(out, "@panel") {
		t.Errorf("unsafe or duplicate output:\n%s", out)
	}
}
