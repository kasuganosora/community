package homeserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Both routers set the authenticated Matrix identity. The caller supplies
// either Matrix authentication or Community OAuth read/write scopes.
func RegisterCommunityConversationRoutes(group *gin.RouterGroup) {
	group.GET("/conversations", handleBridgeConversations)
	group.POST("/conversations", handleBridgeConversationCreate)
	group.GET("/conversations/:id", handleBridgeConversationGet)
	group.POST("/conversations/:id/members", handleBridgeConversationMembers)
	group.DELETE("/conversations/:id/members/:memberID", handleBridgeConversationMembers)
	group.GET("/conversations/:id/messages", handleBridgeMessages)
	group.POST("/conversations/:id/messages", handleBridgeMessageSend)
	group.DELETE("/conversations/:id/messages/:eventID", handleBridgeMessageDelete)
	group.POST("/conversations/:id/messages/:eventID/replace", handleBridgeMessageSend)
	group.GET("/conversations/:id/messages/:eventID/delivery", handleBridgeMessageDelivery)
	group.POST("/conversations/:id/messages/:eventID/retry", handleBridgeMessageDelivery)
}
func bridgeAPIError(c *gin.Context, err error) {
	code, errcode, message := http.StatusInternalServerError, "M_UNKNOWN", "会话操作暂时失败，请稍后重试"
	if errors.Is(err, gorm.ErrRecordNotFound) {
		code, errcode, message = http.StatusNotFound, "M_NOT_FOUND", "会话或消息不存在"
	}
	if errors.Is(err, errDirectMessageSkipped) {
		code, errcode, message = http.StatusForbidden, "M_FORBIDDEN", "当前成员或消息权限不允许此操作"
	}
	if errors.Is(err, errOutboundParentPending) {
		code, errcode, message = http.StatusConflict, "M_NOT_YET_READY", "原消息正在投递，请稍后重试编辑"
	}
	c.JSON(code, gin.H{"error": message, "errcode": errcode})
}
func bridgeMatrixAPIError(c *gin.Context, err *MatrixError) {
	code := http.StatusInternalServerError
	switch err.ErrCode {
	case "M_FORBIDDEN", "M_UNAUTHORIZED":
		code = http.StatusForbidden
	case "M_NOT_FOUND":
		code = http.StatusNotFound
	case "M_NOT_YET_READY", "M_CONFLICT":
		code = http.StatusConflict
	case "M_BAD_JSON", "M_INVALID_PARAM":
		code = http.StatusBadRequest
	}
	c.JSON(code, err)
}

func bridgeConversationDTO(conn *gorm.DB, conv *BridgeConversation, user string) (gin.H, error) {
	var members []BridgeConversationMember
	if err := conn.Where("conversation_id = ? AND left_revision = 0", conv.ID).Order("id").Find(&members).Error; err != nil {
		return nil, err
	}
	canManage := false
	for _, m := range members {
		if m.MatrixID == user && m.AccountID == conv.CreatorAccountID {
			canManage = !conv.Legacy
		}
	}
	memberViews := []gin.H{}
	var ids []uint64
	for _, m := range members {
		if m.AccountID != 0 {
			ids = append(ids, m.AccountID)
		}
	}
	var accounts []models.Account
	if len(ids) > 0 {
		if err := conn.Where("id IN ?", ids).Find(&accounts).Error; err != nil {
			return nil, err
		}
	}
	byID := map[uint64]models.Account{}
	for _, a := range accounts {
		byID[a.Id] = a
	}
	for _, m := range members {
		v := gin.H{"id": strconv.FormatUint(m.ID, 10), "account_id": strconv.FormatUint(m.AccountID, 10), "matrix_id": m.MatrixID, "kind": m.Kind, "active": true}
		if a, ok := byID[m.AccountID]; ok {
			v["acct"] = a.Username
			if a.Domain != nil && *a.Domain != "" {
				v["acct"] = a.Username + "@" + *a.Domain
			}
			v["display_name"] = a.DisplayName
		}
		memberViews = append(memberViews, v)
	}
	out := gin.H{"id": conv.ID, "room_id": conv.RoomID, "revision": conv.Revision, "encrypted": false, "title": bridgeConversationTitle(members), "creator_id": strconv.FormatUint(conv.CreatorAccountID, 10), "can_manage": canManage, "can_leave": !conv.Legacy, "members": memberViews}
	var source models.Status
	if err := conn.Joins("JOIN matrix_dm_deliveries d ON d.status_id = statuses.id").Where("d.room_id = ? AND statuses.conversation_id IS NOT NULL", conv.RoomID).Order("statuses.id DESC").First(&source).Error; err == nil && source.ConversationId != nil {
		out["legacy_conversation_id"] = strconv.FormatUint(*source.ConversationId, 10)
	}
	return out, nil
}
func handleBridgeConversationCreate(c *gin.Context) {
	var req BridgeConversationRequest
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}
	hs := GetHomeServer()
	conv, err := hs.CreateBridgeConversation(c.Request.Context(), getUserID(c), req)
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	out, err := bridgeConversationDTO(db.GetDB().WithContext(c.Request.Context()), conv, getUserID(c))
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	c.JSON(200, out)
}
func handleBridgeConversations(c *gin.Context) {
	hs := GetHomeServer()
	conn := db.GetDB().WithContext(c.Request.Context())
	if err := hs.importLegacyBridgeConversations(c.Request.Context(), getUserID(c)); err != nil {
		bridgeAPIError(c, err)
		return
	}
	var rows []BridgeConversation
	query := conn.Select("matrix_bridge_conversations.*").Joins("JOIN matrix_rooms_hs r ON r.room_id = matrix_bridge_conversations.room_id").Joins("JOIN matrix_bridge_conversation_members m ON m.conversation_id = matrix_bridge_conversations.id").Where("m.matrix_id = ? AND m.left_revision = 0", getUserID(c))
	if cursor := c.Query("cursor"); cursor != "" {
		parts := strings.SplitN(cursor, "_", 2)
		stamp, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || len(parts) != 2 {
			c.JSON(400, gin.H{"error": "invalid cursor"})
			return
		}
		query = query.Where("r.last_event_ts < ? OR (r.last_event_ts = ? AND matrix_bridge_conversations.id < ?)", stamp, stamp, parts[1])
	}
	if err := query.Order("r.last_event_ts DESC, matrix_bridge_conversations.id DESC").Limit(40).Find(&rows).Error; err != nil {
		bridgeAPIError(c, err)
		return
	}
	result := []gin.H{}
	for _, conv := range rows {
		if _, _, err := hs.bridgeConversationAccess(conn, conv.ID, getUserID(c)); err != nil {
			continue
		}
		out, err := bridgeConversationDTO(conn, &conv, getUserID(c))
		if err != nil {
			bridgeAPIError(c, err)
			return
		}
		result = append(result, out)
	}
	var next any
	if len(rows) == 40 {
		last := rows[len(rows)-1]
		var room MatrixRoom
		if err := conn.Select("last_event_ts").First(&room, "room_id = ?", last.RoomID).Error; err != nil {
			bridgeAPIError(c, err)
			return
		}
		stamp := int64(0)
		if room.LastEventTS != nil {
			stamp = *room.LastEventTS
		}
		next = fmt.Sprintf("%d_%s", stamp, last.ID)
	}
	c.JSON(200, gin.H{"conversations": result, "next_cursor": next})
}
func handleBridgeConversationGet(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context())
	conv, _, err := GetHomeServer().bridgeConversationAccess(conn, c.Param("id"), getUserID(c))
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	out, err := bridgeConversationDTO(conn, conv, getUserID(c))
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	c.JSON(200, out)
}
func handleBridgeConversationMembers(c *gin.Context) {
	var req BridgeConversationRequest
	var remove uint64
	if c.Request.Method == http.MethodDelete {
		remove, _ = strconv.ParseUint(c.Param("memberID"), 10, 64)
		if remove == 0 {
			c.Status(400)
			return
		}
	} else if c.ShouldBindJSON(&req) != nil {
		c.Status(400)
		return
	}
	if err := GetHomeServer().ChangeBridgeMembers(c.Request.Context(), c.Param("id"), getUserID(c), req, remove); err != nil {
		bridgeAPIError(c, err)
		return
	}
	if c.Request.Method == http.MethodDelete {
		c.Status(204)
		return
	}
	handleBridgeConversationGet(c)
}
func handleBridgeMessages(c *gin.Context) {
	hs := GetHomeServer()
	conn := db.GetDB().WithContext(c.Request.Context())
	conv, member, err := hs.bridgeConversationAccess(conn, c.Param("id"), getUserID(c))
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	var viewer models.Account
	if err := conn.First(&viewer, member.AccountID).Error; err != nil {
		bridgeAPIError(c, err)
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit < 1 || limit > 80 {
		limit = 40
	}
	query := conn.Where("room_id = ? AND event_type = ? AND sender <> ?", conv.RoomID, EventTypeRoomMessage, conv.Controller)
	if before := c.Query("before"); before != "" {
		var e MatrixEvent
		if err := conn.Where("room_id = ? AND event_id = ?", conv.RoomID, before).First(&e).Error; err != nil {
			bridgeAPIError(c, err)
			return
		}
		query = query.Where("origin_server_ts < ? OR (origin_server_ts = ? AND event_id < ?)", e.OriginServerTS, e.OriginServerTS, e.EventID)
	}
	var rows []MatrixEvent
	if err := query.Order("origin_server_ts DESC,event_id DESC").Limit(limit).Find(&rows).Error; err != nil {
		bridgeAPIError(c, err)
		return
	}
	reader, err := newClientHistoryReader(conn, conv.RoomID, getUserID(c))
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	visible, err := reader.filter(rows)
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	// Matrix retains the edited root for native clients. The Community UI uses
	// delete/repost semantics, so never follow a retargeted reference from a
	// superseded root back to the replacement's content.
	eventIDs := make([]string, 0, len(visible))
	for _, e := range visible {
		eventIDs = append(eventIDs, e.EventID)
	}
	var replacedRows []models.MatrixDMDelivery
	if len(eventIDs) > 0 {
		if err := conn.Select("event_id").Where("room_id = ? AND event_id IN ? AND state = ?", conv.RoomID, eventIDs, "replaced").Find(&replacedRows).Error; err != nil {
			bridgeAPIError(c, err)
			return
		}
	}
	replacedIDs := map[string]bool{}
	for _, row := range replacedRows {
		replacedIDs[row.EventID] = true
	}
	result := []gin.H{}
	ownIDs := []string{}
	for _, event := range visible {
		if member.JoinedAt > 0 && (event.CreatedAt == nil || event.CreatedAt.UnixMilli() < member.JoinedAt) {
			continue
		}
		if relation, _ := event.Content["m.relates_to"].(map[string]any); relation["rel_type"] == "m.replace" {
			if edited, ok := event.Content["m.new_content"].(map[string]any); ok {
				event.Content = edited
			}
		}
		body, _ := event.Content["body"].(string)
		item := gin.H{"event_id": event.EventID, "sender": event.Sender, "body": body, "msgtype": event.Content["msgtype"], "created_at": event.OriginServerTS, "own": event.Sender == getUserID(c), "redacted": len(event.Content) == 0, "status_id": nil}
		if replacedIDs[event.EventID] {
			item["body"] = ""
			item["redacted"] = true
			result = append(result, item)
			continue
		}
		if event.Sender == getUserID(c) {
			ownIDs = append(ownIDs, event.EventID)
		}
		if relation, ok := event.Content["m.relates_to"].(map[string]any); ok {
			if reply, ok := relation["m.in_reply_to"].(map[string]any); ok {
				item["reply_event_id"] = reply["event_id"]
			}
		}
		var mapping models.MatrixDMDelivery
		if err := resolveDirectEventStatus(conn, conv.RoomID, event.EventID, &mapping); err == nil {
			var s models.Status
			if err := models.StatusLoadAllQuery(conn).Where("id = ? AND deleted_at IS NULL", mapping.StatusID).First(&s).Error; err == nil {
				allowed, err := s.CanViewEx(conn, &viewer)
				if err != nil {
					bridgeAPIError(c, err)
					return
				}
				if !allowed {
					continue
				}
				item["status_id"] = strconv.FormatUint(s.Id, 10)
				if serialized, err := serializer.ToStatus(&s, &viewer); err == nil {
					item["status"] = serialized
				}
				if mediaURL, ok := event.Content["url"].(string); ok {
					var media MatrixMedia
					mediaID := mediaURL[strings.LastIndex(mediaURL, "/")+1:]
					if conn.Where("media_id = ?", mediaID).First(&media).Error == nil {
						for _, a := range s.Attachments {
							if a.StorageKey() == media.FilePath || (len(s.Attachments) == 1 && event.Sender == getUserID(c)) {
								item["url"] = fmt.Sprintf("%s/media/%d/%s", systemsettings.GetBaseURL(), a.Id, a.FileName)
								break
							}
						}
					}
				}
			}
		}
		result = append(result, item)
	}
	statuses, err := hs.BatchDirectDeliveryStatuses(c.Request.Context(), viewer.Id, conv.RoomID, ownIDs)
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	for _, item := range result {
		if d, ok := statuses[item["event_id"].(string)]; ok {
			item["delivery"] = d
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i]["created_at"].(int64) < result[j]["created_at"].(int64) })
	var next any
	if len(rows) == limit {
		next = rows[len(rows)-1].EventID
	}
	c.JSON(200, gin.H{"messages": result, "next_cursor": next})
}

type bridgeMessageRequest struct {
	TransactionID string   `json:"transaction_id"`
	Body          string   `json:"body"`
	ReplyEventID  string   `json:"reply_event_id"`
	MediaIDs      []string `json:"media_ids"`
}

func handleBridgeMessageSend(c *gin.Context) {
	var req bridgeMessageRequest
	if c.ShouldBindJSON(&req) != nil || req.TransactionID == "" || len(req.TransactionID) > 128 || len(req.Body) > 65536 || len(req.MediaIDs) > 4 {
		c.Status(400)
		return
	}
	hs := GetHomeServer()
	conn := db.GetDB().WithContext(c.Request.Context())
	conv, member, err := hs.bridgeConversationAccess(conn, c.Param("id"), getUserID(c))
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	if strings.TrimSpace(req.Body) == "" && len(req.MediaIDs) == 0 {
		c.Status(400)
		return
	}
	content := map[string]any{"msgtype": "m.text", "body": req.Body}
	if req.ReplyEventID != "" {
		content["m.relates_to"] = map[string]any{"m.in_reply_to": map[string]any{"event_id": req.ReplyEventID}}
	}
	if target := c.Param("eventID"); target != "" {
		// Editing a Matrix attachment preserves its authenticated original MXC,
		// filename/type and reply relation. A body-only form is not a request
		// to silently turn that attachment into a text-only message.
		if len(req.MediaIDs) > 0 {
			c.JSON(422, gin.H{"error": "Replace attachment by deleting and sending a new message"})
			return
		}
		var original MatrixEvent
		if err := conn.Where("event_id = ? AND room_id = ? AND sender = ?", target, conv.RoomID, getUserID(c)).First(&original).Error; err != nil {
			bridgeAPIError(c, err)
			return
		}
		normalized, _, err := hs.normalizeBridgeReplacement(conn, &original)
		if err != nil {
			bridgeAPIError(c, err)
			return
		}
		edited := map[string]any{}
		for k, v := range normalized.Content {
			edited[k] = v
		}
		edited["body"] = req.Body
		delete(edited, "format")
		delete(edited, "formatted_body")
		if req.ReplyEventID != "" {
			edited["m.relates_to"] = map[string]any{"m.in_reply_to": map[string]any{"event_id": req.ReplyEventID}}
		}
		content = map[string]any{"msgtype": edited["msgtype"], "body": "* " + req.Body, "m.new_content": edited, "m.relates_to": map[string]any{"rel_type": "m.replace", "event_id": target}}
	}
	contents := []map[string]any{}
	if req.Body != "" {
		contents = append(contents, content)
	}
	for _, raw := range req.MediaIDs {
		id, e := strconv.ParseUint(raw, 10, 64)
		if e != nil {
			c.Status(400)
			return
		}
		var a models.MediaAttachment
		if err := conn.Where("id = ? AND account_id = ?", id, member.AccountID).First(&a).Error; err != nil {
			bridgeAPIError(c, err)
			return
		}
		hash := sha256.Sum256([]byte(conv.ID + "\x00" + getUserID(c) + "\x00" + req.TransactionID + "\x00" + raw))
		m := MatrixMedia{MediaID: hex.EncodeToString(hash[:]), ServerName: hs.Config.ServerName, UploaderID: getUserID(c), FilePath: a.StorageKey(), FileName: bridgeAttachmentFilename(a), FileSize: a.FileSize, ContentType: a.ContentType, Width: a.Width, Height: a.Height}
		if err := conn.Clauses(clause.OnConflict{DoNothing: true}).Create(&m).Error; err != nil {
			bridgeAPIError(c, err)
			return
		}
		part := bridgeAttachmentContent(hs.Config.ServerName, a, m, a.Sensitive)
		if req.ReplyEventID != "" {
			part["m.relates_to"] = map[string]any{"m.in_reply_to": map[string]any{"event_id": req.ReplyEventID}}
		}
		contents = append(contents, part)
	}
	ids := []string{}
	for i, part := range contents {
		txn := fmt.Sprintf("%s:%d", req.TransactionID, i)
		resp, me := hs.Event.SendRoomEventFromDevice(conv.RoomID, getUserID(c), c.GetString("device_id"), "m.room.message", &txn, part, nil)
		if me != nil {
			bridgeMatrixAPIError(c, me)
			return
		}
		ids = append(ids, resp.EventID)
	}
	c.JSON(200, gin.H{"event_id": ids[0], "event_ids": ids, "delivery": "queued"})
}
func handleBridgeMessageDelete(c *gin.Context) {
	hs := GetHomeServer()
	conv, _, err := hs.bridgeConversationAccess(db.GetDB().WithContext(c.Request.Context()), c.Param("id"), getUserID(c))
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	txn := "delete:" + c.Param("eventID")
	resp, me := hs.Event.RedactEvent(conv.RoomID, c.Param("eventID"), getUserID(c), "", &txn)
	if me != nil {
		bridgeMatrixAPIError(c, me)
		return
	}
	c.JSON(200, resp)
}
func handleBridgeMessageDelivery(c *gin.Context) {
	hs := GetHomeServer()
	conv, member, err := hs.bridgeConversationAccess(db.GetDB().WithContext(c.Request.Context()), c.Param("id"), getUserID(c))
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	var out DirectDeliveryStatus
	if c.Request.Method == http.MethodPost {
		out, err = hs.RetryDirectDelivery(c.Request.Context(), member.AccountID, conv.RoomID, c.Param("eventID"))
	} else {
		out, err = hs.GetDirectDeliveryStatus(c.Request.Context(), member.AccountID, conv.RoomID, c.Param("eventID"))
	}
	if err != nil {
		bridgeAPIError(c, err)
		return
	}
	c.JSON(200, out)
}

func (hs *HomeServer) importLegacyBridgeConversations(ctx context.Context, user string) error {
	conn := db.GetDB().WithContext(ctx)
	account, err := hs.bridgeLocalAccount(conn, user)
	if err != nil {
		return err
	}
	var jobs []models.MatrixDMDelivery
	if err := conn.Joins("JOIN matrix_room_members m ON m.room_id = matrix_dm_deliveries.room_id").Where("m.user_id = ? AND m.membership = ? AND matrix_dm_deliveries.state = ?", user, MembershipJoin, "delivered").Order("matrix_dm_deliveries.status_id DESC").Limit(100).Find(&jobs).Error; err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, job := range jobs {
		if seen[job.RoomID] {
			continue
		}
		seen[job.RoomID] = true
		var count int64
		if err := conn.Model(&BridgeConversation{}).Where("room_id = ?", job.RoomID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		var event MatrixEvent
		if conn.First(&event, "event_id = ?", job.EventID).Error != nil {
			continue
		}
		event.Sender = user
		_, recipients, err := hs.outboundDirectAudience(conn, &MatrixDMOutbound{RoomID: job.RoomID, SourceStatusID: job.StatusID, SenderAccountID: account.Id}, &event)
		if err != nil {
			continue
		}
		var room MatrixRoom
		if conn.First(&room, "room_id = ?", job.RoomID).Error != nil {
			continue
		}
		err = conn.Transaction(func(tx *gorm.DB) error {
			conv := BridgeConversation{ID: uuidForBridgeRoom(job.RoomID), RoomID: job.RoomID, CreatorAccountID: account.Id, Controller: room.Creator, RequestKey: "legacy:" + uuidForBridgeRoom(job.RoomID), State: "active", Revision: 1, Legacy: true, CreatedAt: time.Now()}
			if err := tx.Create(&conv).Error; err != nil {
				return err
			}
			for _, a := range append(recipients, *account) {
				kind := "local"
				if a.Domain != nil {
					kind = "ap"
				}
				if err := tx.Create(&BridgeConversationMember{ConversationID: conv.ID, MatrixID: hs.bridgeAccountID(a), AccountID: a.Id, Kind: kind, JoinedRevision: 1}).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
func uuidForBridgeRoom(room string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(room)).String()
}
