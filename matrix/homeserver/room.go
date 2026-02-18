package homeserver

import (
	"strings"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
)

// RoomService 房间服务
type RoomService struct {
	HS *HomeServer
}

// NewRoomService 创建房间服务
func NewRoomService(hs *HomeServer) *RoomService {
	return &RoomService{HS: hs}
}

// ===== 房间创建 =====

// CreateRoomRequest 创建房间请求
type CreateRoomRequest struct {
	Visibility      string                     `json:"visibility"` // public, private
	RoomAliasName   *string                    `json:"room_alias_name"`
	Name            *string                    `json:"name"`
	Topic           *string                    `json:"topic"`
	Invite          []string                   `json:"invite"`
	Invite3PID      []Invite3PID               `json:"invite_3pid"`
	RoomVersion     string                     `json:"room_version"`
	CreationContent map[string]interface{}     `json:"creation_content"`
	InitialState    []CreateRoomStateEvent     `json:"initial_state"`
	Preset          string                     `json:"preset"` // private_chat, trusted_private_chat, public_chat
	IsDirect        bool                       `json:"is_direct"`
	PowerLevelContentOverride *PowerLevelContent `json:"power_level_content_override"`
}

// Invite3PID 第三方邀请
type Invite3PID struct {
	Medium     string `json:"medium"`
	Address    string `json:"address"`
	IDServer   string `json:"id_server"`
	IDAccessToken string `json:"id_access_token"`
}

// CreateRoomStateEvent 初始状态事件
type CreateRoomStateEvent struct {
	Type     string                 `json:"type"`
	StateKey string                 `json:"state_key"`
	Content  map[string]interface{} `json:"content"`
}

// PowerLevelContent 权限级别
type PowerLevelContent struct {
	Users      map[string]int64 `json:"users"`
	UsersDefault int64           `json:"users_default"`
	Events     map[string]int64 `json:"events"`
	EventsDefault int64          `json:"events_default"`
	StateDefault int64           `json:"state_default"`
	Ban        int64            `json:"ban"`
	Kick       int64            `json:"kick"`
	Redact     int64            `json:"redact"`
	Invite     int64            `json:"invite"`
}

// CreateRoomResponse 创建房间响应
type CreateRoomResponse struct {
	RoomID string `json:"room_id"`
}

// CreateRoom 创建房间
func (r *RoomService) CreateRoom(creatorID string, req *CreateRoomRequest) (*CreateRoomResponse, *MatrixError) {
	// 生成房间 ID
	roomID := "!" + uuid.New().String() + ":" + r.HS.Config.ServerName
	
	// 默认房间版本
	roomVersion := req.RoomVersion
	if roomVersion == "" {
		roomVersion = "10"
	}
	
	// 开始事务
	tx := db.GetDB().Begin()
	
	// 创建房间记录
	room := &MatrixRoom{
		RoomID:      roomID,
		RoomVersion: roomVersion,
		Creator:     creatorID,
		IsDirect:    req.IsDirect,
	}
	
	if req.Name != nil {
		room.Name = req.Name
	}
	if req.Topic != nil {
		room.Topic = req.Topic
	}
	
	if err := tx.Create(room).Error; err != nil {
		tx.Rollback()
		return nil, ErrInternal
	}
	
	// 处理预设
	preset := req.Preset
	if preset == "" {
		if req.Visibility == "public" {
			preset = "public_chat"
		} else {
			preset = "private_chat"
		}
	}
	
	// 构建 auth events
	var authEvents []string
	
	// 1. 创建 m.room.create 事件
	createEvent := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomCreate,
		Sender:    creatorID,
		StateKey:  ptrString(""),
		Content: map[string]interface{}{
			"creator":      creatorID,
			"room_version": roomVersion,
		},
		Depth: 1,
	}
	
	// 添加 creation_content
	if req.CreationContent != nil {
		for k, v := range req.CreationContent {
			createEvent.Content[k] = v
		}
	}
	
	if err := tx.Create(createEvent).Error; err != nil {
		tx.Rollback()
		return nil, ErrInternal
	}
	authEvents = append(authEvents, createEvent.EventID)
	
	// 更新状态表
	r.updateRoomState(tx, roomID, createEvent)
	
	// 2. 创建 m.room.power_levels 事件
	plContent := r.getDefaultPowerLevels()
	if req.PowerLevelContentOverride != nil {
		plContent = req.PowerLevelContentOverride
	}
	if plContent.Users == nil {
		plContent.Users = make(map[string]int64)
	}
	plContent.Users[creatorID] = 100 // 创建者拥有最高权限
	
	plEvent := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomPowerLevels,
		Sender:    creatorID,
		StateKey:  ptrString(""),
		Content: map[string]interface{}{
			"users":          plContent.Users,
			"users_default":  plContent.UsersDefault,
			"events":         plContent.Events,
			"events_default": plContent.EventsDefault,
			"state_default":  plContent.StateDefault,
			"ban":            plContent.Ban,
			"kick":           plContent.Kick,
			"redact":         plContent.Redact,
			"invite":         plContent.Invite,
		},
		Depth:     2,
	}
	if err := tx.Create(plEvent).Error; err != nil {
		tx.Rollback()
		return nil, ErrInternal
	}
	authEvents = append(authEvents, plEvent.EventID)
	r.updateRoomState(tx, roomID, plEvent)
	
	// 3. 根据 preset 设置其他默认事件
	switch preset {
	case "private_chat":
		r.createDefaultEvent(tx, roomID, creatorID, EventTypeRoomJoinRules, map[string]interface{}{
			"join_rule": "invite",
		}, &authEvents)
		r.createDefaultEvent(tx, roomID, creatorID, EventTypeRoomHistoryVisibility, map[string]interface{}{
			"history_visibility": "shared",
		}, nil)
		r.createDefaultEvent(tx, roomID, creatorID, EventTypeRoomGuestAccess, map[string]interface{}{
			"guest_access": "forbidden",
		}, nil)
	case "trusted_private_chat":
		r.createDefaultEvent(tx, roomID, creatorID, EventTypeRoomJoinRules, map[string]interface{}{
			"join_rule": "invite",
		}, &authEvents)
		r.createDefaultEvent(tx, roomID, creatorID, EventTypeRoomHistoryVisibility, map[string]interface{}{
			"history_visibility": "shared",
		}, nil)
		r.createDefaultEvent(tx, roomID, creatorID, EventTypeRoomGuestAccess, map[string]interface{}{
			"guest_access": "forbidden",
		}, nil)
	case "public_chat":
		r.createDefaultEvent(tx, roomID, creatorID, EventTypeRoomJoinRules, map[string]interface{}{
			"join_rule": "public",
		}, &authEvents)
		r.createDefaultEvent(tx, roomID, creatorID, EventTypeRoomHistoryVisibility, map[string]interface{}{
			"history_visibility": "shared",
		}, nil)
		r.createDefaultEvent(tx, roomID, creatorID, EventTypeRoomGuestAccess, map[string]interface{}{
			"guest_access": "can_join",
		}, nil)
	}
	
	// 4. 处理 initial_state
	for _, state := range req.InitialState {
		event := &MatrixEvent{
			EventID:   "$" + uuid.New().String(),
			RoomID:    roomID,
			EventType: EventType(state.Type),
			Sender:    creatorID,
			StateKey:  &state.StateKey,
			Content:   state.Content,
			Depth:     r.getNextDepth(tx, roomID),
		}
		if err := tx.Create(event).Error; err != nil {
			continue // 忽略错误
		}
		r.updateRoomState(tx, roomID, event)
	}
	
	// 5. 创建者加入房间
	memberEvent := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomMember,
		Sender:    creatorID,
		StateKey:  ptrString(creatorID),
		Content: map[string]interface{}{
			"membership": "join",
		},
		Depth: r.getNextDepth(tx, roomID),
	}
	if err := tx.Create(memberEvent).Error; err != nil {
		tx.Rollback()
		return nil, ErrInternal
	}
	authEvents = append(authEvents, memberEvent.EventID)
	r.updateRoomState(tx, roomID, memberEvent)
	r.updateRoomMember(tx, roomID, creatorID, MembershipJoin, memberEvent.EventID)
	
	// 6. 设置房间名称和主题
	if req.Name != nil {
		nameEvent := &MatrixEvent{
			EventID:   "$" + uuid.New().String(),
			RoomID:    roomID,
			EventType: EventTypeRoomName,
			Sender:    creatorID,
			StateKey:  ptrString(""),
			Content: map[string]interface{}{
				"name": *req.Name,
			},
			Depth: r.getNextDepth(tx, roomID),
		}
		tx.Create(nameEvent)
		r.updateRoomState(tx, roomID, nameEvent)
	}
	
	if req.Topic != nil {
		topicEvent := &MatrixEvent{
			EventID:   "$" + uuid.New().String(),
			RoomID:    roomID,
			EventType: EventTypeRoomTopic,
			Sender:    creatorID,
			StateKey:  ptrString(""),
			Content: map[string]interface{}{
				"topic": *req.Topic,
			},
			Depth: r.getNextDepth(tx, roomID),
		}
		tx.Create(topicEvent)
		r.updateRoomState(tx, roomID, topicEvent)
	}
	
	// 7. 处理邀请
	for _, inviteeID := range req.Invite {
		if err := r.inviteUser(tx, roomID, creatorID, inviteeID); err != nil {
			continue // 忽略邀请错误
		}
	}
	
	if err := tx.Commit().Error; err != nil {
		return nil, ErrInternal
	}
	
	return &CreateRoomResponse{RoomID: roomID}, nil
}

// createDefaultEvent 创建默认事件
func (r *RoomService) createDefaultEvent(tx *gorm.DB, roomID, sender string, eventType EventType, content map[string]interface{}, authEvents *[]string) {
	event := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: eventType,
		Sender:    sender,
		StateKey:  ptrString(""),
		Content:   content,
		Depth:     r.getNextDepth(tx, roomID),
	}
	if err := tx.Create(event).Error; err == nil {
		r.updateRoomState(tx, roomID, event)
		if authEvents != nil {
			*authEvents = append(*authEvents, event.EventID)
		}
	}
}

// ===== 房间加入 =====

// JoinRoomRequest 加入房间请求
type JoinRoomRequest struct {
	Reason string `json:"reason"`
}

// JoinRoom 加入房间
func (r *RoomService) JoinRoom(userID, roomIDOrAlias string, req *JoinRoomRequest) (string, *MatrixError) {
	roomID := roomIDOrAlias
	
	// 如果是别名，解析为房间ID
	if strings.HasPrefix(roomIDOrAlias, "#") {
		aliasService := NewAliasService(r.HS)
		resolvedRoomID, err := aliasService.GetRoomIDByAlias(roomIDOrAlias)
		if err != nil {
			return "", ErrNotFound
		}
		roomID = resolvedRoomID
	}
	
	// 检查房间是否存在
	var room MatrixRoom
	err := db.GetDB().Where("room_id = ?", roomID).First(&room).Error
	if err != nil {
		return "", ErrNotFound
	}
	
	// 检查用户是否已在房间中
	var member MatrixRoomMember
	err = db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error
	if err == nil {
		if member.Membership == MembershipBan {
			return "", ErrForbidden
		}
		// 如果是 invite 或 leave，更新为 join
		if member.Membership == MembershipInvite || member.Membership == MembershipLeave {
			// 创建成员事件
			event := &MatrixEvent{
				EventID:   "$" + uuid.New().String(),
				RoomID:    roomID,
				EventType: EventTypeRoomMember,
				Sender:    userID,
				StateKey:  ptrString(userID),
				Content: map[string]interface{}{
					"membership": "join",
				},
				Depth: r.getNextDepth(db.GetDB(), roomID),
			}
			db.GetDB().Create(event)
			r.updateRoomState(db.GetDB(), roomID, event)
			r.updateRoomMember(db.GetDB(), roomID, userID, MembershipJoin, event.EventID)
		}
		return roomID, nil
	}
	
	// 检查加入规则
	joinRule, _ := r.GetRoomState(roomID, EventTypeRoomJoinRules, "")
	if joinRule == "invite" || joinRule == "knock" {
		return "", ErrForbidden
	}
	
	// 创建成员事件
	event := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomMember,
		Sender:    userID,
		StateKey:  ptrString(userID),
		Content: map[string]interface{}{
			"membership": "join",
		},
		Depth: r.getNextDepth(db.GetDB(), roomID),
	}
	
	if err := db.GetDB().Create(event).Error; err != nil {
		return "", ErrInternal
	}
	
	r.updateRoomState(db.GetDB(), roomID, event)
	r.updateRoomMember(db.GetDB(), roomID, userID, MembershipJoin, event.EventID)
	
	return roomID, nil
}

// ===== 房间离开 =====

// LeaveRoomRequest 离开房间请求
type LeaveRoomRequest struct {
	Reason string `json:"reason"`
}

// LeaveRoom 离开房间
func (r *RoomService) LeaveRoom(userID, roomID string, req *LeaveRoomRequest) *MatrixError {
	// 检查用户是否在房间中
	var member MatrixRoomMember
	err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error
	if err != nil {
		return ErrNotFound
	}
	
	if member.Membership == MembershipLeave || member.Membership == MembershipBan {
		return nil // 已经离开
	}
	
	// 创建成员事件
	event := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomMember,
		Sender:    userID,
		StateKey:  ptrString(userID),
		Content: map[string]interface{}{
			"membership": "leave",
		},
		Depth: r.getNextDepth(db.GetDB(), roomID),
	}
	
	if err := db.GetDB().Create(event).Error; err != nil {
		return ErrInternal
	}
	
	r.updateRoomState(db.GetDB(), roomID, event)
	r.updateRoomMember(db.GetDB(), roomID, userID, MembershipLeave, event.EventID)
	
	return nil
}

// ===== 邀请 =====

// InviteUserRequest 邀请用户请求
type InviteUserRequest struct {
	Reason   string                 `json:"reason"`
	UserID   string                 `json:"user_id"`
	IsDirect *bool                  `json:"is_direct"`
	Content  map[string]interface{} `json:"-"`
}

// InviteUser 邀请用户
func (r *RoomService) InviteUser(roomID, inviterID string, req *InviteUserRequest) *MatrixError {
	// 检查邀请者权限
	if !r.canUserInvite(roomID, inviterID) {
		return ErrForbidden
	}
	
	// 检查被邀请者是否已在房间中
	var member MatrixRoomMember
	err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, req.UserID).First(&member).Error
	if err == nil {
		if member.Membership == MembershipJoin {
			return nil // 已在房间中
		}
		if member.Membership == MembershipBan {
			return ErrForbidden
		}
	}
	
	// 创建邀请
	if err := r.inviteUser(db.GetDB(), roomID, inviterID, req.UserID); err != nil {
		return ErrInternal
	}
	return nil
}

// inviteUser 内部邀请函数
func (r *RoomService) inviteUser(tx *gorm.DB, roomID, inviterID, inviteeID string) error {
	event := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomMember,
		Sender:    inviterID,
		StateKey:  ptrString(inviteeID),
		Content: map[string]interface{}{
			"membership": "invite",
		},
		Depth: r.getNextDepth(tx, roomID),
	}
	
	if err := tx.Create(event).Error; err != nil {
		return err
	}
	
	r.updateRoomState(tx, roomID, event)
	r.updateRoomMember(tx, roomID, inviteeID, MembershipInvite, event.EventID)
	
	return nil
}

// ===== 踢人/封禁 =====

// KickUserRequest 踢人请求
type KickUserRequest struct {
	Reason string `json:"reason"`
	UserID string `json:"user_id"`
}

// KickUser 踢出用户
func (r *RoomService) KickUser(roomID, kickerID string, req *KickUserRequest) *MatrixError {
	// 检查权限
	if !r.canUserKick(roomID, kickerID) {
		return ErrForbidden
	}
	
	// 检查被踢者是否在房间中
	var member MatrixRoomMember
	err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, req.UserID).First(&member).Error
	if err != nil {
		return ErrNotFound
	}
	
	// 创建 leave 事件
	event := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomMember,
		Sender:    kickerID,
		StateKey:  ptrString(req.UserID),
		Content: map[string]interface{}{
			"membership": "leave",
			"reason":     req.Reason,
		},
		Depth: r.getNextDepth(db.GetDB(), roomID),
	}
	
	if err := db.GetDB().Create(event).Error; err != nil {
		return ErrInternal
	}
	
	r.updateRoomState(db.GetDB(), roomID, event)
	r.updateRoomMember(db.GetDB(), roomID, req.UserID, MembershipLeave, event.EventID)
	
	return nil
}

// BanUserRequest 封禁请求
type BanUserRequest struct {
	Reason string `json:"reason"`
	UserID string `json:"user_id"`
}

// BanUser 封禁用户
func (r *RoomService) BanUser(roomID, bannerID string, req *BanUserRequest) *MatrixError {
	// 检查权限
	if !r.canUserBan(roomID, bannerID) {
		return ErrForbidden
	}
	
	// 创建 ban 事件
	event := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomMember,
		Sender:    bannerID,
		StateKey:  ptrString(req.UserID),
		Content: map[string]interface{}{
			"membership": "ban",
			"reason":     req.Reason,
		},
		Depth: r.getNextDepth(db.GetDB(), roomID),
	}
	
	if err := db.GetDB().Create(event).Error; err != nil {
		return ErrInternal
	}
	
	r.updateRoomState(db.GetDB(), roomID, event)
	r.updateRoomMember(db.GetDB(), roomID, req.UserID, MembershipBan, event.EventID)
	
	return nil
}

// ===== 房间状态 =====

// GetRoomState 获取房间状态
func (r *RoomService) GetRoomState(roomID string, eventType EventType, stateKey string) (interface{}, *MatrixError) {
	var state MatrixRoomState
	err := db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, eventType, stateKey).First(&state).Error
	if err != nil {
		return nil, ErrNotFound
	}
	return state.Content, nil
}

// GetRoomStateAll 获取所有房间状态
func (r *RoomService) GetRoomStateAll(roomID string) ([]*ClientEvent, *MatrixError) {
	var states []MatrixRoomState
	db.GetDB().Where("room_id = ?", roomID).Find(&states)
	
	events := make([]*ClientEvent, len(states))
	for i, state := range states {
		events[i] = &ClientEvent{
			EventID: state.EventID,
			RoomID:  state.RoomID,
			Type:    string(state.EventType),
			StateKey: &state.StateKey,
			Content: state.Content,
		}
	}
	
	return events, nil
}

// ===== 房间成员 =====

// GetRoomMembers 获取房间成员
func (r *RoomService) GetRoomMembers(roomID string, membership []MembershipState) ([]*MatrixRoomMember, *MatrixError) {
	query := db.GetDB().Where("room_id = ?", roomID)
	
	if len(membership) > 0 {
		query = query.Where("membership IN ?", membership)
	}
	
	var members []*MatrixRoomMember
	if err := query.Find(&members).Error; err != nil {
		return nil, ErrInternal
	}
	
	return members, nil
}

// GetUserMembership 获取用户在房间中的状态
func (r *RoomService) GetUserMembership(roomID, userID string) (MembershipState, error) {
	var member MatrixRoomMember
	err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error
	if err != nil {
		return MembershipLeave, nil
	}
	return member.Membership, nil
}

// ===== 权限检查 =====

// canUserInvite 检查用户是否可以邀请
func (r *RoomService) canUserInvite(roomID, userID string) bool {
	// 检查用户是否在房间中
	membership, _ := r.GetUserMembership(roomID, userID)
	if membership != MembershipJoin {
		return false
	}
	
	// 获取 power levels
	pl, err := r.getRoomPowerLevels(roomID)
	if err != nil {
		return false
	}
	
	userPL := r.getUserPowerLevel(pl, userID)
	return userPL >= pl.Invite
}

// canUserKick 检查用户是否可以踢人
func (r *RoomService) canUserKick(roomID, userID string) bool {
	pl, err := r.getRoomPowerLevels(roomID)
	if err != nil {
		return false
	}
	
	userPL := r.getUserPowerLevel(pl, userID)
	return userPL >= pl.Kick
}

// canUserBan 检查用户是否可以封禁
func (r *RoomService) canUserBan(roomID, userID string) bool {
	pl, err := r.getRoomPowerLevels(roomID)
	if err != nil {
		return false
	}
	
	userPL := r.getUserPowerLevel(pl, userID)
	return userPL >= pl.Ban
}

// ===== 辅助函数 =====

// getDefaultPowerLevels 获取默认权限级别
func (r *RoomService) getDefaultPowerLevels() *PowerLevelContent {
	return &PowerLevelContent{
		Users:         make(map[string]int64),
		UsersDefault:  0,
		Events:        make(map[string]int64),
		EventsDefault: 0,
		StateDefault:  50,
		Ban:           50,
		Kick:          50,
		Redact:        50,
		Invite:        50,
	}
}

// getRoomPowerLevels 获取房间权限级别
func (r *RoomService) getRoomPowerLevels(roomID string) (*PowerLevelContent, error) {
	content, err := r.GetRoomState(roomID, EventTypeRoomPowerLevels, "")
	if err != nil {
		return r.getDefaultPowerLevels(), nil
	}
	
	pl := &PowerLevelContent{}
	if b, ok := content.(map[string]interface{}); ok {
		if users, ok := b["users"].(map[string]interface{}); ok {
			pl.Users = make(map[string]int64)
			for k, v := range users {
				if fv, ok := v.(float64); ok {
					pl.Users[k] = int64(fv)
				}
			}
		}
		if v, ok := b["users_default"].(float64); ok {
			pl.UsersDefault = int64(v)
		}
		if v, ok := b["events_default"].(float64); ok {
			pl.EventsDefault = int64(v)
		}
		if v, ok := b["state_default"].(float64); ok {
			pl.StateDefault = int64(v)
		}
		if v, ok := b["ban"].(float64); ok {
			pl.Ban = int64(v)
		}
		if v, ok := b["kick"].(float64); ok {
			pl.Kick = int64(v)
		}
		if v, ok := b["redact"].(float64); ok {
			pl.Redact = int64(v)
		}
		if v, ok := b["invite"].(float64); ok {
			pl.Invite = int64(v)
		}
	}
	
	return pl, nil
}

// getUserPowerLevel 获取用户权限级别
func (r *RoomService) getUserPowerLevel(pl *PowerLevelContent, userID string) int64 {
	if level, ok := pl.Users[userID]; ok {
		return level
	}
	return pl.UsersDefault
}

// updateRoomState 更新房间状态
func (r *RoomService) updateRoomState(tx *gorm.DB, roomID string, event *MatrixEvent) {
	if !event.IsStateEvent() {
		return
	}
	
	stateKey := ""
	if event.StateKey != nil {
		stateKey = *event.StateKey
	}
	
	// 删除旧状态
	tx.Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, event.EventType, stateKey).
		Delete(&MatrixRoomState{})
	
	// 创建新状态
	state := &MatrixRoomState{
		RoomID:    roomID,
		EventType: event.EventType,
		StateKey:  stateKey,
		Content:   event.Content,
		EventID:   event.EventID,
	}
	tx.Create(state)
}

// updateRoomMember 更新房间成员
func (r *RoomService) updateRoomMember(tx *gorm.DB, roomID, userID string, membership MembershipState, eventID string) {
	var member MatrixRoomMember
	err := tx.Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error
	if err != nil {
		// 创建新成员
		member = MatrixRoomMember{
			RoomID:     roomID,
			UserID:     userID,
			Membership: membership,
			EventID:    eventID,
		}
		tx.Create(&member)
	} else {
		// 更新成员
		tx.Model(&member).Updates(map[string]interface{}{
			"membership": membership,
			"event_id":   eventID,
		})
	}
}

// getNextDepth 获取下一个深度
func (r *RoomService) getNextDepth(tx *gorm.DB, roomID string) int64 {
	var maxDepth int64
	tx.Model(&MatrixEvent{}).Where("room_id = ?", roomID).Select("COALESCE(MAX(depth), 0)").Scan(&maxDepth)
	return maxDepth + 1
}

// ptrString 辅助函数
func ptrString(s string) *string {
	return &s
}

// GetRoom 获取房间信息
func (r *RoomService) GetRoom(roomID string) (*MatrixRoom, error) {
	var room MatrixRoom
	err := db.GetDB().Where("room_id = ?", roomID).First(&room).Error
	if err != nil {
		return nil, err
	}
	return &room, nil
}

// GetJoinedRooms 获取用户加入的房间
func (r *RoomService) GetJoinedRooms(userID string) ([]string, error) {
	var members []MatrixRoomMember
	db.GetDB().Where("user_id = ? AND membership = ?", userID, MembershipJoin).Find(&members)
	
	roomIDs := make([]string, len(members))
	for i, m := range members {
		roomIDs[i] = m.RoomID
	}
	
	return roomIDs, nil
}
