package linkcard

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var plainURL = regexp.MustCompile(`https?://[^\s<>"'\x60]+`)

// FirstLink returns the first http(s) link of a status that is neither a
// mention nor a hashtag. Content is HTML for federated posts and may be
// plain text for local ones.
func FirstLink(content string) string {
	if strings.Contains(content, "<") {
		if link := firstAnchor(content); link != "" {
			return link
		}
		// Plain URLs in text between tags (e.g. unlinked <p> text).
		return firstPlain(textOf(content))
	}
	return firstPlain(content)
}

func hasClass(classes, want string) bool {
	for _, c := range strings.Fields(classes) {
		if c == want {
			return true
		}
	}
	return false
}

func firstAnchor(content string) string {
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return ""
	}
	var found string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			attr := map[string]string{}
			for _, a := range n.Attr {
				attr[a.Key] = a.Val
			}
			skip := hasClass(attr["class"], "mention") || hasClass(attr["class"], "hashtag") ||
				attr["rel"] == "tag" || strings.Contains(attr["rel"], "tag")
			if !skip {
				if link := cleanLink(attr["href"]); link != "" {
					found = link
					return
				}
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found
}

func textOf(content string) string {
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return b.String()
}

func firstPlain(text string) string {
	for _, m := range plainURL.FindAllString(text, -1) {
		if link := cleanLink(strings.TrimRight(m, ".,;:!?)]}>")); link != "" {
			return link
		}
	}
	return ""
}

func cleanLink(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2048 {
		return ""
	}
	u, err := checkWebURL(raw)
	if err != nil {
		return ""
	}
	u.Fragment = ""
	return u.String()
}
