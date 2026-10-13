package homeserver

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
)

func handleWebGroupPatch(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	var req struct {
		Name           *string `json:"name"`
		Topic          *string `json:"topic"`
		AvatarUploadID *string `json:"avatar_upload_id"`
	}
	if c.ShouldBindJSON(&req) != nil || (req.Name != nil && (strings.TrimSpace(*req.Name) == "" || utf8.RuneCountInString(*req.Name) > 255)) || (req.Topic != nil && utf8.RuneCountInString(*req.Topic) > 512) || (req.Name == nil && req.Topic == nil && req.AvatarUploadID == nil) {
		webGroupError(c, ErrInvalidParam)
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	type change struct {
		kind    string
		content map[string]any
		upload  string
	}
	changes := []change{}
	if req.Name != nil {
		changes = append(changes, change{kind: "m.room.name", content: map[string]any{"name": *req.Name}})
	}
	if req.Topic != nil {
		changes = append(changes, change{kind: "m.room.topic", content: map[string]any{"topic": *req.Topic}})
	}
	if req.AvatarUploadID != nil {
		content, err := webGroupUploadContent(conn, room.RoomID, user, *req.AvatarUploadID)
		if err != nil {
			webGroupError(c, err)
			return
		}
		if content["msgtype"] != "m.image" {
			webGroupFailure(c, 400, "invalid_image", "群头像需要图片文件")
			return
		}
		changes = append(changes, change{kind: "m.room.avatar", content: map[string]any{"url": content["url"], "info": content["info"]}, upload: *req.AvatarUploadID})
	}
	// Check the whole request before the first write. Event authorization is
	// repeated at commit; a later race still reports which fields already saved.
	for _, ch := range changes {
		if !hs.Event.canUserSendStateEvent(room.RoomID, user, EventType(ch.kind)) {
			webGroupError(c, ErrForbidden)
			return
		}
	}
	applied := []string{}
	for _, ch := range changes {
		_, err := webGroupPersistState(c.Request.Context(), hs, room.RoomID, user, ch.kind, "", ch.upload, func(_ *gorm.DB, _ *MatrixRoom, _ map[EventType]MatrixRoomState) (map[string]any, *MatrixError) {
			return ch.content, nil
		})
		if err != nil {
			if len(applied) > 0 {
				c.JSON(409, gin.H{"error": "部分设置已保存，请刷新后重试剩余项目", "code": "partial_update", "applied": applied, "retryable": true})
			} else {
				webGroupError(c, err)
			}
			return
		}
		applied = append(applied, ch.kind)

	}
	handleWebGroupGet(c)
}

func handleWebGroupMembers(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	limit := webGroupLimit(c, 50)
	query := conn.Where("room_id = ? AND membership IN ?", room.RoomID, []MembershipState{MembershipJoin, MembershipInvite, MembershipKnock, MembershipBan})
	if cursor := c.Query("cursor"); cursor != "" {
		n, err := strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			webGroupFailure(c, 400, "invalid_cursor", "无效的分页位置")
			return
		}
		query = query.Where("id > ?", n)
	}
	var members []MatrixRoomMember
	if err := query.Order("id ASC").Limit(limit + 1).Find(&members).Error; err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	var next any
	if len(members) > limit {
		members = members[:limit]
		next = strconv.FormatUint(members[len(members)-1].ID, 10)
	}
	states, err := webGroupStates(conn, []string{room.RoomID})
	if err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	pl := webGroupLevels(room, states[room.RoomID])
	profiles, err := webGroupMemberProfiles(hs, conn, room.RoomID, members)
	if err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	result := make([]gin.H, 0, len(members))
	for _, m := range members {
		profile := profiles[m.UserID]
		name, avatar := profile.DisplayName, profile.AvatarURL
		_, privileged := pl.privilegedCreators[m.UserID]
		result = append(result, gin.H{"id": m.UserID, "display_name": name, "avatar_url": avatar, "membership": m.Membership, "power_level": hs.Room.getUserPowerLevel(pl, m.UserID), "own": m.UserID == user, "privileged_creator": privileged})
	}
	c.JSON(200, gin.H{"members": result, "next_cursor": next})
}

func handleWebGroupMemberAction(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if c.ShouldBindJSON(&req) != nil || len(req.Reason) > 512 {
		webGroupError(c, ErrInvalidParam)
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var err *MatrixError
	switch req.Action {
	case "invite", "approve":
		target, e := webGroupInvitee(hs, conn, req.UserID)
		if e != nil {
			webGroupError(c, e)
			return
		}
		if req.Action == "approve" {
			membership, _ := hs.Room.GetUserMembership(room.RoomID, target)
			if membership != MembershipKnock {
				webGroupFailure(c, 409, "not_pending", "此成员没有待处理的加入申请")
				return
			}
		}
		err = hs.inviteWebGroupUser(c.Request.Context(), room.RoomID, user, target, req.Reason, "")
	case "kick", "ban", "unban":
		_, server, valid := ParseMatrixUserID(req.UserID)
		if !valid || !usableServerName(server) || req.UserID == user {
			webGroupError(c, ErrInvalidParam)
			return
		}
		_, err = webGroupPersistState(c.Request.Context(), hs, room.RoomID, user, "m.room.member", req.UserID, "", func(tx *gorm.DB, locked *MatrixRoom, state map[EventType]MatrixRoomState) (map[string]any, *MatrixError) {
			var target MatrixRoomMember
			if tx.Where("room_id = ? AND user_id = ?", locked.RoomID, req.UserID).Take(&target).Error != nil {
				return nil, ErrNotFound
			}
			if req.Action == "kick" && target.Membership != MembershipJoin && target.Membership != MembershipInvite && target.Membership != MembershipKnock {
				return nil, ErrForbidden
			}
			if req.Action == "unban" && target.Membership != MembershipBan {
				return nil, ErrForbidden
			}
			membership := "leave"
			if req.Action == "ban" {
				membership = "ban"
			}
			return map[string]any{"membership": membership, "reason": req.Reason}, nil
		})
	default:
		webGroupError(c, ErrInvalidParam)
		return
	}
	if err != nil {
		webGroupError(c, err)
		return
	}
	handleWebGroupGet(c)
}

func handleWebGroupPermissions(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	var req struct {
		UserID     string `json:"user_id"`
		PowerLevel *int64 `json:"power_level"`
	}
	if c.ShouldBindJSON(&req) != nil || req.PowerLevel == nil || *req.PowerLevel < 0 || *req.PowerLevel > 100 {
		webGroupError(c, ErrInvalidParam)
		return
	}
	_, err := webGroupPersistState(c.Request.Context(), hs, room.RoomID, user, "m.room.power_levels", "", "", func(tx *gorm.DB, locked *MatrixRoom, state map[EventType]MatrixRoomState) (map[string]any, *MatrixError) {
		var target MatrixRoomMember
		if tx.Where("room_id = ? AND user_id = ? AND membership = ?", locked.RoomID, req.UserID, MembershipJoin).Take(&target).Error != nil {
			return nil, ErrForbidden
		}
		levels := webGroupLevels(locked, state)
		selfLevel, targetLevel := hs.Room.getUserPowerLevel(levels, user), hs.Room.getUserPowerLevel(levels, req.UserID)
		_, privileged := levels.privilegedCreators[req.UserID]
		if privileged || *req.PowerLevel > selfLevel || (req.UserID != user && targetLevel >= selfLevel) {
			return nil, ErrForbidden
		}
		content := state[EventTypeRoomPowerLevels].Content
		if content == nil {
			return nil, ErrInternal
		}
		users, ok := content["users"].(map[string]any)
		if !ok {
			users = map[string]any{}
		}
		users[req.UserID] = *req.PowerLevel
		content["users"] = users
		return content, nil
	})
	if err != nil {
		webGroupError(c, err)
		return
	}
	handleWebGroupGet(c)
}
func handleWebGroupLeave(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, false)
	if !ok {
		return
	}
	membership, _ := hs.Room.GetUserMembership(room.RoomID, user)
	if membership == MembershipLeave || membership == MembershipBan {
		c.JSON(200, gin.H{})
		return
	}
	if err := hs.Room.LeaveRoomContext(c.Request.Context(), user, room.RoomID, &LeaveRoomRequest{}); err != nil {
		webGroupError(c, err)
		return
	}
	c.JSON(200, gin.H{})
}
func handleWebGroupRead(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	var req struct {
		EventID string `json:"event_id"`
	}
	if c.ShouldBindJSON(&req) != nil || !canReadClientEventContext(c.Request.Context(), room.RoomID, user, req.EventID) {
		webGroupError(c, ErrForbidden)
		return
	}
	if err := hs.Sync.SetReceipt(room.RoomID, req.EventID, user, "m.read"); err != nil {
		webGroupError(c, err)
		return
	}
	if hs.Account != nil {
		if err := hs.Account.SetAccountData(user, "m.fully_read", &room.RoomID, map[string]any{"event_id": req.EventID}); err != nil {
			webGroupError(c, ErrInternal)
			return
		}
	}
	c.JSON(200, gin.H{})
}
func handleWebGroupTyping(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	var req struct {
		Typing bool `json:"typing"`
	}
	if c.ShouldBindJSON(&req) != nil {
		webGroupError(c, ErrInvalidParam)
		return
	}
	if req.Typing {
		if err := webGroupCanWrite(hs, room, user); err != nil {
			webGroupError(c, err)
			return
		}
	}
	if err := hs.Sync.SetTyping(room.RoomID, user, req.Typing, 25000); err != nil {
		webGroupError(c, err)
		return
	}
	c.JSON(200, gin.H{})
}
func handleWebGroupPreferences(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	var req struct {
		Muted *bool `json:"muted"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Muted == nil {
		webGroupError(c, ErrInvalidParam)
		return
	}
	if hs.Push == nil {
		webGroupError(c, ErrInternal)
		return
	}
	var err error
	if *req.Muted {
		err = hs.Push.SetPushRule(user, "global", "room", room.RoomID, &PushRule{Enabled: true, Actions: `["dont_notify"]`})
	} else {
		err = hs.Push.DeletePushRule(user, "global", "room", room.RoomID)
	}
	if err != nil {
		webGroupError(c, ErrInternal)
		return
	}
	hs.NotifyUser(user)
	c.JSON(200, gin.H{})
}
