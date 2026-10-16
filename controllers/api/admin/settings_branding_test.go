package admin

import (
	"testing"

	"github.com/kasuganosora/community/systemsettings"
)

func TestBrandingSettingsValidation(t *testing.T) {
	fields := map[string]SettingsField{}
	for _, f := range settingsSections()["branding"].Fields {
		fields[f.Key] = f
	}
	cases := []struct {
		key   string
		value any
		ok    bool
	}{
		{systemsettings.KeyBrandThemeColor, "#86b300", true},
		{systemsettings.KeyBrandThemeColor, "", true},
		{systemsettings.KeyBrandThemeColor, "green", false},
		{systemsettings.KeyBrandEntrancePageStyle, "simple", true},
		{systemsettings.KeyBrandEntrancePageStyle, "fancy", false},
		{systemsettings.KeyBrandShowTimelineVisitor, false, true},
		{systemsettings.KeyBrandIconURL, "https://x.example/i.png", true},
		{systemsettings.KeyBrandIconURL, "javascript:alert(1)", false},
		{systemsettings.KeyBrandDefaultDarkTheme, `{"base":"dark","props":{"accent":"#fff"}}`, true},
		{systemsettings.KeyBrandDefaultDarkTheme, `{"base":`, false},
		{systemsettings.KeyBrandManifestJSONOverride, `{"short_name":"x"}`, true},
		{systemsettings.KeyBrandManifestJSONOverride, `[1]`, false},
	}
	for _, c := range cases {
		f, ok := fields[c.key]
		if !ok {
			t.Fatalf("field %s missing from branding section", c.key)
		}
		if err := validateSettingsValue(f, c.value); (err == nil) != c.ok {
			t.Errorf("%s=%v: got err %v, want ok=%v", c.key, c.value, err, c.ok)
		}
	}
}
