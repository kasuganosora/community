package matrix

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ====================================================================
// MessageBridge construction
// ====================================================================

func TestNewMessageBridge(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "a"})
	um := NewUserMapper(c)
	rm := NewRoomMapper(c, um)
	mb := NewMessageBridge(c, um, rm)
	require.NotNil(t, mb)
	assert.Equal(t, c, mb.client)
	assert.Equal(t, um, mb.userMapper)
	assert.Equal(t, rm, mb.roomMapper)
}

// ====================================================================
// convertMatrixMessageToStatus (pure logic, no DB)
// ====================================================================

func TestConvertMatrixMessageToStatus_PlainText(t *testing.T) {
	mb := &MessageBridge{}
	content := &RoomMessageContent{
		MsgType: MsgTypeText,
		Body:    "Hello world",
	}
	result := mb.convertMatrixMessageToStatus(content)
	assert.Equal(t, "Hello world", result)
}

func TestConvertMatrixMessageToStatus_Emote(t *testing.T) {
	mb := &MessageBridge{}
	content := &RoomMessageContent{
		MsgType: MsgTypeEmote,
		Body:    "waves",
	}
	result := mb.convertMatrixMessageToStatus(content)
	assert.Equal(t, "* waves", result)
}

func TestConvertMatrixMessageToStatus_HTMLFormatted(t *testing.T) {
	mb := &MessageBridge{}
	content := &RoomMessageContent{
		MsgType:       MsgTypeText,
		Body:          "fallback",
		Format:        "org.matrix.custom.html",
		FormattedBody: "<b>bold</b> and <i>italic</i>",
	}
	result := mb.convertMatrixMessageToStatus(content)
	assert.Equal(t, "bold and italic", result)
}

func TestConvertMatrixMessageToStatus_HTMLWithEntities(t *testing.T) {
	mb := &MessageBridge{}
	content := &RoomMessageContent{
		MsgType:       MsgTypeText,
		Body:          "x",
		Format:        "org.matrix.custom.html",
		FormattedBody: "&lt;tag&gt; &amp; stuff",
	}
	result := mb.convertMatrixMessageToStatus(content)
	assert.Contains(t, result, "<tag>")
	assert.Contains(t, result, "& stuff")
}

func TestConvertMatrixMessageToStatus_HTMLWrongFormat(t *testing.T) {
	mb := &MessageBridge{}
	// Format is not org.matrix.custom.html, so formatted_body is ignored.
	content := &RoomMessageContent{
		MsgType:       MsgTypeText,
		Body:          "use body",
		Format:        "other.format",
		FormattedBody: "<b>ignored</b>",
	}
	result := mb.convertMatrixMessageToStatus(content)
	assert.Equal(t, "use body", result)
}

func TestConvertMatrixMessageToStatus_EmoteWithHTML(t *testing.T) {
	mb := &MessageBridge{}
	content := &RoomMessageContent{
		MsgType:       MsgTypeEmote,
		Body:          "x",
		Format:        "org.matrix.custom.html",
		FormattedBody: "<b>dances</b>",
	}
	result := mb.convertMatrixMessageToStatus(content)
	// Emote prefix added after HTML stripping.
	assert.Equal(t, "* dances", result)
}

// ====================================================================
// convertStatusToMatrixMessage (pure logic, no DB)
// ====================================================================

func TestConvertStatusToMatrixMessage(t *testing.T) {
	mb := &MessageBridge{}
	content := mb.convertStatusToMatrixMessage("Hello\nworld")
	assert.Equal(t, MsgTypeText, content.MsgType)
	assert.Equal(t, "Hello\nworld", content.Body)
	assert.Equal(t, "org.matrix.custom.html", content.Format)
	// Newlines converted to <br>.
	assert.Contains(t, content.FormattedBody, "<br>")
}

func TestConvertStatusToMatrixMessage_TrimsWhitespace(t *testing.T) {
	mb := &MessageBridge{}
	content := mb.convertStatusToMatrixMessage("  hello  ")
	assert.Equal(t, "hello", content.Body)
}

func TestConvertStatusToMatrixMessage_EscapesHTML(t *testing.T) {
	mb := &MessageBridge{}
	content := mb.convertStatusToMatrixMessage("<script>alert(1)</script>")
	assert.NotContains(t, content.FormattedBody, "<script>")
	assert.Contains(t, content.FormattedBody, "&lt;script&gt;")
}

func TestConvertStatusToMatrixMessage_Empty(t *testing.T) {
	mb := &MessageBridge{}
	content := mb.convertStatusToMatrixMessage("")
	assert.Equal(t, "", content.Body)
}

// ====================================================================
// stripHTMLTags (pure logic, no DB)
// ====================================================================

func TestStripHTMLTags_Simple(t *testing.T) {
	mb := &MessageBridge{}
	result := mb.stripHTMLTags("<b>bold</b>")
	assert.Equal(t, "bold", result)
}

func TestStripHTMLTags_Nested(t *testing.T) {
	mb := &MessageBridge{}
	result := mb.stripHTMLTags("<div><p>hello</p><span>world</span></div>")
	assert.Equal(t, "hello\nworld", result)
}

func TestStripHTMLTags_LineBreak(t *testing.T) {
	mb := &MessageBridge{}
	result := mb.stripHTMLTags("line1<br>line2<br/>line3")
	assert.Equal(t, "line1\nline2\nline3", result)
}

func TestStripHTMLTags_NoTags(t *testing.T) {
	mb := &MessageBridge{}
	result := mb.stripHTMLTags("plain text")
	assert.Equal(t, "plain text", result)
}

func TestStripHTMLTags_WithAttributes(t *testing.T) {
	mb := &MessageBridge{}
	result := mb.stripHTMLTags(`<a href="http://example.com">link</a>`)
	assert.Equal(t, "link", result)
}

func TestStripHTMLTags_Empty(t *testing.T) {
	mb := &MessageBridge{}
	result := mb.stripHTMLTags("")
	assert.Equal(t, "", result)
}

func TestStripHTMLTags_Entities(t *testing.T) {
	mb := &MessageBridge{}
	// HTML entities are preserved in text nodes; stripHTMLTags extracts
	// text nodes which include the raw entity text.
	result := mb.stripHTMLTags("<p>5 &lt; 10</p>")
	assert.Contains(t, result, "5")
	assert.Contains(t, result, "10")
}

// ====================================================================
// textToHTML (pure logic, no DB)
// ====================================================================

func TestTextToHTML_Simple(t *testing.T) {
	mb := &MessageBridge{}
	result := mb.textToHTML("hello")
	assert.Equal(t, "hello", result)
}

func TestTextToHTML_Newlines(t *testing.T) {
	mb := &MessageBridge{}
	result := mb.textToHTML("line1\nline2\nline3")
	assert.Equal(t, "line1<br>line2<br>line3", result)
}

func TestTextToHTML_EscapesSpecial(t *testing.T) {
	mb := &MessageBridge{}
	result := mb.textToHTML("<>&\"")
	assert.Contains(t, result, "&lt;")
	assert.Contains(t, result, "&gt;")
}

func TestTextToHTML_Empty(t *testing.T) {
	mb := &MessageBridge{}
	result := mb.textToHTML("")
	assert.Equal(t, "", result)
}

// ====================================================================
// HandleMatrixMessage edge cases (no DB needed for early returns)
// ====================================================================

func TestHandleMatrixMessage_NilContent(t *testing.T) {
	mb := &MessageBridge{}
	// Event with nil content -> returns nil immediately.
	err := mb.HandleMatrixMessage(&Event{Content: nil})
	assert.NoError(t, err)
}

func TestHandleMatrixMessage_NonTextType(t *testing.T) {
	mb := &MessageBridge{}
	// m.image is not a text type -> returns nil without processing.
	err := mb.HandleMatrixMessage(&Event{
		Content: map[string]any{
			"msgtype": MsgTypeImage,
			"body":    "img",
		},
		EventID: "$e1",
	})
	assert.NoError(t, err)
}

func TestHandleMatrixMessage_EmptyContent(t *testing.T) {
	mb := &MessageBridge{}
	// Empty content map -> ToRoomMessageContent returns empty, non-text msgtype
	// -> early return.
	err := mb.HandleMatrixMessage(&Event{
		Content: map[string]any{},
	})
	assert.NoError(t, err)
}
