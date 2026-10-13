package homeserver

// Community groups are native Matrix rooms. This adapter deliberately does not
// create ActivityPub statuses, bridge conversations or a parallel member store.
import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/defaultavatar"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

func RegisterCommunityGroupRoutes(parent *gin.RouterGroup) {
	group := parent.Group("", func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		if !ensureWebGroupIdentity(c) {
			c.Abort()
			return
		}
		c.Next()
	})
	group.GET("/groups", handleWebGroupList)
	group.POST("/groups", handleWebGroupCreate)
	group.GET("/groups/:id", handleWebGroupGet)
	group.PATCH("/groups/:id", handleWebGroupPatch)
	group.GET("/groups/:id/members", handleWebGroupMembers)
	group.POST("/groups/:id/members", handleWebGroupMemberAction)
	group.POST("/groups/:id/permissions", handleWebGroupPermissions)
	group.POST("/groups/:id/leave", handleWebGroupLeave)
	group.POST("/groups/:id/read", handleWebGroupRead)
	group.POST("/groups/:id/typing", handleWebGroupTyping)
	group.POST("/groups/:id/preferences", handleWebGroupPreferences)
	registerWebGroupSearchRoutes(group)
	registerWebGroupMessageRoutes(group)
	registerWebGroupMediaRoutes(group)
}

// Native homeserver accounts are Community accounts: there is no second user
// registry to provision or token to mint. Check this authority rather than only
// constructing an MXID, so suspended/remote accounts can never acquire a group
// identity. Matrix clients authenticate against the same account rows.
func ensureWebGroupIdentity(c *gin.Context) bool {
	if v, exists := c.Get("web_group_identity"); exists {
		return v == true
	}
	hs := GetHomeServer()
	if hs == nil || hs.Config == nil || !hs.Config.Enabled || hs.Room == nil || hs.Event == nil {
		webGroupFailure(c, 503, "unavailable", "群聊服务暂不可用")
		return false
	}
	local, server, valid := ParseMatrixUserID(getUserID(c))
	if !valid || server != hs.Config.ServerName {
		webGroupFailure(c, 403, "local_account_required", "请使用本站账号登录")
		return false
	}
	var account models.Account
	if err := db.GetDB().WithContext(c.Request.Context()).Where("username = ? AND domain IS NULL", local).First(&account).Error; err != nil || account.IsBan || account.UserId == 0 {
		webGroupFailure(c, 403, "local_account_required", "请使用有效的本站账号登录")
		return false
	}
	var user models.User
	if err := db.GetDB().WithContext(c.Request.Context()).Where("id = ?", account.UserId).First(&user).Error; err != nil || user.IsBan || user.Status != models.UserStatusNormal {
		webGroupFailure(c, 403, "local_account_required", "账号目前不可用")
		return false
	}
	c.Set("web_group_identity", true)
	c.Set("web_group_account", &account)
	return true
}

func webGroupID(roomID string) string { return base64.RawURLEncoding.EncodeToString([]byte(roomID)) }
func webGroupRoomID(id string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(raw) < 2 || len(raw) > 255 || raw[0] != '!' || !utf8.Valid(raw) || strings.ContainsAny(string(raw), " \t\r\n/\\") {
		return "", errors.New("invalid group id")
	}
	return string(raw), nil
}
func webGroupFailure(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": message, "code": code, "retryable": status >= 500 || status == 429})
}

// Keep database diagnostics in the existing request-ID-correlated server log,
// while returning the same safe, stable error contract to the web client.
func webGroupInternalError(c *gin.Context, operation string, err error) {
	_ = c.Error(fmt.Errorf("web groups %s: %w", operation, err))
	webGroupError(c, ErrInternal)
}

func webGroupError(c *gin.Context, err *MatrixError) {
	if err == nil {
		return
	}
	status, code, message := 500, "unavailable", "群聊操作暂时失败，请稍后重试"
	switch err.ErrCode {
	case "M_FORBIDDEN", "M_UNAUTHORIZED":
		status, code, message = 403, "forbidden", "当前权限不允许此操作"
	case "M_NOT_FOUND":
		status, code, message = 404, "not_found", "群组或内容不存在"
	case "M_UNABLE_TO_AUTHORISE_JOIN", "M_UNABLE_TO_GRANT_JOIN":
		status, code, message = 403, "join_requirements", "此群限制加入资格，请先满足群组要求，或联系管理员邀请"
	case "M_UNSUPPORTED_ROOM_VERSION":
		status, code, message = 409, "unsupported_room_version", "此群使用暂不支持的房间版本，请使用其他 Matrix 客户端或服务器"
	case "M_BAD_GATEWAY", "M_UNAVAILABLE":
		status, code, message = 502, "remote_unavailable", "对方服务器暂时不可用，请稍后重试"
	case "M_INVALID_PARAM", "M_BAD_JSON":
		status, code, message = 400, "invalid_request", "请检查填写内容"
	case "M_CONFLICT":
		status, code, message = 409, "conflict", "此操作与当前状态冲突，请刷新后重试"
	case "M_LIMIT_EXCEEDED":
		status, code, message = 429, "rate_limited", "操作过于频繁，请稍后重试"
	case "M_GROUP_ENCRYPTED":
		status, code, message = 409, "encrypted", "此群已启用端到端加密，请使用 Matrix 客户端"
	case "M_GROUP_UNKNOWN":
		status, code, message = 409, "state_unknown", "正在确认群组状态，暂时无法发送"
	case "M_GROUP_UPGRADED":
		status, code, message = 409, "room_upgraded", "此群已升级，请打开替代群组"
	case "M_GROUP_SPACE":
		status, code, message = 409, "space_unsupported", "此链接是空间目录，请使用 Matrix 客户端"
	}
	webGroupFailure(c, status, code, message)
}

// Only explicitly known local bridges are excluded. Remote users and remote
// appservices are ordinary Matrix participants; do not infer their origin.
func webGroupRoomAllowed(conn *gorm.DB, room *MatrixRoom) (bool, error) {
	if room.IsDirect || room.ConversationID != nil {
		return false, nil
	}
	var count int64
	if err := conn.Model(&BridgeConversation{}).Where("room_id = ?", room.RoomID).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	if err := conn.Model(&models.MatrixDMDelivery{}).Where("room_id = ?", room.RoomID).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}
func webGroupRoomQuery(conn *gorm.DB) *gorm.DB {
	return conn.Model(&MatrixRoom{}).Where("matrix_rooms_hs.is_direct = ? AND matrix_rooms_hs.conversation_id IS NULL", false).
		Where("NOT EXISTS (SELECT 1 FROM matrix_bridge_conversations bc WHERE bc.room_id = matrix_rooms_hs.room_id)").
		Where("NOT EXISTS (SELECT 1 FROM matrix_dm_deliveries dm WHERE dm.room_id = matrix_rooms_hs.room_id)")
}
func webGroupAccess(c *gin.Context, requireJoined bool) (*HomeServer, *MatrixRoom, string, bool) {
	if !ensureWebGroupIdentity(c) {
		return nil, nil, "", false
	}
	hs, user := GetHomeServer(), getUserID(c)
	roomID, err := webGroupRoomID(c.Param("id"))
	if err != nil {
		webGroupFailure(c, 404, "not_found", "群组不存在")
		return nil, nil, "", false
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var room MatrixRoom
	if err = conn.Where("room_id = ?", roomID).First(&room).Error; err != nil {
		webGroupError(c, ErrNotFound)
		return nil, nil, "", false
	}
	allowed, err := webGroupRoomAllowed(conn, &room)
	if err != nil {
		webGroupError(c, ErrInternal)
		return nil, nil, "", false
	}
	if !allowed {
		webGroupError(c, ErrNotFound)
		return nil, nil, "", false
	}
	var member MatrixRoomMember
	if err = conn.Where("room_id = ? AND user_id = ?", roomID, user).First(&member).Error; err != nil || member.Forgotten || (requireJoined && member.Membership != MembershipJoin) {
		webGroupError(c, ErrForbidden)
		return nil, nil, "", false
	}
	return hs, &room, user, true
}

func webGroupPlaintext(hs *HomeServer, room *MatrixRoom) *MatrixError {
	states, err := webGroupStates(db.GetDB(), []string{room.RoomID})
	if err != nil {
		return ErrInternal
	}
	return webGroupPlaintextState(room, states[room.RoomID])
}
func webGroupPlaintextState(room *MatrixRoom, state map[EventType]MatrixRoomState) *MatrixError {
	if _, _, partial := parsePartialStateMarker(room.CurrentStateID); partial {
		return &MatrixError{ErrCode: "M_GROUP_UNKNOWN"}
	}
	if room.RoomType != nil && *room.RoomType == "m.space" {
		return &MatrixError{ErrCode: "M_GROUP_SPACE"}
	}
	if value, _ := state[EventTypeRoomCreate].Content["type"].(string); value == "m.space" {
		return &MatrixError{ErrCode: "M_GROUP_SPACE"}
	}
	if _, ok := state["m.room.tombstone"]; ok {
		return &MatrixError{ErrCode: "M_GROUP_UPGRADED"}
	}
	if room.IsEncrypted {
		return &MatrixError{ErrCode: "M_GROUP_ENCRYPTED"}
	}
	if _, ok := state[EventTypeRoomEncryption]; ok {
		return &MatrixError{ErrCode: "M_GROUP_ENCRYPTED"}
	}
	if _, ok := state[EventTypeRoomCreate]; !ok {
		return &MatrixError{ErrCode: "M_GROUP_UNKNOWN"}
	}
	return nil
}
func webGroupCanWrite(hs *HomeServer, room *MatrixRoom, user string) *MatrixError {
	if err := webGroupPlaintext(hs, room); err != nil {
		return err
	}
	membership, err := hs.Room.GetUserMembership(room.RoomID, user)
	if err != nil || membership != MembershipJoin || !hs.Event.canUserSendEvent(room.RoomID, user, EventTypeRoomMessage) {
		return ErrForbidden
	}
	return nil
}

func webGroupStates(conn *gorm.DB, roomIDs []string) (map[string]map[EventType]MatrixRoomState, error) {
	out := map[string]map[EventType]MatrixRoomState{}
	if len(roomIDs) == 0 {
		return out, nil
	}
	var states []MatrixRoomState
	if err := conn.Where("room_id IN ? AND state_key = ? AND event_type IN ?", roomIDs, "", []EventType{EventTypeRoomCreate, EventTypeRoomEncryption, EventTypeRoomPowerLevels, "m.room.tombstone"}).Find(&states).Error; err != nil {
		return nil, err
	}
	for _, s := range states {
		if out[s.RoomID] == nil {
			out[s.RoomID] = map[EventType]MatrixRoomState{}
		}
		out[s.RoomID][s.EventType] = s
	}
	return out, nil
}
func webGroupLevels(room *MatrixRoom, state map[EventType]MatrixRoomState) *PowerLevelContent {
	pl := &PowerLevelContent{Users: map[string]int64{}, Events: map[string]int64{}, StateDefault: 50, Ban: 50, Kick: 50, Redact: 50}
	if b, err := json.Marshal(state[EventTypeRoomPowerLevels].Content); err == nil {
		_ = json.Unmarshal(b, pl)
	}
	if room.RoomVersion == "12" {
		pl.privilegedCreators = map[string]struct{}{room.Creator: {}}
		// The create event, not the mutable power level map, owns v12 creators.
		if creators, ok := state[EventTypeRoomCreate].Content["additional_creators"].([]any); ok {
			for _, v := range creators {
				if id, ok := v.(string); ok {
					pl.privilegedCreators[id] = struct{}{}
				}
			}
		}
	}
	return pl
}
func webGroupCapabilities(hs *HomeServer, room *MatrixRoom, member MembershipState, state map[EventType]MatrixRoomState, user string) gin.H {
	caps := gin.H{"send_message": false, "invite": false, "kick": false, "ban": false, "redact": false, "set_name": false, "set_topic": false, "set_avatar": false, "manage_permissions": false}
	if _, _, partial := parsePartialStateMarker(room.CurrentStateID); partial {
		return caps
	}
	if member != MembershipJoin {
		return caps
	}
	if _, ok := state[EventTypeRoomCreate]; !ok {
		return caps
	}
	if webGroupPlaintextState(room, state) != nil {
		return caps
	}
	pl := webGroupLevels(room, state)
	level := hs.Room.getUserPowerLevel(pl, user)
	threshold := func(kind string, isState bool) int64 {
		if n, ok := pl.Events[kind]; ok {
			return n
		}
		if isState {
			return pl.StateDefault
		}
		return pl.EventsDefault
	}
	caps["send_message"] = webGroupPlaintextState(room, state) == nil && level >= threshold("m.room.message", false)
	caps["invite"] = level >= pl.Invite
	caps["kick"] = level >= pl.Kick
	caps["ban"] = level >= pl.Ban
	caps["redact"] = level >= pl.Redact
	for key, event := range map[string]string{"set_name": "m.room.name", "set_topic": "m.room.topic", "set_avatar": "m.room.avatar", "manage_permissions": "m.room.power_levels"} {
		caps[key] = level >= threshold(event, true)
	}
	return caps
}

func webGroupDTO(hs *HomeServer, conn *gorm.DB, room *MatrixRoom, user string) (gin.H, error) {
	views, err := webGroupDTOs(hs, conn, []MatrixRoom{*room}, user)
	if err != nil {
		return nil, err
	}
	return views[0], nil
}
func webGroupDTOs(hs *HomeServer, conn *gorm.DB, rooms []MatrixRoom, user string) ([]gin.H, error) {
	result := make([]gin.H, 0, len(rooms))
	if len(rooms) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(rooms))
	for _, r := range rooms {
		ids = append(ids, r.RoomID)
	}
	states, err := webGroupStates(conn, ids)
	if err != nil {
		return nil, fmt.Errorf("load room state: %w", err)
	}
	var members []MatrixRoomMember
	if err = conn.Where("room_id IN ? AND user_id = ?", ids, user).Find(&members).Error; err != nil {
		return nil, fmt.Errorf("load memberships: %w", err)
	}
	memberships := map[string]MembershipState{}
	for _, m := range members {
		memberships[m.RoomID] = m.Membership
	}
	var counts []struct {
		RoomID string
		Count  int64
	}
	if err = conn.Model(&MatrixRoomMember{}).Select("room_id, COUNT(*) AS count").Where("room_id IN ? AND membership = ?", ids, MembershipJoin).Group("room_id").Scan(&counts).Error; err != nil {
		return nil, fmt.Errorf("count members: %w", err)
	}
	totals := map[string]int64{}
	for _, r := range counts {
		totals[r.RoomID] = r.Count
	}
	var mutedRules []PushRule
	if err = conn.Where("user_id = ? AND scope = ? AND kind IN ? AND rule_id IN ? AND enabled = ?", user, "global", []string{"room", "override"}, ids, true).Find(&mutedRules).Error; err != nil {
		return nil, fmt.Errorf("load notification preferences: %w", err)
	}
	muted := map[string]bool{}
	for _, r := range mutedRules {
		var acts []any
		if json.Unmarshal([]byte(r.Actions), &acts) == nil {
			for _, a := range acts {
				if a == "dont_notify" {
					muted[r.RuleID] = true
				}
			}
		}
	}
	var typing []MatrixTyping
	if err = conn.Where("room_id IN ? AND expires_at > ? AND user_id <> ?", ids, time.Now(), user).Find(&typing).Error; err != nil {
		return nil, fmt.Errorf("load typing state: %w", err)
	}
	typingByRoom := map[string][]gin.H{}
	for _, t := range typing {
		typingByRoom[t.RoomID] = append(typingByRoom[t.RoomID], gin.H{"id": t.UserID, "display_name": t.UserID})
	}
	unread, err := webGroupUnreadCounts(conn, ids, user)
	if err != nil {
		return nil, fmt.Errorf("count unread messages: %w", err)
	}

	for _, room := range rooms {
		state := states[room.RoomID]
		membership := memberships[room.RoomID]
		if membership == "" {
			membership = MembershipLeave
		}
		// A historical reader may retain access to old messages, but that does not
		// grant ongoing access to private room metadata after departure.
		if room.Visibility != "public" && membership != MembershipJoin && membership != MembershipInvite && membership != MembershipKnock {
			result = append(result, gin.H{"id": webGroupID(room.RoomID), "name": "已离开的群组", "topic": "", "membership": membership, "encryption": "unknown", "join_rule": "invite", "listed": false, "member_count": 0, "unread_count": 0, "client_url": "https://matrix.to/#/" + url.PathEscape(room.RoomID), "capabilities": webGroupCapabilities(hs, &room, membership, nil, user), "current_user_id": user, "typing_users": []gin.H{}, "muted": false})
			continue
		}
		encryption := "unknown"
		if room.IsEncrypted {
			encryption = "encrypted"
		} else if _, ok := state[EventTypeRoomEncryption]; ok {
			encryption = "encrypted"
		} else if membership == MembershipJoin {
			_, _, partial := parsePartialStateMarker(room.CurrentStateID)
			if _, ok := state[EventTypeRoomCreate]; ok && !partial {
				encryption = "unencrypted"
			}
		}
		name := "未命名群组"
		if room.Name != nil && strings.TrimSpace(*room.Name) != "" {
			name = *room.Name
		}
		topic := ""
		if room.Topic != nil {
			topic = *room.Topic
		}
		avatar := defaultavatar.Path(defaultavatar.Seed(room.RoomID, "group"))
		if room.AvatarURL != nil && strings.HasPrefix(*room.AvatarURL, "mxc://") {
			avatar = "/api/v1/chat/groups/" + webGroupID(room.RoomID) + "/avatar"
		}
		users := typingByRoom[room.RoomID]
		if users == nil {
			users = []gin.H{}
		}
		out := gin.H{"id": webGroupID(room.RoomID), "name": name, "topic": topic, "avatar_url": avatar, "membership": membership, "encryption": encryption, "join_rule": room.JoinRule, "listed": room.Visibility == "public", "member_count": totals[room.RoomID], "unread_count": unread[room.RoomID], "client_url": "https://matrix.to/#/" + url.PathEscape(room.RoomID) + "?via=" + url.QueryEscape(hs.Config.ServerName), "capabilities": webGroupCapabilities(hs, &room, membership, state, user), "muted": muted[room.RoomID], "typing_users": users, "current_user_id": user, "limits": gin.H{"max_upload_bytes": webGroupUploadMaxBytes(), "max_attachments": 8}}
		if replacement, ok := state["m.room.tombstone"].Content["replacement_room"].(string); ok && replacement != "" {
			out["replacement_id"] = webGroupID(replacement)
		}
		if room.RoomType != nil {
			out["room_type"] = *room.RoomType
		}
		result = append(result, out)
	}
	return result, nil
}

func webGroupLimit(c *gin.Context, defaultLimit int) int {
	n, err := strconv.Atoi(c.Query("limit"))
	if err != nil || n < 1 {
		return defaultLimit
	}
	return min(n, 100)
}
func handleWebGroupList(c *gin.Context) {
	conn := db.GetDB().WithContext(c.Request.Context())
	user := getUserID(c)
	limit := webGroupLimit(c, 40)
	query := webGroupRoomQuery(conn).Select("matrix_rooms_hs.*").Joins("JOIN matrix_room_members m ON m.room_id = matrix_rooms_hs.room_id").Where("m.user_id = ? AND m.membership IN ? AND m.forgotten = ?", user, []MembershipState{MembershipJoin, MembershipInvite, MembershipKnock}, false)
	if cursor := c.Query("cursor"); cursor != "" {
		id, err := webGroupRoomID(cursor)
		if err != nil {
			webGroupFailure(c, 400, "invalid_cursor", "无效的分页位置")
			return
		}
		query = query.Where("matrix_rooms_hs.room_id > ?", id)
	}
	var rooms []MatrixRoom
	if err := query.Order("matrix_rooms_hs.room_id ASC").Limit(limit + 1).Find(&rooms).Error; err != nil {
		webGroupInternalError(c, "list rooms", err)
		return
	}
	var next any
	if len(rooms) > limit {
		rooms = rooms[:limit]
		next = webGroupID(rooms[len(rooms)-1].RoomID)
	}
	views, err := webGroupDTOs(GetHomeServer(), conn, rooms, user)
	if err != nil {
		webGroupInternalError(c, "list room details", err)
		return
	}
	c.JSON(200, gin.H{"groups": views, "next_cursor": next})
}
func handleWebGroupGet(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, false)
	if !ok {
		return
	}
	membership, err := hs.Room.GetUserMembership(room.RoomID, user)
	if err != nil || (membership != MembershipJoin && membership != MembershipInvite && membership != MembershipKnock) {
		webGroupError(c, ErrForbidden)
		return
	}
	out, err := webGroupDTO(hs, db.GetDB().WithContext(c.Request.Context()), room, user)
	if err != nil {
		webGroupInternalError(c, "read room details", err)
		return
	}
	c.JSON(200, out)
}

// Input accepts an explicit Matrix ID or a local Community username. A
// user@domain ActivityPub handle is never silently converted to another identity.
func webGroupInvitee(hs *HomeServer, conn *gorm.DB, input string) (string, *MatrixError) {
	value := strings.TrimSpace(input)
	if len(value) == 0 || len(value) > 255 || strings.ContainsAny(value, " \t\r\n") {
		return "", ErrInvalidParam
	}
	if !strings.Contains(value, ":") {
		value = strings.TrimPrefix(value, "@")
		if strings.Contains(value, "@") {
			return "", ErrInvalidParam
		}
		value = FormatMatrixUserID(value, hs.Config.ServerName)
	}
	local, server, ok := ParseMatrixUserID(value)
	if !ok || local == "" || !usableServerName(server) {
		return "", ErrInvalidParam
	}
	if server != hs.Config.ServerName {
		return value, nil
	}
	var account models.Account
	err := conn.Where("username = ? AND domain IS NULL", local).First(&account).Error
	if err != nil || account.UserId == 0 || account.IsBan {
		return "", ErrForbidden
	}
	var user models.User
	if err = conn.Where("id = ?", account.UserId).First(&user).Error; err != nil || user.IsBan || user.Status != models.UserStatusNormal {
		return "", ErrForbidden
	}
	return value, nil
}

func handleWebGroupCreate(c *gin.Context) {
	var req struct {
		TransactionID    string   `json:"transaction_id"`
		Name             string   `json:"name"`
		Topic            string   `json:"topic"`
		Visibility       string   `json:"visibility"`
		Invite           []string `json:"invite"`
		PlaintextConsent bool     `json:"plaintext_consent"`
	}
	if c.ShouldBindJSON(&req) != nil || !req.PlaintextConsent || strings.TrimSpace(req.Name) == "" || utf8.RuneCountInString(req.Name) > 255 || utf8.RuneCountInString(req.Topic) > 512 || len(req.TransactionID) < 8 || len(req.TransactionID) > 128 || len(req.Invite) > 50 || (req.Visibility != "public" && req.Visibility != "private") {
		webGroupFailure(c, 400, "invalid_request", "请填写群名称、加入方式，并确认此群不使用端到端加密")
		return
	}
	hs, user, conn := GetHomeServer(), getUserID(c), db.GetDB().WithContext(c.Request.Context())
	invites := []string{}
	seen := map[string]bool{user: true}
	for _, input := range req.Invite {
		id, me := webGroupInvitee(hs, conn, input)
		if me != nil {
			webGroupError(c, me)
			return
		}
		if !seen[id] {
			seen[id] = true
			invites = append(invites, id)
		}
	}
	// Stable room ID is the creation idempotency record. Metadata is room state,
	// not another room database. Concurrent requests converge on the primary key.
	key := sha256.Sum256([]byte(user + "\x00" + req.TransactionID))
	roomID := "!web_" + hex.EncodeToString(key[:]) + ":" + hs.Config.ServerName
	payload, _ := json.Marshal(req)
	digest := sha256.Sum256(payload)
	fingerprint := hex.EncodeToString(digest[:])
	var existing MatrixRoom
	if err := conn.Where("room_id = ?", roomID).First(&existing).Error; err == nil {
		if !webGroupCreateMatches(conn, &existing, user, fingerprint) {
			webGroupFailure(c, 409, "transaction_conflict", "此请求编号已用于不同的创建操作")
			return
		}
		webGroupCreateReply(c, hs, &existing, user, invites, req.TransactionID)
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		webGroupError(c, ErrInternal)
		return
	}
	created, me := hs.Room.CreateRoom(user, &CreateRoomRequest{bridgeRoomID: roomID, Visibility: req.Visibility, Name: &req.Name, Topic: &req.Topic, InitialState: []CreateRoomStateEvent{{Type: "m.room.history_visibility", StateKey: "", Content: map[string]any{"history_visibility": "joined"}}, {Type: "m.room.guest_access", StateKey: "", Content: map[string]any{"guest_access": "forbidden"}}, {Type: "org.community.web_group", StateKey: "", Content: map[string]any{"request_hash": fingerprint}}}})
	if me != nil {
		if conn.Where("room_id = ?", roomID).First(&existing).Error != nil || !webGroupCreateMatches(conn, &existing, user, fingerprint) {
			webGroupError(c, me)
			return
		}
	} else {
		roomID = created.RoomID
	}
	// Use the same local profile clients see. Profile conversion caches Matrix
	// media and never creates an AP post or conversation.
	if raw, ok := c.Get("web_group_account"); ok {
		if a, ok := raw.(*models.Account); ok {
			display := a.DisplayName
			if display == "" {
				display = a.Username
			}
			avatar := hs.bridgeCompatibleProfileAvatar(c.Request.Context(), user, *a)
			_, _ = hs.Event.SendRoomEvent(roomID, user, "m.room.member", nil, map[string]any{"membership": "join", "displayname": display, "avatar_url": avatar}, &user)
		}
	}
	if err := conn.Where("room_id = ?", roomID).First(&existing).Error; err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	webGroupCreateReply(c, hs, &existing, user, invites, req.TransactionID)
}
func webGroupCreateReply(c *gin.Context, hs *HomeServer, room *MatrixRoom, user string, invites []string, transaction string) {
	membership, err := hs.Room.GetUserMembership(room.RoomID, user)
	if err != nil || membership != MembershipJoin {
		c.JSON(409, gin.H{"error": "此群已创建，但你已不在群内，请通过群链接重新加入", "code": "created_group_unavailable", "group_id": webGroupID(room.RoomID)})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
	defer cancel()
	failures := []gin.H{}
	for _, target := range invites {
		if err := hs.inviteWebGroupUser(ctx, room.RoomID, user, target, "", "create:"+transaction); err != nil {
			failures = append(failures, gin.H{"user_id": target, "error": "邀请暂未完成，请重试", "retryable": true})
		}
	}
	out, err := webGroupDTO(hs, db.GetDB().WithContext(c.Request.Context()), room, user)
	if err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	out["invite_failures"] = failures
	c.JSON(200, out)
}
func webGroupCreateMatches(conn *gorm.DB, room *MatrixRoom, user, hash string) bool {
	var state MatrixRoomState
	return room.Creator == user && conn.Where("room_id = ? AND event_type = ? AND state_key = ?", room.RoomID, "org.community.web_group", "").First(&state).Error == nil && state.Content["request_hash"] == hash
}
