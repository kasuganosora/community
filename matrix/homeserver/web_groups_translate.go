package homeserver

import (
	"html"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/langdetect"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils/translation"
)

// webGroupPublicState reports whether a room is public for translation
// policy: anyone may join, or its history is world-readable.
func webGroupPublicState(state map[EventType]MatrixRoomState) bool {
	if rule, _ := state[EventTypeRoomJoinRules].Content["join_rule"].(string); rule == "public" {
		return true
	}
	visibility, _ := state[EventTypeRoomHistoryVisibility].Content["history_visibility"].(string)
	return visibility == "world_readable"
}

// webGroupMessageLanguage guesses the language of a text message so the web
// UI can hide Translate for messages already in the reader's language.
func webGroupMessageLanguage(msg *webGroupMessage) string {
	if msg.Redacted || (msg.Kind != "text" && msg.Kind != "notice") {
		return ""
	}
	return langdetect.Detect(msg.Body)
}

// handleWebGroupTranslate handles POST /groups/:id/messages/:messageID/translate
// with {"lang": "ja"}. Only room members who can read the event may translate
// it; the translation policy (group setting, LLM privacy) lives in the
// translation package and is shared with posts, as are rate limits.
func handleWebGroupTranslate(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, false)
	if !ok {
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	if webGroupPlaintext(hs, room) != nil {
		webGroupFailure(c, 404, "not_found", "Message not found")
		return
	}
	var event MatrixEvent
	if err := conn.Where("room_id = ? AND event_id = ? AND state_key IS NULL", room.RoomID, c.Param("messageID")).First(&event).Error; err != nil {
		webGroupFailure(c, 404, "not_found", "Message not found")
		return
	}
	reader, err := newClientHistoryReader(conn, room.RoomID, user)
	if err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	visible, err := reader.filter([]MatrixEvent{event})
	if err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	if len(visible) == 0 || event.EventType != EventTypeRoomMessage {
		webGroupFailure(c, 404, "not_found", "Message not found")
		return
	}
	messages, err := webGroupMessagesDTO(hs, conn, room.RoomID, user, visible)
	if err != nil || len(messages) != 1 {
		webGroupError(c, ErrInternal)
		return
	}
	msg := messages[0]
	if msg.Redacted || (msg.Kind != "text" && msg.Kind != "notice") {
		c.JSON(422, gin.H{"error": translation.ErrNothingToTranslate.Message, "error_code": translation.ErrNothingToTranslate.Code})
		return
	}
	var body struct {
		Lang string `json:"lang"`
	}
	_ = c.ShouldBindJSON(&body)
	if body.Lang == "" {
		body.Lang = c.Query("lang")
	}
	if body.Lang == "" {
		body.Lang = "en"
	}
	content := msg.FormattedBody
	if strings.TrimSpace(content) == "" {
		content = strings.ReplaceAll(html.EscapeString(msg.Body), "\n", "<br>")
	}
	states, err := webGroupStates(conn, []string{room.RoomID})
	if err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	var account *models.Account
	if raw, ok := c.Get("web_group_account"); ok {
		account, _ = raw.(*models.Account)
	}
	result, terr := translation.TranslateGroupMessage(c.Request.Context(), account, msg.ID, content, msg.Language, body.Lang, webGroupPublicState(states[room.RoomID]))
	if terr != nil {
		c.JSON(terr.Status, gin.H{"error": terr.Message, "error_code": terr.Code})
		return
	}
	c.JSON(200, result)
}
