package translation

import "testing"

func TestUnusableText(t *testing.T) {
	cases := []struct {
		name, in, out, src, target, want string
	}{
		{"exact echo", "Hello there, garden friends", "Hello there, garden friends", "pt", "zh-CN", "unchanged_output"},
		{"fullwidth comma echo", "@robin, about the pump noise haha", "@robin， about the pump noise haha", "pt", "zh-CN", "unchanged_output"},
		{"echo with spacing, emoji, url changes", "Look at this 🌻 https://a.example/x #garden", "Look  at this! https://b.example/y", "auto", "de", "unchanged_output"},
		{"latin output for chinese", "The garden pump is very noisy today", "The garden pump is noisy today indeed", "", "zh-CN", "wrong_script"},
		{"latin output for japanese", "The garden pump is very noisy today", "Garden pump: very noisy today!", "en", "ja", "wrong_script"},
		{"cyrillic target", "The garden pump is very noisy today", "The pump in the garden is noisy", "en", "ru", "wrong_script"},
		{"chinese stays chinese for english", "今天花园里的水泵非常吵闹真的", "今天花园里的水泵非常吵", "zh", "en", "wrong_script"},
		{"good chinese", "The garden pump is very noisy today", "今天花园的水泵非常吵", "en", "zh-CN", ""},
		{"chinese with latin names ok", "Robin and Alex fixed the pump", "Robin 和 Alex 修好了水泵", "en", "zh-CN", ""},
		{"short text ignored", "ok", "ok", "en", "zh-CN", ""},
		{"empty output", "Hello garden", "  ", "en", "fr", "empty_output"},
		{"same language echo allowed", "Hallo Garten Freunde", "Hallo Garten Freunde", "de", "de", ""},
		{"latin to latin translated", "Bom dia jardim amigos", "Good morning garden friends", "pt", "en", ""},
	}
	for _, c := range cases {
		if got := unusableText(c.in, c.out, c.src, c.target); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
