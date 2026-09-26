package htmlsafe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

var opts = RenderOptions{BaseURL: "https://social.example", LocalDomain: "social.example"}

func TestRenderPlainText_EscapesHTML(t *testing.T) {
	for _, payload := range []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<a href="javascript:alert(1)">x</a>`,
		`"><svg onload=alert(1)>`,
	} {
		out := RenderPlainText(payload, opts)
		assert.NotContains(t, out, "<script", payload)
		assert.NotContains(t, out, "<img", payload)
		assert.NotContains(t, out, "<svg", payload)
		assert.NotContains(t, out, `href="javascript`, payload)
		assert.Contains(t, out, "&lt;", payload)
	}
}

func TestRenderPlainText_Linkifies(t *testing.T) {
	out := RenderPlainText("hi @bob and @carol@remote.example see https://x.example/a?b=1&c=2. #golang #日本語\nsecond line\n\nnew para", opts)

	assert.Equal(t, `<p>hi <span class="h-card"><a href="https://social.example/@bob" class="u-url mention">@<span>bob</span></a></span>`+
		` and <span class="h-card"><a href="https://remote.example/@carol" class="u-url mention">@<span>carol</span></a></span>`+
		` see <a href="https://x.example/a?b=1&amp;c=2" rel="nofollow noopener noreferrer" target="_blank">https://x.example/a?b=1&amp;c=2</a>.`+
		` <a href="https://social.example/tags/golang" class="mention hashtag" rel="tag">#<span>golang</span></a>`+
		` <a href="https://social.example/tags/%E6%97%A5%E6%9C%AC%E8%AA%9E" class="mention hashtag" rel="tag">#<span>日本語</span></a>`+
		`<br />second line</p><p>new para</p>`, out)
	san := Sanitize(out)
	assert.Contains(t, san, `class="u-url mention"`, "mentions survive the sanitizer")
	assert.Contains(t, san, `class="mention hashtag"`, "hashtags survive the sanitizer")
	assert.Equal(t, san, Sanitize(san), "sanitizer is idempotent")
}

func TestRenderPlainText_NotMentions(t *testing.T) {
	out := RenderPlainText("mail me at a@b.example or see page#anchor and &#39;", opts)
	assert.NotContains(t, out, "mention")
	assert.Contains(t, out, "&amp;#39;")
}

func TestRenderPlainText_URLWithQuotesCannotBreakAttribute(t *testing.T) {
	out := RenderPlainText(`https://x.example/"onmouseover="alert(1)`, opts)
	assert.NotContains(t, out, `"onmouseover=`)
	assert.NotContains(t, Sanitize(out), `onmouseover="`)
}

func TestSanitize_RemovesDangerousMarkup(t *testing.T) {
	cases := map[string]string{
		`<p>hi<script>alert(1)</script></p>`:            "<script",
		`<p><img src=x onerror=alert(1)></p>`:           "onerror",
		`<a href="javascript:alert(1)">x</a>`:           "javascript:",
		`<a href="JaVaScRiPt:alert(1)">x</a>`:           "alert(1)\"",
		`<p onclick="alert(1)">x</p>`:                   "onclick",
		`<span style="background:url(x)">x</span>`:      "style",
		`<iframe src="https://evil.example"></iframe>`:  "<iframe",
		`<a href="data:text/html;base64,PHNjcmlwdD4=">`: "data:",
		`<svg><script>alert(1)</script></svg>`:          "<svg",
		`<p class="evil-class x">x</p>`:                 "class",
	}

	for in, forbidden := range cases {
		out := Sanitize(in)
		assert.NotContains(t, strings.ToLower(out), strings.ToLower(forbidden), in)
	}
}

func TestSanitize_KeepsMastodonMarkup(t *testing.T) {
	in := `<p>Hello <span class="h-card"><a href="https://m.example/@a" class="u-url mention">@<span>a</span></a></span> ` +
		`<a href="https://m.example/tags/x" class="mention hashtag" rel="tag">#<span>x</span></a><br>` +
		`<strong>b</strong> <em>i</em> <code>c</code></p><blockquote><p>q</p></blockquote>`
	out := Sanitize(in)

	assert.Contains(t, out, `class="h-card"`)
	assert.Contains(t, out, `class="u-url mention"`)
	assert.Contains(t, out, `class="mention hashtag"`)
	assert.Contains(t, out, `href="https://m.example/@a"`)
	assert.Contains(t, out, `nofollow`)
	assert.Contains(t, out, "<strong>b</strong>")
	assert.Contains(t, out, "<blockquote>")
	assert.Contains(t, out, "<br")
}

func TestStripTags(t *testing.T) {
	assert.Equal(t, "CW: spoiler & more", StripTags(`<b>CW:</b> spoiler &amp; more<script>x</script>`))
}
