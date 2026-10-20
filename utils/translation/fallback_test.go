package translation

import "testing"

func TestExplicitTraditionalURL(t *testing.T) {
	cases := map[string]bool{
		"": false, "not a url": false, "ftp://x.example": false,
		"https://libretranslate.de": false, "https://libretranslate.de/": false, "HTTPS://LibreTranslate.de": false,
		"https://translate.internal.example": true, "http://10.0.0.5:5000": true, "https://libretranslate.de/gateway": true,
	}
	for in, want := range cases {
		if got := ExplicitTraditionalURL(in); got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestFallbackServiceRequiresExplicitURL(t *testing.T) {
	t.Setenv("TRANSLATION_PROVIDER", "")
	t.Setenv("LIBRETRANSLATE_URL", "")
	if _, ok := FallbackService(); ok {
		t.Fatal("public default must not be a fallback")
	}
	t.Setenv("LIBRETRANSLATE_URL", PublicLibreTranslateURL)
	if _, ok := FallbackService(); ok {
		t.Fatal("explicit public default must not be a fallback")
	}
	t.Setenv("LIBRETRANSLATE_URL", "https://translate.internal.example")
	if s, ok := FallbackService(); !ok || s.GetProviderName() != "libretranslate" {
		t.Fatal("self-hosted URL should be a fallback")
	}
	t.Setenv("TRANSLATION_PROVIDER", "deepl")
	if _, ok := FallbackService(); ok {
		t.Fatal("placeholder providers must not be a fallback")
	}
}
