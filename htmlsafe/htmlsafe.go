// Package htmlsafe makes status HTML safe for every consumer.
//
// Local statuses are stored as the plain text the author typed and rendered
// with RenderPlainText: everything is HTML-escaped, URLs, @mentions and
// #hashtags become links, and line breaks become <p>/<br />.
//
// Remote (federated) statuses arrive as HTML and are cleaned with Sanitize,
// an allowlist that mirrors what Mastodon keeps (p, br, span, a and a few
// inline/block formatting tags, only microformat/mention classes, only
// http/https/mailto links). Sanitize runs when remote content is ingested and
// again when any status is written to the API or ActivityPub (defense in
// depth for rows stored before sanitizing existed).
package htmlsafe

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

var (
	policyOnce sync.Once
	policy     *bluemonday.Policy
	strictOnce sync.Once
	strict     *bluemonday.Policy
)

// classAllow keeps only the class names Mastodon keeps: microformats
// (h-card, u-url, p-name, ...) and mention/hashtag/ellipsis/invisible.
var classAllow = regexp.MustCompile(`^(?:(?:[hpuedt]-[a-z-]+|mention|hashtag|ellipsis|invisible)(?:\s+|$))+$`)

var relAllow = regexp.MustCompile(`^(?:(?:nofollow|noopener|noreferrer|tag|me|ugc)(?:\s+|$))+$`)

func contentPolicy() *bluemonday.Policy {
	policyOnce.Do(func() {
		p := bluemonday.NewPolicy()
		p.AllowElements("p", "br", "span", "del", "s", "pre", "code", "blockquote",
			"b", "strong", "u", "i", "em", "ul", "ol", "li", "ruby", "rt", "rp")
		p.AllowAttrs("href").OnElements("a")
		p.AllowAttrs("rel").Matching(relAllow).OnElements("a")
		p.AllowAttrs("class").Matching(classAllow).OnElements("a", "span")
		p.AllowAttrs("start", "reversed").OnElements("ol")
		p.AllowAttrs("value").OnElements("li")
		p.AllowURLSchemes("http", "https", "mailto")
		p.RequireParseableURLs(true)
		p.AllowRelativeURLs(false)
		p.RequireNoFollowOnFullyQualifiedLinks(true)
		// target is only kept as _blank (bluemonday then adds noopener);
		// mention/hashtag links stay in-app so clients can route them.
		p.AllowAttrs("target").Matching(regexp.MustCompile(`^_blank$`)).OnElements("a")
		p.RequireNoReferrerOnFullyQualifiedLinks(false)
		policy = p
	})

	return policy
}

func strictPolicy() *bluemonday.Policy {
	strictOnce.Do(func() { strict = bluemonday.StrictPolicy() })

	return strict
}

// Sanitize cleans untrusted status HTML with the Mastodon-like allowlist.
func Sanitize(s string) string {
	if s == "" {
		return ""
	}

	return contentPolicy().Sanitize(s)
}

// StripTags removes all markup (for fields that are plain text in the
// Mastodon API, such as spoiler_text of remote statuses). Entities are
// decoded so the result is plain text; clients must escape it.
func StripTags(s string) string {
	if s == "" {
		return ""
	}

	return html.UnescapeString(strictPolicy().Sanitize(s))
}

// RenderOptions configures RenderPlainText.
type RenderOptions struct {
	// BaseURL of this instance, e.g. "https://social.example" (no trailing
	// slash). Used for local mention and hashtag links.
	BaseURL string
	// LocalDomain is this instance's host; mentions of @user@LocalDomain are
	// treated as local.
	LocalDomain string
}

// token matches, in order of precedence, a URL, a mention or a hashtag.
// Usernames and tags may contain any letter/number (CJK usernames are
// allowed at registration).
var token = regexp.MustCompile(
	`(https?://[^\s<>"'\x60]+)` +
		`|(?:^|[^\p{L}\p{N}_/@.])(@([\p{L}\p{N}_]+)(?:@([A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?\.[A-Za-z]{2,}))?)` +
		`|(?:^|[^\p{L}\p{N}_&/#])(#([\p{L}\p{N}_]*[\p{L}_][\p{L}\p{N}_]*))`)

var paragraphSplit = regexp.MustCompile(`\n[ \t]*\n+`)

// RenderPlainText renders a local status' plain text as safe HTML.
func RenderPlainText(text string, opts RenderOptions) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	text = strings.TrimSpace(text)

	if text == "" {
		return ""
	}

	base := strings.TrimRight(opts.BaseURL, "/")

	var b strings.Builder

	for _, para := range paragraphSplit.Split(text, -1) {
		b.WriteString("<p>")

		for i, line := range strings.Split(para, "\n") {
			if i > 0 {
				b.WriteString("<br />")
			}

			renderLine(&b, line, base, opts.LocalDomain)
		}

		b.WriteString("</p>")
	}

	return b.String()
}

func renderLine(b *strings.Builder, line, base, localDomain string) {
	pos := 0

	for _, m := range token.FindAllStringSubmatchIndex(line, -1) {
		var start, end int

		switch {
		case m[2] >= 0: // URL
			start, end = m[2], m[3]
			link := trimURL(line[start:end])
			end = start + len(link)
			b.WriteString(html.EscapeString(line[pos:start]))
			b.WriteString(`<a href="` + html.EscapeString(link) + `" rel="nofollow noopener noreferrer" target="_blank">`)
			b.WriteString(html.EscapeString(link))
			b.WriteString(`</a>`)
		case m[4] >= 0: // mention
			start, end = m[4], m[5]
			user := line[m[6]:m[7]]
			host := ""

			if m[8] >= 0 {
				host = line[m[8]:m[9]]
			}

			b.WriteString(html.EscapeString(line[pos:start]))

			href := base + "/@" + user
			if host != "" && !strings.EqualFold(host, localDomain) {
				href = "https://" + host + "/@" + user
			}

			b.WriteString(`<span class="h-card"><a href="` + html.EscapeString(href) + `" class="u-url mention">@<span>`)
			b.WriteString(html.EscapeString(user))
			b.WriteString(`</span></a></span>`)
		case m[10] >= 0: // hashtag
			start, end = m[10], m[11]
			tag := line[m[12]:m[13]]
			b.WriteString(html.EscapeString(line[pos:start]))
			b.WriteString(`<a href="` + html.EscapeString(base+"/tags/"+url.PathEscape(tag)) + `" class="mention hashtag" rel="tag">#<span>`)
			b.WriteString(html.EscapeString(tag))
			b.WriteString(`</span></a>`)
		default:
			continue
		}

		pos = end
	}

	b.WriteString(html.EscapeString(line[pos:]))
}

// trimURL drops trailing punctuation that is almost never part of a URL,
// and an unbalanced closing parenthesis.
func trimURL(u string) string {
	for len(u) > 0 {
		last := u[len(u)-1]

		switch {
		case strings.IndexByte(".,;:!?'\"", last) >= 0:
			u = u[:len(u)-1]
		case last == ')' && strings.Count(u, "(") < strings.Count(u, ")"):
			u = u[:len(u)-1]
		default:
			return u
		}
	}

	return u
}

// OptionsForBaseURL builds RenderOptions for an instance base URL such as
// "https://social.example".
func OptionsForBaseURL(baseURL string) RenderOptions {
	opts := RenderOptions{BaseURL: strings.TrimRight(baseURL, "/")}
	if u, err := url.Parse(opts.BaseURL); err == nil {
		opts.LocalDomain = u.Hostname()
	}

	return opts
}

// LocalOrRemote returns safe HTML for text that is plain text when local
// and HTML when it came from a remote server.
func LocalOrRemote(text string, local bool, opts RenderOptions) string {
	if local {
		return Sanitize(RenderPlainText(text, opts))
	}

	return Sanitize(text)
}
