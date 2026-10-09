// Package linkcard builds Mastodon-style link preview cards for statuses:
// the first link of a post is fetched through the SSRF-protected client and
// its OpenGraph / Twitter / oEmbed metadata is stored in status_cards.
package linkcard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/kasuganosora/community/htmlsafe"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	// MaxPageBytes bounds how much of a page (or oEmbed document) is read.
	MaxPageBytes = 1 << 20
	maxTitle     = 255
	maxDesc      = 500
)

// Meta is the preview metadata of one page.
type Meta struct {
	URL, Title, Description, Image string
	Type                           string
	AuthorName, AuthorURL          string
	ProviderName, ProviderURL      string
	oembedURL                      string
}

var (
	// ErrNotHTML is returned for responses that are not (X)HTML pages.
	ErrNotHTML = errors.New("linkcard: not an HTML page")
	errNoMeta  = errors.New("linkcard: page has no preview metadata")
)

// Fetcher downloads pages with the given (SSRF-safe) client.
type Fetcher struct {
	Client *http.Client
	// Validate rejects URLs before any request (scheme, host, resolved IP).
	Validate func(rawURL string) error
}

func checkWebURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return nil, fmt.Errorf("linkcard: unsupported URL %q", raw)
	}
	return u, nil
}

func (f *Fetcher) get(ctx context.Context, raw, accept string) (*http.Response, error) {
	if _, err := checkWebURL(raw); err != nil {
		return nil, err
	}
	if f.Validate != nil {
		if err := f.Validate(raw); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CommunityLinkPreview/1.0)")
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("linkcard: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// FetchMeta fetches rawURL and extracts its preview metadata.
func (f *Fetcher) FetchMeta(ctx context.Context, rawURL string) (*Meta, error) {
	resp, err := f.get(ctx, rawURL, "text/html,application/xhtml+xml;q=0.9")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	ctype := resp.Header.Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ctype)
	if mt != "text/html" && mt != "application/xhtml+xml" {
		return nil, ErrNotHTML
	}
	body := io.LimitReader(resp.Body, MaxPageBytes)
	reader, err := charset.NewReader(body, ctype)
	if err != nil {
		reader = body
	}
	final := resp.Request.URL
	meta := parseMeta(reader, final)
	meta.URL = rawURL
	if meta.oembedURL != "" {
		f.mergeOEmbed(ctx, meta)
	}
	meta.clean()
	if meta.Title == "" {
		return nil, errNoMeta
	}
	return meta, nil
}

// parseMeta reads the head of an HTML document.
func parseMeta(r io.Reader, base *url.URL) *Meta {
	m := &Meta{}
	z := html.NewTokenizer(r)
	og := map[string]string{}
	var htmlTitle string
	inTitle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			m.fill(og, htmlTitle, base)
			return m
		case html.TextToken:
			if inTitle && htmlTitle == "" {
				htmlTitle = strings.TrimSpace(string(z.Text()))
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(name) == "title" {
				inTitle = false
			}
			if string(name) == "head" {
				m.fill(og, htmlTitle, base)
				return m
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "title":
				inTitle = true
			case "body":
				m.fill(og, htmlTitle, base)
				return m
			case "meta":
				attrs := readAttrs(z, hasAttr)
				key := strings.ToLower(firstNonEmpty(attrs["property"], attrs["name"]))
				if key != "" && attrs["content"] != "" {
					if _, seen := og[key]; !seen {
						og[key] = attrs["content"]
					}
				}
			case "link":
				attrs := readAttrs(z, hasAttr)
				if strings.EqualFold(attrs["rel"], "alternate") && strings.EqualFold(attrs["type"], "application/json+oembed") && m.oembedURL == "" {
					m.oembedURL = resolve(base, attrs["href"])
				}
			}
		}
	}
}

func readAttrs(z *html.Tokenizer, has bool) map[string]string {
	attrs := map[string]string{}
	for has {
		var k, v []byte
		k, v, has = z.TagAttr()
		attrs[strings.ToLower(string(k))] = string(v)
	}
	return attrs
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func resolve(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	u, err := base.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	return u.String()
}

func (m *Meta) fill(og map[string]string, htmlTitle string, base *url.URL) {
	m.Title = firstNonEmpty(og["og:title"], og["twitter:title"], htmlTitle)
	m.Description = firstNonEmpty(og["og:description"], og["twitter:description"], og["description"])
	m.Image = resolve(base, firstNonEmpty(og["og:image"], og["og:image:url"], og["og:image:secure_url"], og["twitter:image"], og["twitter:image:src"]))
	m.ProviderName = og["og:site_name"]
	m.AuthorName = firstNonEmpty(og["article:author"], og["author"], og["twitter:creator"])
	if strings.HasPrefix(m.AuthorName, "http://") || strings.HasPrefix(m.AuthorName, "https://") {
		m.AuthorURL, m.AuthorName = resolve(base, m.AuthorName), ""
	}
	m.Type = "link"
}

// mergeOEmbed fills gaps from the page's oEmbed document. Only plain
// metadata is used; embedded HTML is never taken over.
func (f *Fetcher) mergeOEmbed(ctx context.Context, m *Meta) {
	resp, err := f.get(ctx, m.oembedURL, "application/json")
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); !strings.Contains(mt, "json") {
		return
	}
	var doc struct {
		Type         string `json:"type"`
		Title        string `json:"title"`
		AuthorName   string `json:"author_name"`
		AuthorURL    string `json:"author_url"`
		ProviderName string `json:"provider_name"`
		ProviderURL  string `json:"provider_url"`
		Thumbnail    string `json:"thumbnail_url"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, MaxPageBytes)).Decode(&doc) != nil {
		return
	}
	base := resp.Request.URL
	if m.Title == "" {
		m.Title = doc.Title
	}
	if m.AuthorName == "" {
		m.AuthorName = doc.AuthorName
	}
	if m.AuthorURL == "" {
		m.AuthorURL = resolve(base, doc.AuthorURL)
	}
	if m.ProviderName == "" {
		m.ProviderName = doc.ProviderName
	}
	if m.ProviderURL == "" {
		m.ProviderURL = resolve(base, doc.ProviderURL)
	}
	if m.Image == "" {
		m.Image = resolve(base, doc.Thumbnail)
	}
	if doc.Type == "photo" {
		m.Type = "photo"
	}
}

var spaces = regexp.MustCompile(`\s+`)

func plain(s string, max int) string {
	s = htmlsafe.StripTags(s)
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

// clean reduces every field to bounded plain text / valid http(s) URLs.
func (m *Meta) clean() {
	m.Title = plain(m.Title, maxTitle)
	m.Description = plain(m.Description, maxDesc)
	m.AuthorName = plain(m.AuthorName, maxTitle)
	m.ProviderName = plain(m.ProviderName, maxTitle)
	for _, u := range []*string{&m.Image, &m.AuthorURL, &m.ProviderURL} {
		if len(*u) > 1024 {
			*u = ""
		}
	}
	if m.ProviderName == "" {
		if u, err := url.Parse(m.URL); err == nil {
			m.ProviderName = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		}
	}
	if m.Type != "photo" {
		m.Type = "link"
	}
}
