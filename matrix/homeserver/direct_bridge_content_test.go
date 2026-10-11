package homeserver

import (
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBridgeRichContentSanitizesAndPreservesStructure(t *testing.T) {
	d := "remote.example"
	s := &models.Status{Domain: &d, OriginUri: new("https://remote.example/notes/1"), Content: models.StatusContent{Content: `<p>Hello <a href="https://example.org/a">link</a></p><ul><li>One</li></ul><blockquote>Quote</blockquote><script>alert(1)</script><img src=x onerror=alert(1)>`}}
	c := bridgeStatusContent(s, "alice@remote.example")
	h := c["formatted_body"].(string)
	require.Contains(t, h, "<ul>")
	require.Contains(t, h, "<blockquote>")
	require.NotContains(t, h, "script")
	require.NotContains(t, h, "<img")
	require.Contains(t, c["body"], "https://example.org/a")
	require.Contains(t, c["body"], "\n")
	s.Content.SpoilerText = "warning <tag>"
	s.Sensitive = true
	c = bridgeStatusContent(s, "alice")
	require.NotContains(t, c["body"], "Hello")
	require.Contains(t, c["formatted_body"], "data-mx-spoiler=")
	require.NotContains(t, c["formatted_body"], "<script")
}
func TestBridgeOutboundHTMLAndPlainBoundaries(t *testing.T) {
	text, format, err := bridgeOutboundText(map[string]any{"body": "fallback", "format": "org.matrix.custom.html", "formatted_body": `<mx-reply><blockquote>private old quote</blockquote></mx-reply><p>Hello <strong>bold</strong> <a href="javascript:alert(1)">bad</a></p><iframe src=x></iframe>`}, true)
	require.NoError(t, err)
	require.Equal(t, "html", format)
	require.Contains(t, text, "<strong>bold</strong>")
	require.NotContains(t, text, "private old quote")
	require.NotContains(t, text, "javascript:")
	require.NotContains(t, text, "iframe")
	text, format, err = bridgeOutboundText(map[string]any{"body": "<b>literal</b>"}, false)
	require.NoError(t, err)
	require.Equal(t, "plain", format)
	require.Equal(t, "<b>literal</b>", text)
}
func TestBridgeMediaFilenameAndTypes(t *testing.T) {
	for _, typ := range []models.MediaType{models.MediaTypeImage, models.MediaTypeAudio, models.MediaTypeVideo, models.MediaTypeUnknown} {
		c := bridgeAttachmentContent("test.local", models.MediaAttachment{Type: typ, FileName: "random-key", Description: "../../original\nname.txt"}, MatrixMedia{MediaID: "opaque", ContentType: "video/mp4", FileSize: 42, Width: new(12), Height: new(16)}, false)
		require.Equal(t, "originalname.txt", c["filename"])
		require.Equal(t, "mxc://test.local/opaque", c["url"])
		require.Equal(t, 42, int(c["info"].(map[string]any)["size"].(int64)))
	}
}

func TestBridgeSensitiveAttachmentHasNoAutomaticPreview(t *testing.T) {
	c := bridgeAttachmentContent("test.local", models.MediaAttachment{Type: models.MediaTypeImage, Description: "photo.png"}, MatrixMedia{MediaID: "opaque", ContentType: "image/png", Width: new(10), Height: new(20)}, true)
	require.Equal(t, "m.file", c["msgtype"])
	require.Equal(t, "敏感附件：photo.png", c["body"])
	info := c["info"].(map[string]any)
	require.NotContains(t, info, "w")
	require.NotContains(t, info, "thumbnail_url")
	require.Equal(t, "image/png", info["mimetype"])
}
