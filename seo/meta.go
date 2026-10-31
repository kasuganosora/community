// Package seo builds the HTML metadata and sitemap that search engines see
// before any client-side script runs.
package seo

import (
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Meta is the document head for one public URL. Empty fields are omitted.
// NoIndex is set only for surfaces that must stay out of search results
// (admin, settings, private pages, opted-out profiles, hidden tags).
type Meta struct {
	Title       string
	Description string
	Keywords    string
	Canonical   string
	Type        string // website, profile, or article
	Image       string
	SiteName    string
	NoIndex     bool
}

// Document is the HTTP status plus the head injected into the SPA shell.
type Document struct {
	Code int
	Meta Meta
}

func (m Meta) empty() bool {
	return m.Title == "" && m.Description == "" && m.Keywords == "" && m.Canonical == "" && m.Image == "" && m.SiteName == "" && !m.NoIndex
}

var titleTag = regexp.MustCompile(`(?i)<title>[^<]*</title>`)

// Inject writes meta into the SPA shell <head>. A document with no meta
// leaves the shell unchanged.
func Inject(raw []byte, doc Document) []byte {
	if doc.Meta.empty() {
		return raw
	}
	out := string(raw)
	if doc.Meta.Title != "" {
		tag := "<title>" + html.EscapeString(doc.Meta.Title) + "</title>"
		if titleTag.MatchString(out) {
			out = titleTag.ReplaceAllLiteralString(out, tag)
		} else {
			out = strings.Replace(out, "</head>", tag+"\n</head>", 1)
		}
	}
	block := renderMeta(doc.Meta)
	if strings.Contains(out, "</head>") {
		out = strings.Replace(out, "</head>", block+"</head>", 1)
	} else {
		out += block
	}
	return []byte(out)
}

func renderMeta(m Meta) string {
	var b strings.Builder
	writeName := func(name, content string) {
		if content == "" {
			return
		}
		b.WriteString(`<meta name="` + name + `" content="` + html.EscapeString(content) + `" />` + "\n    ")
	}
	writeProp := func(prop, content string) {
		if content == "" {
			return
		}
		b.WriteString(`<meta property="` + prop + `" content="` + html.EscapeString(content) + `" />` + "\n    ")
	}
	writeName("description", m.Description)
	writeName("keywords", m.Keywords)
	if m.NoIndex {
		writeName("robots", "noindex")
	}
	if m.Canonical != "" {
		b.WriteString(`<link rel="canonical" href="` + html.EscapeString(m.Canonical) + `" />` + "\n    ")
	}
	kind := m.Type
	if kind == "" {
		kind = "website"
	}
	writeProp("og:title", first(m.Title, m.SiteName))
	writeProp("og:description", m.Description)
	writeProp("og:url", m.Canonical)
	writeProp("og:type", kind)
	writeProp("og:site_name", m.SiteName)
	writeProp("og:image", m.Image)
	card := "summary"
	if m.Image != "" {
		card = "summary_large_image"
	}
	if m.Title != "" || m.Description != "" {
		writeName("twitter:card", card)
		writeName("twitter:title", first(m.Title, m.SiteName))
		writeName("twitter:description", m.Description)
		writeName("twitter:image", m.Image)
	}
	return b.String()
}

func first(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	if n == 1 {
		return "…"
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
