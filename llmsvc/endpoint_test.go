package llmsvc

import "testing"

func TestChatEndpointResolution(t *testing.T) {
	cases := []struct{ base, path, want string }{
		{"https://api.example.com", "", "https://api.example.com/v1/chat/completions"},
		{"https://api.example.com/", "", "https://api.example.com/v1/chat/completions"},
		{"https://api.example.com/v1", "", "https://api.example.com/v1/chat/completions"},
		{"https://api.example.com/v1/", "", "https://api.example.com/v1/chat/completions"},
		{"https://open.bigmodel.cn/api/paas/v4", "", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"https://open.bigmodel.cn/api/coding/paas/v4", "", "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions"},
		{"https://generativelanguage.example/v1beta", "", "https://generativelanguage.example/v1beta/chat/completions"},
		{"https://api.example.com/openai", "", "https://api.example.com/openai/v1/chat/completions"},
		{"https://api.example.com/v1/chat/completions", "", "https://api.example.com/v1/chat/completions"},
		{"https://api.example.com/vendor", "", "https://api.example.com/vendor/v1/chat/completions"},
		{"https://api.example.com/api/coding/paas/v4", "/chat/completions", "https://api.example.com/api/coding/paas/v4/chat/completions"},
		{"https://api.example.com", "/custom/chat", "https://api.example.com/custom/chat"},
		{"https://api.example.com", "custom/chat", "https://api.example.com/custom/chat"},
		{"https://api.example.com/v1", "/v1/chat/completions", "https://api.example.com/v1/v1/chat/completions"},
		{"https://api.example.com", "https://other.example/x/chat/completions", "https://other.example/x/chat/completions"},
	}
	for _, c := range cases {
		if got := ChatEndpoint(c.base, c.path); got != c.want {
			t.Errorf("ChatEndpoint(%q, %q) = %q, want %q", c.base, c.path, got, c.want)
		}
	}
}

func TestEndpointRedactsAndValidates(t *testing.T) {
	p := ProviderConfig{Type: TypeOpenAICompatible, BaseURL: "https://user:secret@api.example.com/v4?key=abc"}
	if got := p.Endpoint(); got != "https://api.example.com/v4/chat/completions" {
		t.Errorf("Endpoint = %q", got)
	}
	for in, want := range map[string]bool{"": true, "/chat/completions": true, "https://x.example/chat": true, "chat": false, "/a/../b": false, "/a?b=1": false, "https://u:p@x.example/c": false} {
		if validChatPath(in) != want {
			t.Errorf("validChatPath(%q) != %v", in, want)
		}
	}
	cfg := Config{Providers: []ProviderConfig{{ID: "a", Name: "a", Type: TypeOpenAICompatible, BaseURL: "https://x.example", Model: "m", ChatPath: "bad path"}}}
	if err := Validate(cfg); err == nil {
		t.Error("invalid chat path accepted")
	}
}

func TestCheckTranslation(t *testing.T) {
	src := "<p>Hello world, this is a fairly long English sentence.</p>"
	cases := map[string]string{
		"<p>こんにちは世界、これはかなり長い英語の文です。</p>": "",
		"<p></p>":                          ReasonEmptyOutput,
		"<p>I'm sorry, I cannot help.</p>": ReasonRefusal,
		"<p>抱歉，无法翻译。这是一段很长的文字。</p>": ReasonRefusal,
		src:        ReasonUnchanged,
		"<p>あ</p>": ReasonLengthRatio,
	}
	for out, want := range cases {
		if got := CheckTranslation(src, out, "en", "ja"); got != want {
			t.Errorf("CheckTranslation(%q) = %q, want %q", out, got, want)
		}
	}
	// A source that itself contains the phrase is not a refusal.
	if got := CheckTranslation("<p>I can't wait for the weekend to start!</p>", "<p>週末が待ちきれない！ I can't wait</p>", "en", "ja"); got != "" {
		t.Errorf("source phrase flagged: %q", got)
	}
	// Unknown/same language: identical output is fine.
	if got := CheckTranslation(src, src, "", "ja"); got != "" {
		t.Errorf("unknown source flagged: %q", got)
	}
}
