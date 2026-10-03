package models

import (
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestImportedHTMLPreservesFormattingWithoutChangingLocalIdentity(t *testing.T) {
	source := `<p>Hello <strong>world</strong> <a href="https://example.test/page">link</a> :wave:</p><script>alert(1)</script><img src="x" onerror="alert(2)"><a href="javascript:alert(3)">bad</a>`
	status := Status{Content: StatusContent{Content: "plain fallback"}, Properties: map[string]any{"import_original_html": source}}
	rendered := status.ContentHTML(htmlsafe.RenderOptions{})
	require.Contains(t, rendered, "<p>")
	require.Contains(t, rendered, "<strong>world</strong>")
	require.Contains(t, rendered, `href="https://example.test/page"`)
	require.Contains(t, rendered, ":wave:")
	require.NotContains(t, rendered, "<script")
	require.NotContains(t, rendered, "onerror")
	require.NotContains(t, rendered, "javascript:")
	require.True(t, status.IsLocal())
	require.Nil(t, status.OriginUri)
	profile := Account{Note: "plain fallback", ImportedNoteHTML: source}
	require.Equal(t, rendered, profile.NoteHTML(htmlsafe.RenderOptions{}))
	require.True(t, profile.IsLocal())
	profile.ImportedNoteHTML = ""
	profile.Note = "<strong>literal</strong>"
	require.Contains(t, profile.NoteHTML(htmlsafe.RenderOptions{}), "&lt;strong&gt;")
	status.Properties = nil
	status.Content.Content = "<strong>literal</strong>"
	require.Contains(t, status.ContentHTML(htmlsafe.RenderOptions{}), "&lt;strong&gt;")
	status.Properties = map[string]any{"import_original_html": 123}
	require.Contains(t, status.ContentHTML(htmlsafe.RenderOptions{}), "&lt;strong&gt;")
}
