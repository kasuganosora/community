package models

import (
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStatusExplicitHTMLFormatAndEditableSource(t *testing.T) {
	s := Status{Content: StatusContent{Content: `<p>Hello <strong>world</strong></p><p><a href="https://example.org">link</a></p><script>bad()</script>`, ContentFormat: "html"}}
	require.Contains(t, s.ContentHTML(htmlsafe.RenderOptions{}), "<strong>world</strong>")
	require.NotContains(t, s.ContentHTML(htmlsafe.RenderOptions{}), "script")
	text := s.Content.EditableText()
	require.Contains(t, text, "Hello world\n")
	require.Contains(t, text, "https://example.org")
	require.NotContains(t, text, "<p>")
	s.Content.ContentFormat = "unknown"
	require.Contains(t, s.ContentHTML(htmlsafe.RenderOptions{}), "&lt;p&gt;")
}
