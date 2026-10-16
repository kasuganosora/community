package discovery

import "testing"

func TestLanguageTier(t *testing.T) {
	cases := []struct {
		cand   string
		viewer []string
		want   int
	}{
		{"zh", []string{"zh"}, languageTierExact},
		{"zh-TW", []string{"zh"}, languageTierExact},
		{"ja", []string{"zh", "en"}, languageTierClose},
		{"zh", []string{"ja"}, languageTierClose},
		{"en", []string{"zh", "en"}, languageTierExact},
		{"ru", []string{"zh"}, languageTierOther},
		{"", []string{"zh"}, languageTierOther},
		{"ko", []string{"zh"}, languageTierOther},
		{"zh", nil, languageTierOther},
	}
	for _, c := range cases {
		if got := languageTier(c.cand, c.viewer); got != c.want {
			t.Errorf("languageTier(%q,%v)=%d want %d", c.cand, c.viewer, got, c.want)
		}
	}
}
