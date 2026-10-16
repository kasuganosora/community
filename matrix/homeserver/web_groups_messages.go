package homeserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	xhtml "golang.org/x/net/html"
	"html"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type webGroupSender struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}
type webGroupReaction struct {
	Key       string `json:"key"`
	Count     int    `json:"count"`
	Own       bool   `json:"own"`
	URL       string `json:"url,omitempty"`
	Shortcode string `json:"shortcode,omitempty"`
}
type webGroupEmoji struct {
	Shortcode string `json:"shortcode"`
	URL       string `json:"url"`
	StaticURL string `json:"static_url"`
}
type webGroupMessage struct {
	ID               string               `json:"id"`
	Sender           webGroupSender       `json:"sender"`
	Body             string               `json:"body"`
	FormattedBody    string               `json:"formatted_body,omitempty"`
	Kind             string               `json:"kind"`
	CreatedAt        int64                `json:"created_at"`
	Own              bool                 `json:"own"`
	Redacted         bool                 `json:"redacted"`
	Edited           bool                 `json:"edited"`
	ReplyUnavailable bool                 `json:"reply_unavailable,omitempty"`
	ReplyTo          string               `json:"reply_to,omitempty"`
	Attachments      []webGroupAttachment `json:"attachments,omitempty"`
	Reactions        []webGroupReaction   `json:"reactions"`
	TransactionID    string               `json:"transaction_id,omitempty"`
	CustomEmojis     []webGroupEmoji      `json:"custom_emojis,omitempty"`
}
type webGroupSendRequest struct {
	TransactionID string   `json:"transaction_id"`
	Body          string   `json:"body"`
	ReplyTo       string   `json:"reply_to"`
	UploadIDs     []string `json:"upload_ids"`
	Mentions      []string `json:"mentions"`
	CustomEmojis  []string `json:"custom_emojis"`
}

type webGroupHistoryCursor struct {
	Room     string `json:"r"`
	User     string `json:"u"`
	Position uint64 `json:"p"`
	Created  int64  `json:"t"`
	Event    string `json:"e"`
}

func registerWebGroupMessageRoutes(r *gin.RouterGroup) {
	r.GET("/groups/:id/messages", handleWebGroupMessages)
	r.POST("/groups/:id/messages", handleWebGroupSend)
	r.DELETE("/groups/:id/messages/:messageID", handleWebGroupRedact)
	r.POST("/groups/:id/messages/:messageID/replace", handleWebGroupReplace)
	r.POST("/groups/:id/messages/:messageID/reactions", handleWebGroupReaction)
	r.DELETE("/groups/:id/messages/:messageID/reactions", handleWebGroupUnreact)
	r.GET("/group-sync", handleWebGroupSync)
	r.POST("/group-sync", handleWebGroupSync)
}
func webGroupHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func webGroupEncode(value any) string {
	raw, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func webGroupDecode(raw string, value any) error {
	if len(raw) > 256*1024 {
		return errors.New("cursor too large")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, value)
}

func handleWebGroupMessages(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, false)
	if !ok {
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var cursor *webGroupHistoryCursor
	if token := c.Query("before"); token != "" {
		cursor = &webGroupHistoryCursor{}
		if webGroupDecode(token, cursor) != nil || cursor.Room != room.RoomID || cursor.User != user {
			webGroupFailure(c, 400, "invalid_cursor", "The history page has expired; reopen the group chat")
			return
		}
	}
	events, next, err := webGroupHistory(conn, room.RoomID, user, cursor, webGroupLimit(c, 40))
	if err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	messages, err := webGroupMessagesDTO(hs, conn, room.RoomID, user, events)
	if err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	var token any
	if next != nil {
		token = webGroupEncode(next)
	}
	c.JSON(200, gin.H{"messages": messages, "next_cursor": token})
}
func webGroupHistory(conn *gorm.DB, roomID, user string, cursor *webGroupHistoryCursor, limit int) ([]MatrixEvent, *webGroupHistoryCursor, error) {
	reader, err := newClientHistoryReader(conn, roomID, user)
	if err != nil {
		return nil, nil, err
	}
	accepted := []MatrixEvent{}
	current := cursor
	exhausted := false
	order := "stream_position DESC, created_at DESC, event_id DESC"
	if dialect.IsPostgres(conn) {
		// Legacy rows without a timestamp must follow dated rows, matching the
		// cursor predicates below. PostgreSQL otherwise puts NULL first in DESC.
		order = "stream_position DESC, created_at DESC NULLS LAST, event_id DESC"
	}
	// Bound work when most history is not visible. A cursor advances past examined
	// candidates even when a page is empty, so callers can continue safely.
	for scanned := 0; len(accepted) < limit && scanned < 2000; {
		q := conn.Where("room_id = ? AND state_key IS NULL AND event_type NOT IN ?", roomID, []string{"m.reaction", "m.room.redaction"})
		if current != nil {
			if current.Position > 0 {
				q = q.Where("stream_position < ?", current.Position)
			} else if current.Created == 0 {
				q = q.Where("stream_position = 0 AND created_at IS NULL AND event_id < ?", current.Event)
			} else {
				q = q.Where("stream_position = 0 AND (created_at < ? OR (created_at = ? AND event_id < ?) OR created_at IS NULL)", time.Unix(0, current.Created), time.Unix(0, current.Created), current.Event)
			}
		}
		var rows []MatrixEvent
		if err := q.Order(order).Limit(min(limit-len(accepted), 100)).Find(&rows).Error; err != nil {
			return nil, nil, err
		}
		if len(rows) == 0 {
			exhausted = true
			break
		}
		scanned += len(rows)
		visible, err := reader.filter(rows)
		if err != nil {
			return nil, nil, err
		}
		for _, event := range visible {
			if event.RelationType != "m.replace" && webGroupRelation(event.Content)["rel_type"] != "m.replace" {
				accepted = append(accepted, event)
			}
		}
		last := rows[len(rows)-1]
		current = &webGroupHistoryCursor{Room: roomID, User: user, Position: last.StreamPosition, Event: last.EventID}
		if last.CreatedAt != nil {
			current.Created = last.CreatedAt.UnixNano()
		}
	}
	slices.Reverse(accepted)
	if exhausted {
		return accepted, nil, nil
	}
	return accepted, current, nil
}
func webGroupRelation(content map[string]any) map[string]any {
	relation, _ := content["m.relates_to"].(map[string]any)
	return relation
}
func webGroupString(content map[string]any, key string) string {
	v, _ := content[key].(string)
	return v
}

// All authorization inside this function uses the same transaction and room row
// lock as encryption and membership event persistence.
func webGroupWriteGuard(tx *gorm.DB, room *MatrixRoom, user, eventType string, redact *MatrixEvent) *MatrixError {
	var member MatrixRoomMember
	if tx.Where("room_id = ? AND user_id = ? AND membership = ? AND forgotten = ?", room.RoomID, user, MembershipJoin, false).Take(&member).Error != nil {
		return ErrForbidden
	}
	if room.IsEncrypted {
		return &MatrixError{ErrCode: "M_GROUP_ENCRYPTED", Error: "This group uses end-to-end encryption; use a Matrix client"}
	}
	if _, _, partial := parsePartialStateMarker(room.CurrentStateID); partial {
		return &MatrixError{ErrCode: "M_GROUP_UNKNOWN", Error: "The group state is still syncing, please try again later"}
	}
	if room.RoomType != nil && *room.RoomType == "m.space" {
		return ErrForbidden
	}
	var states []MatrixRoomState
	if tx.Where("room_id = ? AND state_key = ? AND event_type IN ?", room.RoomID, "", []string{"m.room.create", "m.room.encryption", "m.room.tombstone", "m.room.power_levels"}).Find(&states).Error != nil {
		return ErrInternal
	}
	var create *MatrixRoomState
	pl := PowerLevelContent{StateDefault: 50, Ban: 50, Kick: 50, Redact: 50}
	for i := range states {
		s := &states[i]
		switch s.EventType {
		case "m.room.encryption":
			return &MatrixError{ErrCode: "M_GROUP_ENCRYPTED", Error: "This group uses end-to-end encryption; use a Matrix client"}
		case "m.room.tombstone":
			return &MatrixError{ErrCode: "M_GROUP_UPGRADED", Error: "This group has been upgraded; go to the new group"}
		case EventTypeRoomCreate:
			create = s
		case EventTypeRoomPowerLevels:
			b, _ := json.Marshal(s.Content)
			if json.Unmarshal(b, &pl) != nil {
				return ErrInternal
			}
		}
	}
	if create == nil {
		return &MatrixError{ErrCode: "M_GROUP_UNKNOWN", Error: "The group state is not confirmed yet, please try again later"}
	}
	power := pl.UsersDefault
	if p, ok := pl.Users[user]; ok {
		power = p
	}
	if version, err := gmsl.GetRoomVersion(gmsl.RoomVersion(room.RoomVersion)); err == nil && version.PrivilegedCreators() {
		pdu, err := eventPDUFromDB(tx, create.EventID, room.RoomVersion)
		if err != nil {
			return ErrForbidden
		}
		if slices.Contains(gmsl.CreatorsFromCreateEvent(pdu), user) {
			power = gmsl.CreatorPowerLevel
		}
	}
	required := pl.EventsDefault
	if p, ok := pl.Events[eventType]; ok {
		required = p
	}
	if redact != nil {
		if redact.Sender == user {
			return nil
		}
		required = pl.Redact
	}
	if power < required {
		return ErrForbidden
	}
	return nil
}

func webGroupPersist(hs *HomeServer, conn *gorm.DB, roomID, user, txn, hash, eventType string, content map[string]any, redacts, uploadID string) (*MatrixEvent, *MatrixError) {
	key := "$web_" + webGroupHash([]string{roomID, user, txn})
	var result MatrixEvent
	created := false
	var failure *MatrixError
	err := conn.Set("matrix:signer", hs).Transaction(func(tx *gorm.DB) error {
		var room MatrixRoom
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("room_id = ?", roomID).Take(&room).Error; err != nil {
			return err
		}
		var target *MatrixEvent
		if redacts != "" {
			target = &MatrixEvent{}
			if tx.Where("room_id = ? AND event_id = ? AND state_key IS NULL", roomID, redacts).Take(target).Error != nil {
				failure = ErrNotFound
				return errors.New("target unavailable")
			}
		}
		if failure = webGroupWriteGuard(tx, &room, user, eventType, target); failure != nil {
			return errors.New("write denied")
		}
		lookup := tx.Where("txn_key = ?", key).Take(&result).Error
		if lookup == nil {
			if result.WebContentHash != hash {
				failure = &MatrixError{ErrCode: "M_CONFLICT", Error: "This transaction ID was already used for different content"}
				return errors.New("txn conflict")
			}
			return nil
		}
		if !errors.Is(lookup, gorm.ErrRecordNotFound) {
			return lookup
		}
		if uploadID != "" {
			attachment, merr := webGroupUploadContent(tx, roomID, user, uploadID)
			if merr != nil {
				failure = merr
				return errors.New("upload unavailable")
			}
			for k, v := range attachment {
				content[k] = v
			}
		}
		result = MatrixEvent{EventID: key, RoomID: roomID, Sender: user, EventType: EventType(eventType), Content: content, OriginServerTS: time.Now().UnixMilli(), TxnKey: &key, TxnID: &txn, WebContentHash: hash}
		if redacts != "" {
			result.Redacts = &redacts
		}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if redacts != "" {
			if err := applyStoredRedactionTx(tx, roomID, redacts); err != nil {
				return err
			}
		}
		if uploadID != "" {
			if err := webGroupMarkUploadUsed(tx, &result, uploadID); err != nil {
				return err
			}
		}
		if err := tx.Model(&MatrixRoom{}).Where("room_id = ?", roomID).Updates(map[string]any{"last_event_id": result.EventID, "last_event_ts": result.OriginServerTS}).Error; err != nil {
			return err
		}
		if err := hs.queueNativeFederation(tx, &result); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		if failure != nil {
			return nil, failure
		}
		return nil, ErrInternal
	}
	if created {
		if hs.Relations != nil {
			_ = hs.Relations.StoreRelation(&result)
		}
		go hs.NotifyRoomEvent(roomID)
		if hs.Push != nil {
			go func() {
				var room MatrixRoom
				if db.GetDB().Where("room_id = ?", roomID).Take(&room).Error == nil {
					hs.Push.NotifyEvent(&result, &room)
				}
			}()
		}
	}
	return &result, nil
}
func webGroupValidateSend(hs *HomeServer, conn *gorm.DB, roomID, user string, req *webGroupSendRequest) (map[string]any, *MatrixError) {
	if strings.TrimSpace(req.TransactionID) == "" || len(req.TransactionID) > 128 || len(req.Body) > 32000 || len(req.UploadIDs) > 8 || len(req.Mentions) > 50 || len(req.CustomEmojis) > 50 {
		return nil, &MatrixError{ErrCode: "M_INVALID_PARAM", Error: "Too many messages or attachments"}
	}
	if strings.TrimSpace(req.Body) == "" && len(req.UploadIDs) == 0 {
		return nil, &MatrixError{ErrCode: "M_INVALID_PARAM", Error: "Enter a message or choose an attachment"}
	}
	content := map[string]any{"msgtype": "m.text", "body": req.Body}
	if req.ReplyTo != "" {
		reader, err := newClientHistoryReader(conn, roomID, user)
		if err != nil {
			return nil, ErrInternal
		}
		var target MatrixEvent
		if conn.Where("room_id = ? AND event_id = ? AND state_key IS NULL", roomID, req.ReplyTo).Take(&target).Error != nil {
			return nil, ErrNotFound
		}
		visible, err := reader.filter([]MatrixEvent{target})
		if err != nil || len(visible) != 1 {
			return nil, ErrForbidden
		}
		content["m.relates_to"] = map[string]any{"m.in_reply_to": map[string]any{"event_id": req.ReplyTo}}
	}
	ids := []string{}
	for _, id := range req.Mentions {
		if slices.Contains(ids, id) {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		var n int64
		if conn.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id IN ? AND membership = ?", roomID, ids, MembershipJoin).Count(&n).Error != nil {
			return nil, ErrInternal
		}
		if n != int64(len(ids)) {
			return nil, &MatrixError{ErrCode: "M_INVALID_PARAM", Error: "You can only mention current group members"}
		}
		content["m.mentions"] = map[string]any{"user_ids": ids}
	}
	if len(req.CustomEmojis) > 0 {
		emojis, err := webGroupLocalEmojis(conn, req.CustomEmojis)
		if err != nil {
			return nil, ErrInternal
		}
		if len(emojis) != len(slices.Compact(slices.Sorted(slices.Values(req.CustomEmojis)))) {
			return nil, &MatrixError{ErrCode: "M_INVALID_PARAM", Error: "Some emojis are unavailable or not allowed across servers"}
		}
		formatted := strings.ReplaceAll(html.EscapeString(req.Body), "\n", "<br>")
		for _, emoji := range emojis {
			var record models.Emoji
			if conn.Where("shortcode = ? AND domain = ?", emoji.Shortcode, "").Take(&record).Error != nil {
				return nil, ErrInternal
			}
			mxc, err := webGroupEmojiMXC(conn.Statement.Context, hs, record)
			if err != nil {
				return nil, &MatrixError{ErrCode: "M_INVALID_PARAM", Error: "The emoji could not be imported into the chat, please choose again"}
			}
			shortcode := ":" + emoji.Shortcode + ":"
			formatted = strings.ReplaceAll(formatted, shortcode, "<img data-mx-emoticon src=\""+html.EscapeString(mxc)+"\" alt=\""+shortcode+"\" title=\""+shortcode+"\" width=\"24\" height=\"24\">")
		}
		content["format"] = "org.matrix.custom.html"
		content["formatted_body"] = formatted
	}
	return content, nil
}
func webGroupLocalEmojis(conn *gorm.DB, codes []string) ([]webGroupEmoji, error) {
	out := []webGroupEmoji{}
	if len(codes) == 0 {
		return out, nil
	}
	var rows []models.Emoji
	if err := conn.Where("shortcode IN ? AND domain = ? AND disabled = ? AND local_only = ?", codes, "", false, false).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, e := range rows {
		if emojiasset.StorageKey(e.Url) != "" {
			out = append(out, webGroupEmoji{e.Shortcode, e.Url, e.StaticUrl})
		}
	}
	return out, nil
}
func handleWebGroupSend(c *gin.Context)    { webGroupHandleSend(c, false) }
func handleWebGroupReplace(c *gin.Context) { webGroupHandleSend(c, true) }
func webGroupHandleSend(c *gin.Context, replacing bool) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var req webGroupSendRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 96*1024)
	if c.ShouldBindJSON(&req) != nil {
		webGroupFailure(c, 400, "invalid_message", "Malformed message")
		return
	}
	content, merr := webGroupValidateSend(hs, conn, room.RoomID, user, &req)
	if merr != nil {
		webGroupError(c, merr)
		return
	}
	hash := webGroupHash(struct {
		Request webGroupSendRequest
		Replace string
	}{req, c.Param("messageID")})
	deleted := false
	if replacing {
		var target MatrixEvent
		if conn.Where("room_id = ? AND event_id = ? AND sender = ? AND event_type = ?", room.RoomID, c.Param("messageID"), user, EventTypeRoomMessage).Take(&target).Error != nil {
			webGroupError(c, ErrForbidden)
			return
		}
		if _, merr := webGroupPersist(hs, conn, room.RoomID, user, "replace-delete:"+req.TransactionID, hash, "m.room.redaction", map[string]any{"reason": "Deleted and reposted"}, target.EventID, ""); merr != nil {
			webGroupError(c, merr)
			return
		}
		deleted = true
	}
	events := []MatrixEvent{}
	ids := []string{}
	failed := []string{}
	var failure *MatrixError
	parts := append([]string(nil), req.UploadIDs...)
	if strings.TrimSpace(req.Body) != "" {
		parts = append([]string{""}, parts...)
	}
	for i, upload := range parts {
		part := map[string]any{}
		for k, v := range content {
			part[k] = v
		}
		prefix := "send:"
		if replacing {
			prefix = "replace-send:"
		}
		event, merr := webGroupPersist(hs, conn, room.RoomID, user, prefix+req.TransactionID+":"+strconv.Itoa(i), hash, "m.room.message", part, "", upload)
		if merr != nil {
			failure = merr
			failed = append(failed, parts[i:]...)
			break
		}
		events = append(events, *event)
		ids = append(ids, event.EventID)
	}
	messages, err := webGroupMessagesDTO(hs, conn, room.RoomID, user, events)
	if err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	for i := range messages {
		messages[i].TransactionID = req.TransactionID
	}
	if failure != nil {
		if deleted || len(events) > 0 {
			c.JSON(http.StatusConflict, gin.H{"error": failure.Error, "code": "partial_failure", "cause": failure.ErrCode, "deleted": deleted, "messages": messages, "event_ids": ids, "failed_upload_ids": failed})
			return
		}
		webGroupError(c, failure)
		return
	}
	c.JSON(200, gin.H{"status": "accepted", "messages": messages, "event_ids": ids})
}
func handleWebGroupRedact(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	id := c.Param("messageID")
	_, err := webGroupPersist(hs, db.GetDB().WithContext(c.Request.Context()), room.RoomID, user, "delete:"+id, webGroupHash(id), "m.room.redaction", map[string]any{}, id, "")
	if err != nil {
		webGroupError(c, err)
		return
	}
	c.JSON(200, gin.H{})
}
func handleWebGroupReaction(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var req struct {
		Key           string `json:"key"`
		TransactionID string `json:"transaction_id"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Key) == "" || len(req.Key) > 256 || req.TransactionID == "" || len(req.TransactionID) > 128 {
		webGroupFailure(c, 400, "invalid_reaction", "Choose an emoji")
		return
	}
	if !canReadClientEventContext(c.Request.Context(), room.RoomID, user, c.Param("messageID")) {
		webGroupError(c, ErrForbidden)
		return
	}
	content := map[string]any{"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": c.Param("messageID"), "key": req.Key}}
	if strings.HasPrefix(req.Key, ":") && strings.HasSuffix(req.Key, ":") {
		emojis, err := webGroupLocalEmojis(conn, []string{strings.Trim(req.Key, ":")})
		if err != nil || len(emojis) != 1 {
			webGroupFailure(c, 400, "invalid_emoji", "This emoji is unavailable or not allowed across servers")
			return
		}
		var emoji models.Emoji
		if conn.Where("shortcode = ? AND domain = ?", emojis[0].Shortcode, "").Take(&emoji).Error != nil {
			webGroupError(c, ErrInternal)
			return
		}
		mxc, err := webGroupEmojiMXC(c.Request.Context(), hs, emoji)
		if err != nil {
			webGroupFailure(c, 400, "invalid_emoji", "The emoji could not be imported into the chat, please choose again")
			return
		}
		content["m.relates_to"].(map[string]any)["key"] = mxc
		content["shortcode"] = req.Key
		content["im.ponies.emote"] = true
		content["org.community.emoji"] = map[string]any{"shortcode": emojis[0].Shortcode, "url": emojis[0].URL}
	}
	_, err := webGroupPersist(hs, conn, room.RoomID, user, "reaction:"+req.TransactionID, webGroupHash([]string{c.Param("messageID"), req.Key}), "m.reaction", content, "", "")
	if err != nil {
		webGroupError(c, err)
		return
	}
	c.JSON(200, gin.H{})
}
func handleWebGroupUnreact(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	key := c.Query("key")
	if key == "" || len(key) > 256 {
		webGroupFailure(c, 400, "invalid_reaction", "Choose an emoji")
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var events []MatrixEvent
	if conn.Where("room_id = ? AND sender = ? AND event_type = ? AND relation_target = ?", room.RoomID, user, "m.reaction", c.Param("messageID")).Find(&events).Error != nil {
		webGroupError(c, ErrInternal)
		return
	}
	for _, event := range events {
		relation := webGroupRelation(event.Content)
		matches := relation["key"] == key
		if !matches && strings.HasPrefix(key, ":") && strings.HasSuffix(key, ":") {
			var emoji models.Emoji
			if conn.Where("shortcode = ? AND domain = ?", strings.Trim(key, ":"), "").Take(&emoji).Error == nil {
				matches = relation["key"] == "mxc://"+hs.Config.ServerName+"/"+webGroupEmojiMediaID(emoji)
			}
		}
		if relation["event_id"] == c.Param("messageID") && matches {
			_, err := webGroupPersist(hs, conn, room.RoomID, user, "unreact:"+event.EventID, webGroupHash(event.EventID), "m.room.redaction", map[string]any{}, event.EventID, "")
			if err != nil {
				webGroupError(c, err)
				return
			}
		}
	}
	c.JSON(200, gin.H{})
}

// webGroupMessagesDTO batches sender and relation reads. Historical visibility
// is checked on both targets and relation events, so aggregates cannot reveal
// messages/edits from a period the viewer could not read.
func webGroupMessagesDTO(hs *HomeServer, conn *gorm.DB, roomID, user string, events []MatrixEvent) ([]webGroupMessage, error) {
	out := []webGroupMessage{}
	if len(events) == 0 {
		return out, nil
	}
	ids := []string{}
	senders := []string{}
	for _, event := range events {
		ids = append(ids, event.EventID)
		senders = append(senders, event.Sender)
	}
	var members []MatrixRoomMember
	if err := conn.Where("room_id = ? AND user_id IN ?", roomID, senders).Find(&members).Error; err != nil {
		return nil, err
	}
	profiles, err := webGroupMemberProfiles(hs, conn, roomID, members)
	if err != nil {
		return nil, err
	}
	// A JSON-path expression works on supported SQLite, PostgreSQL and MySQL.
	targetExpr := "json_extract(content, '$.\"m.relates_to\".event_id')"
	if conn.Dialector.Name() == "postgres" {
		targetExpr = "(content::jsonb -> 'm.relates_to' ->> 'event_id')"
	}
	// Separate indexed lookups: a single OR over a relation key, redaction key
	// and legacy JSON forces MySQL to walk the entire room on every page.
	related := []MatrixEvent{}
	seenRelated := map[string]bool{}
	queries := []*gorm.DB{
		conn.Where("room_id = ? AND state_key IS NULL AND relation_target IN ?", roomID, ids),
		conn.Where("room_id = ? AND state_key IS NULL AND redacts IN ?", roomID, ids),
		conn.Where("room_id = ? AND state_key IS NULL AND stream_position = 0 AND (relation_target IS NULL OR relation_target = '') AND "+targetExpr+" IN ?", roomID, ids),
	}
	for _, query := range queries {
		var rows []MatrixEvent
		if err := query.Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, event := range rows {
			if !seenRelated[event.EventID] {
				related = append(related, event)
				seenRelated[event.EventID] = true
			}
		}
	}
	reader, err := newClientHistoryReader(conn, roomID, user)
	if err != nil {
		return nil, err
	}
	related, err = reader.filter(related)
	if err != nil {
		return nil, err
	}
	edits := map[string]MatrixEvent{}
	reactions := map[string]map[string]map[string]bool{}
	reactionCodes := map[string]string{}
	reactionMedia := map[string]webGroupReaction{}
	redacted := map[string]bool{}
	for _, r := range related {
		if r.Redacts != nil {
			redacted[*r.Redacts] = true
		}
	}
	for _, r := range related {
		rel := webGroupRelation(r.Content)
		id := webGroupString(rel, "event_id")
		if rel["rel_type"] == "m.replace" {
			for _, target := range events {
				if target.EventID == id && target.Sender == r.Sender && r.EventType == target.EventType {
					if _, valid := r.Content["m.new_content"].(map[string]any); valid {
						previous, exists := edits[id]
						if !exists || r.OriginServerTS > previous.OriginServerTS || (r.OriginServerTS == previous.OriginServerTS && r.EventID > previous.EventID) {
							edits[id] = r
						}
					}
				}
			}
		}
		if r.EventType == "m.reaction" && rel["rel_type"] == "m.annotation" {
			key := webGroupString(rel, "key")
			if code := webGroupString(r.Content, "shortcode"); strings.HasPrefix(code, ":") && strings.HasSuffix(code, ":") {
				reactionCodes[key] = strings.Trim(code, ":")
			}
			if mediaURL := webGroupMediaURL(roomID, r.EventID, key); mediaURL != "" {
				reactionMedia[key] = webGroupReaction{URL: mediaURL, Shortcode: reactionCodes[key]}
			}
			if key != "" && len(key) <= 1024 {
				if reactions[id] == nil {
					reactions[id] = map[string]map[string]bool{}
				}
				if reactions[id][key] == nil {
					reactions[id][key] = map[string]bool{}
				}
				reactions[id][key][r.Sender] = true
			}
		}
	}
	allCodes := []string{}
	for _, event := range events {
		content := event.Content
		if edit, ok := edits[event.EventID]; ok {
			content, _ = edit.Content["m.new_content"].(map[string]any)
		}
		for _, code := range models.MatchEmojiTags(webGroupString(content, "body")) {
			allCodes = append(allCodes, strings.Trim(code, ":"))
		}
		for key := range reactions[event.EventID] {
			if code := reactionCodes[key]; code != "" {
				allCodes = append(allCodes, code)
			}
			if strings.HasPrefix(key, ":") && strings.HasSuffix(key, ":") {
				allCodes = append(allCodes, strings.Trim(key, ":"))
			}
		}
	}
	emojiRows, err := webGroupLocalEmojis(conn, allCodes)
	if err != nil {
		return nil, err
	}
	emojiMap := map[string]webGroupEmoji{}
	emojiByMXC := map[string]webGroupEmoji{}
	for _, e := range emojiRows {
		emojiMap[e.Shortcode] = e
		emojiByMXC["mxc://"+hs.Config.ServerName+"/"+webGroupEmojiMediaID(models.Emoji{Url: e.URL})] = e
	}
	replyIDs := []string{}
	for _, event := range events {
		content := event.Content
		if edit, ok := edits[event.EventID]; ok {
			content, _ = edit.Content["m.new_content"].(map[string]any)
		}
		rel := webGroupRelation(content)
		if reply, ok := rel["m.in_reply_to"].(map[string]any); ok {
			if id := webGroupString(reply, "event_id"); id != "" {
				replyIDs = append(replyIDs, id)
			}
		} else if rel["rel_type"] == "m.thread" {
			replyIDs = append(replyIDs, webGroupString(rel, "event_id"))
		}
	}
	readableReplies := map[string]bool{}
	if len(replyIDs) > 0 {
		var replies []MatrixEvent
		if err := conn.Where("room_id = ? AND event_id IN ?", roomID, replyIDs).Find(&replies).Error; err != nil {
			return nil, err
		}
		visible, err := reader.filter(replies)
		if err != nil {
			return nil, err
		}
		for _, e := range visible {
			readableReplies[e.EventID] = true
		}
	}
	for _, event := range events {
		p, ok := profiles[event.Sender]
		if !ok {
			p = webGroupSender{ID: event.Sender, DisplayName: event.Sender}
		}
		msg := webGroupMessage{ID: event.EventID, Sender: p, Kind: "unsupported", CreatedAt: event.OriginServerTS, Own: event.Sender == user, Reactions: []webGroupReaction{}, Redacted: redacted[event.EventID]}
		content := event.Content
		if edit, ok := edits[event.EventID]; ok && !msg.Redacted {
			content, _ = edit.Content["m.new_content"].(map[string]any)
			msg.Edited = true
		}
		if event.EventType == EventTypeRoomMessage {
			switch webGroupString(content, "msgtype") {
			case "m.text", "m.emote":
				msg.Kind = "text"
			case "m.notice":
				msg.Kind = "notice"
			case "m.image":
				msg.Kind = "image"
			case "m.file", "m.audio", "m.video":
				msg.Kind = "file"
			}
		}
		if msg.Redacted || len(content) == 0 {
			msg.Redacted = true
			msg.Body = "Message deleted"
			msg.Kind = "notice"
		} else if msg.Kind == "unsupported" {
			msg.Body = "This message type is not supported; view it in a Matrix client"
		} else {
			msg.Body, msg.FormattedBody = webGroupDisplayBody(content)
			copyEvent := event
			if edit, ok := edits[event.EventID]; ok {
				copyEvent = edit
			}
			copyEvent.Content = content
			msg.Attachments = webGroupAttachments(roomID, copyEvent)
			inline := webGroupInlineEmojis(roomID, copyEvent)
			inlineCodes := map[string]bool{}
			for _, emoji := range inline {
				inlineCodes[emoji.Shortcode] = true
			}
			msg.CustomEmojis = append(msg.CustomEmojis, inline...)
			for _, code := range models.MatchEmojiTags(msg.Body) {
				if emoji, ok := emojiMap[strings.Trim(code, ":")]; ok && !inlineCodes[emoji.Shortcode] {
					msg.CustomEmojis = append(msg.CustomEmojis, emoji)
				}
			}
		}
		rel := webGroupRelation(content)
		if reply, ok := rel["m.in_reply_to"].(map[string]any); ok {
			msg.ReplyTo = webGroupString(reply, "event_id")
		} else if rel["rel_type"] == "m.thread" {
			msg.ReplyTo = webGroupString(rel, "event_id")
		}
		if msg.ReplyTo != "" && !readableReplies[msg.ReplyTo] {
			msg.ReplyUnavailable = true
		}
		if !msg.Redacted {
			for key, users := range reactions[event.EventID] {
				r := webGroupReaction{Key: key, Count: len(users), Own: users[user]}
				if media, ok := reactionMedia[key]; ok {
					r.URL = media.URL
					r.Shortcode = media.Shortcode
				}
				if emoji, ok := emojiByMXC[key]; ok {
					r.Key = ":" + emoji.Shortcode + ":"
					r.URL = emoji.URL
					r.Shortcode = emoji.Shortcode
				}
				if emoji, ok := emojiMap[strings.Trim(key, ":")]; ok && key == ":"+emoji.Shortcode+":" {
					r.URL = emoji.URL
					r.Shortcode = emoji.Shortcode
				}
				msg.Reactions = append(msg.Reactions, r)
			}
			sort.Slice(msg.Reactions, func(i, j int) bool { return msg.Reactions[i].Key < msg.Reactions[j].Key })
		}
		out = append(out, msg)
	}
	return out, nil
}

// Matrix rich replies include an old-client fallback quote. The web UI renders
// the explicit reply context itself; do not duplicate that quote in the body.
func webGroupDisplayBody(content map[string]any) (string, string) {
	body := webGroupString(content, "body")
	formatted := ""
	if content["format"] == "org.matrix.custom.html" {
		formatted = webGroupString(content, "formatted_body")
	}
	relation := webGroupRelation(content)
	if _, reply := relation["m.in_reply_to"].(map[string]any); reply {
		if strings.HasPrefix(body, "> <@") {
			if _, answer, ok := strings.Cut(body, "\n\n"); ok {
				body = answer
			}
		}
		if formatted != "" {
			if root, err := xhtml.Parse(strings.NewReader(formatted)); err == nil {
				var prune func(*xhtml.Node)
				prune = func(node *xhtml.Node) {
					for child := node.FirstChild; child != nil; {
						next := child.NextSibling
						if child.Type == xhtml.ElementNode && child.Data == "mx-reply" {
							node.RemoveChild(child)
						} else {
							prune(child)
						}
						child = next
					}
				}
				prune(root)
				var out strings.Builder
				if xhtml.Render(&out, root) == nil {
					formatted = out.String()
				}
			}
		}
	}
	return body, htmlsafe.Sanitize(formatted)
}
