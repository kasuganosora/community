package htmlsafe

import (
	xhtml "golang.org/x/net/html"
	"strings"
)

// Keep readable paragraph/list boundaries and actual link destinations in the
// plain fallback instead of flattening all block elements into one sentence.
func PlainText(fragment string) string {
	root, err := xhtml.Parse(strings.NewReader(fragment))
	if err != nil {
		return StripTags(fragment)
	}
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == xhtml.ElementNode && n.Data == "li" {
			b.WriteString("\n• ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type != xhtml.ElementNode {
			return
		}
		switch n.Data {
		case "a":
			for _, a := range n.Attr {
				if a.Key == "href" {
					b.WriteString(" (" + a.Val + ")")
					break
				}
			}
		case "p", "br", "blockquote", "pre", "ul", "ol":
			b.WriteByte('\n')
		}
	}
	walk(root)
	return strings.TrimSpace(b.String())
}
