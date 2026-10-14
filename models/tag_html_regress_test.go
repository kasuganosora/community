package models

import "testing"

func TestExtractTagNamesStripsMastodonHashtagMarkup(t *testing.T) {
	html := `<p>scary <a href="https://x/tags/horror" class="mention hashtag" rel="tag">#<span>horror</span></a></p><p>#ok&#39;s</p>`
	got := ExtractTagNames(html)
	if len(got) != 2 || got[0] != "horror" || got[1] != "ok" {
		t.Fatalf("got %q", got)
	}
	if n := CleanTagNames([]string{"#<span>horror</span></a></p>", "#horror"}); len(n) != 1 || n[0] != "horror" {
		t.Fatalf("CleanTagNames got %q", n)
	}
}
