package homeserver

import (
	"fmt"
	"time"

	"github.com/kasuganosora/community/db"
)

// SyncService 同步服务
type SyncService struct {
	HS *HomeServer
}

// NewSyncService 创建同步服务
func NewSyncService(hs *HomeServer) *SyncService {
	return &SyncService{HS: hs}
}

// ===== 同步请求 =====

// SyncRequest 同步请求
type SyncRequest struct {
	Filter       string `form:"filter"`
	Since        string `form:"since"`        // 上次同步的 token
	FullState    bool   `form:"full_state"`   // 是否返回完整状态
	SetPresence  string `form:"set_presence"` // 设置在线状态
	Timeout      int    `form:"timeout"`      // 长轮询超时（毫秒）
	SinceAll     bool   `form:"_since"`
}

// SyncResponse 同步响应
type SyncResponse struct {
	NextBatch string `json:"next_batch"`
	
	// 房间
	Rooms *SyncRooms `json:"rooms,omitempty"`
	
	// Presence
	Presence *SyncPresence `json:"presence,omitempty"`
	
	// Account Data
	AccountData *SyncAccountData `json:"account_data,omitempty"`
	
	// To-Device
	ToDevice *SyncToDevice `json:"to_device,omitempty"`
	
	// Device Lists
	DeviceLists *DeviceLists `json:"device_lists,omitempty"`
	
	// Device One-Time Keys Count
	DeviceOneTimeKeysCount map[string]int64 `json:"device_one_time_keys_count,omitempty"`
}

// SyncRooms 房间同步
type SyncRooms struct {
	Join   map[string]*JoinedRoom   `json:"join,omitempty"`
	Invite map[string]*InvitedRoom  `json:"invite,omitempty"`
	Leave  map[string]*LeftRoom     `json:"leave,omitempty"`
	Knock  map[string]*KnockedRoom  `json:"knock,omitempty"`
}

// JoinedRoom 已加入房间
type JoinedRoom struct {
	State        *SyncState       `json:"state,omitempty"`
	Timeline     *SyncTimeline    `json:"timeline,omitempty"`
	Ephemeral    *SyncEphemeral   `json:"ephemeral,omitempty"`
	AccountData  *SyncAccountData `json:"account_data,omitempty"`
	UnreadNotifications *UnreadNotifications `json:"unread_notifications,omitempty"`
	Summary      *RoomSummary     `json:"summary,omitempty"`
}

// InvitedRoom 被邀请房间
type InvitedRoom struct {
	InviteState *SyncInviteState `json:"invite_state,omitempty"`
}

// LeftRoom 已离开房间
type LeftRoom struct {
	State    *SyncState    `json:"state,omitempty"`
	Timeline *SyncTimeline `json:"timeline,omitempty"`
}

// KnockedRoom 敲门房间
type KnockedRoom struct {
	KnockState *SyncState `json:"knock_state,omitempty"`
}

// SyncState 状态同步
type SyncState struct {
	Events []*ClientEvent `json:"events,omitempty"`
}

// SyncTimeline 时间线同步
type SyncTimeline struct {
	Events    []*ClientEvent `json:"events,omitempty"`
	Limited   bool           `json:"limited"`
	PrevBatch string         `json:"prev_batch,omitempty"`
}

// SyncEphemeral 临时数据
type SyncEphemeral struct {
	Events []*ClientEvent `json:"events,omitempty"`
}

// SyncAccountData 账户数据
type SyncAccountData struct {
	Events []*ClientEvent `json:"events,omitempty"`
}

// SyncInviteState 邀请状态
type SyncInviteState struct {
	Events []*ClientEvent `json:"events,omitempty"`
}

// UnreadNotifications 未读通知
type UnreadNotifications struct {
	HighlightCount    int64 `json:"highlight_count"`
	NotificationCount int64 `json:"notification_count"`
}

// RoomSummary 房间摘要
type RoomSummary struct {
	Heroes                []string `json:"m.heroes,omitempty"`
	JoinedMemberCount     int64    `json:"m.joined_member_count,omitempty"`
	InvitedMemberCount    int64    `json:"m.invited_member_count,omitempty"`
}

// SyncPresence 在线状态
type SyncPresence struct {
	Events []*ClientEvent `json:"events,omitempty"`
}

// SyncToDevice To-Device 消息
type SyncToDevice struct {
	Events []*ToDeviceEvent `json:"events,omitempty"`
}

// ToDeviceEvent To-Device 事件
type ToDeviceEvent struct {
	Sender  string                 `json:"sender"`
	Type    string                 `json:"type"`
	Content map[string]interface{} `json:"content"`
}

// DeviceLists 设备列表变更
type DeviceLists struct {
	Changed []string `json:"changed,omitempty"`
	Left    []string `json:"left,omitempty"`
}

// Sync 执行同步
func (s *SyncService) Sync(userID, deviceID string, req *SyncRequest) (*SyncResponse, *MatrixError) {
	// 解析上次同步的 token
	since := s.parseSyncToken(req.Since)
	
	// 设置在线状态
	if req.SetPresence != "" {
		s.setPresence(userID, req.SetPresence)
	}
	
	// 构建响应
	resp := &SyncResponse{
		Rooms: &SyncRooms{
			Join:   make(map[string]*JoinedRoom),
			Invite: make(map[string]*InvitedRoom),
			Leave:  make(map[string]*LeftRoom),
		},
		ToDevice: &SyncToDevice{
			Events: make([]*ToDeviceEvent, 0),
		},
		DeviceLists: &DeviceLists{
			Changed: make([]string, 0),
			Left:    make([]string, 0),
		},
		DeviceOneTimeKeysCount: make(map[string]int64),
	}
	
	// 获取用户加入的房间
	joinedRooms, _ := s.HS.Room.GetJoinedRooms(userID)
	
	// 处理每个房间
	for _, roomID := range joinedRooms {
		joinedRoom := s.buildJoinedRoom(roomID, userID, since, req.FullState)
		if joinedRoom != nil {
			resp.Rooms.Join[roomID] = joinedRoom
		}
	}
	
	// 获取被邀请的房间
	invitedRooms := s.getInvitedRooms(userID)
	for _, roomID := range invitedRooms {
		invitedRoom := s.buildInvitedRoom(roomID, userID)
		if invitedRoom != nil {
			resp.Rooms.Invite[roomID] = invitedRoom
		}
	}
	
	// 获取已离开的房间
	leftRooms := s.getLeftRooms(userID, since)
	for _, roomID := range leftRooms {
		leftRoom := s.buildLeftRoom(roomID, userID)
		if leftRoom != nil {
			resp.Rooms.Leave[roomID] = leftRoom
		}
	}
	
	// 获取账户数据
	resp.AccountData = s.getAccountData(userID, since)
	
	// 获取 To-Device 消息
	resp.ToDevice = s.getToDeviceMessages(userID, deviceID)
	
	// 生成下一个 token
	resp.NextBatch = s.generateSyncToken()
	
	return resp, nil
}

// buildJoinedRoom 构建已加入房间
func (s *SyncService) buildJoinedRoom(roomID, userID string, since int64, fullState bool) *JoinedRoom {
	room := &JoinedRoom{
		State:       &SyncState{Events: make([]*ClientEvent, 0)},
		Timeline:    &SyncTimeline{Events: make([]*ClientEvent, 0)},
		Ephemeral:   &SyncEphemeral{Events: make([]*ClientEvent, 0)},
		AccountData: &SyncAccountData{Events: make([]*ClientEvent, 0)},
	}
	
	// 获取房间状态
	if fullState || since == 0 {
		stateEvents, _ := s.HS.Room.GetRoomStateAll(roomID)
		room.State.Events = stateEvents
	} else {
		// 只获取变更的状态
		room.State.Events = s.getChangedStateEvents(roomID, since)
	}
	
	// 获取时间线事件
	var events []MatrixEvent
	query := db.GetDB().Where("room_id = ?", roomID)
	if since > 0 {
		query = query.Where("origin_server_ts > ?", since)
	}
	query.Order("origin_server_ts ASC").Limit(50).Find(&events)
	
	room.Timeline.Events = make([]*ClientEvent, len(events))
	for i, event := range events {
		room.Timeline.Events[i] = event.ToClientEvent()
	}
	
	if len(events) == 50 {
		room.Timeline.Limited = true
		room.Timeline.PrevBatch = s.generatePaginationToken(events[0].OriginServerTS)
	}
	
	// 获取临时事件（typing, receipts）
	room.Ephemeral.Events = s.getEphemeralEvents(roomID)
	
	// 获取房间账户数据
	room.AccountData.Events = s.getRoomAccountData(userID, roomID)
	
	// 获取未读通知数
	room.UnreadNotifications = s.getUnreadNotifications(roomID, userID)
	
	// 获取房间摘要
	room.Summary = s.getRoomSummary(roomID)
	
	return room
}

// buildInvitedRoom 构建被邀请房间
func (s *SyncService) buildInvitedRoom(roomID, userID string) *InvitedRoom {
	room := &InvitedRoom{
		InviteState: &SyncInviteState{Events: make([]*ClientEvent, 0)},
	}
	
	// 获取邀请状态
	state, _ := s.HS.Room.GetRoomStateAll(roomID)
	room.InviteState.Events = state
	
	return room
}

// buildLeftRoom 构建已离开房间
func (s *SyncService) buildLeftRoom(roomID, userID string) *LeftRoom {
	room := &LeftRoom{
		State:    &SyncState{Events: make([]*ClientEvent, 0)},
		Timeline: &SyncTimeline{Events: make([]*ClientEvent, 0)},
	}
	
	// 获取状态
	state, _ := s.HS.Room.GetRoomStateAll(roomID)
	room.State.Events = state
	
	return room
}

// ===== 辅助函数 =====

// parseSyncToken 解析同步 token
func (s *SyncService) parseSyncToken(token string) int64 {
	if token == "" {
		return 0
	}
	var ts int64
	_, err := fmt.Sscanf(token, "s%d", &ts)
	if err != nil {
		return 0
	}
	return ts
}

// generateSyncToken 生成同步 token
func (s *SyncService) generateSyncToken() string {
	return fmt.Sprintf("s%d", time.Now().UnixMilli())
}

// generatePaginationToken 生成分页 token
func (s *SyncService) generatePaginationToken(ts int64) string {
	return fmt.Sprintf("t%d", ts)
}

// setPresence 设置在线状态
func (s *SyncService) setPresence(userID, presence string) {
	// 更新用户在线状态
	now := time.Now().UnixMilli()

	// 查找现有状态记录
	var presenceRecord MatrixPresence
	result := db.GetDB().Where("user_id = ?", userID).First(&presenceRecord)

	if result.Error == nil {
		// 更新现有记录
		db.GetDB().Model(&presenceRecord).Updates(map[string]interface{}{
			"presence":       presence,
			"last_active_ts": now,
		})
	} else {
		// 创建新记录
		nowTime := time.Now()
		presenceRecord = MatrixPresence{
			UserID:       userID,
			Presence:     presence,
			LastActiveTS: now,
			CreatedAt:    &nowTime,
		}
		db.GetDB().Create(&presenceRecord)
	}
}

// getInvitedRooms 获取被邀请的房间
func (s *SyncService) getInvitedRooms(userID string) []string {
	var members []MatrixRoomMember
	db.GetDB().Where("user_id = ? AND membership = ?", userID, MembershipInvite).Find(&members)
	
	roomIDs := make([]string, len(members))
	for i, m := range members {
		roomIDs[i] = m.RoomID
	}
	return roomIDs
}

// getLeftRooms 获取已离开的房间
func (s *SyncService) getLeftRooms(userID string, since int64) []string {
	var members []MatrixRoomMember
	db.GetDB().Where("user_id = ? AND (membership = ? OR membership = ?)", userID, MembershipLeave, MembershipBan).Find(&members)
	
	roomIDs := make([]string, len(members))
	for i, m := range members {
		roomIDs[i] = m.RoomID
	}
	return roomIDs
}

// getChangedStateEvents 获取变更的状态事件
func (s *SyncService) getChangedStateEvents(roomID string, since int64) []*ClientEvent {
	var events []MatrixEvent
	db.GetDB().Where("room_id = ? AND state_key IS NOT NULL AND origin_server_ts > ?", roomID, since).
		Order("origin_server_ts ASC").Find(&events)
	
	result := make([]*ClientEvent, len(events))
	for i, event := range events {
		result[i] = event.ToClientEvent()
	}
	return result
}

// getEphemeralEvents 获取临时事件
func (s *SyncService) getEphemeralEvents(roomID string) []*ClientEvent {
	events := make([]*ClientEvent, 0)
	
	// 获取 typing 状态
	typing := s.getTypingUsers(roomID)
	if len(typing) > 0 {
		events = append(events, &ClientEvent{
			Type: "m.typing",
			Content: map[string]interface{}{
				"user_ids": typing,
			},
		})
	}
	
	// 获取 receipts
	receipts := s.getReceipts(roomID)
	if len(receipts) > 0 {
		events = append(events, &ClientEvent{
			Type:    "m.receipt",
			Content: receipts,
		})
	}
	
	return events
}

// getTypingUsers 获取正在输入的用户
func (s *SyncService) getTypingUsers(roomID string) []string {
	var typing []MatrixTyping
	db.GetDB().Where("room_id = ? AND expires_at > ?", roomID, time.Now()).Find(&typing)
	
	userIDs := make([]string, len(typing))
	for i, t := range typing {
		userIDs[i] = t.UserID
	}
	return userIDs
}

// getReceipts 获取回执
func (s *SyncService) getReceipts(roomID string) map[string]interface{} {
	var receipts []MatrixReceipt
	db.GetDB().Where("room_id = ?", roomID).Find(&receipts)
	
	result := make(map[string]interface{})
	for _, r := range receipts {
		if _, ok := result[r.EventID]; !ok {
			result[r.EventID] = make(map[string]interface{})
		}
		eventReceipts := result[r.EventID].(map[string]interface{})
		if _, ok := eventReceipts[r.ReceiptType]; !ok {
			eventReceipts[r.ReceiptType] = make(map[string]interface{})
		}
		typeReceipts := eventReceipts[r.ReceiptType].(map[string]interface{})
		typeReceipts[r.UserID] = map[string]interface{}{
			"ts": r.TS,
		}
	}
	
	return result
}

// getAccountData 获取账户数据
func (s *SyncService) getAccountData(userID string, since int64) *SyncAccountData {
	var accountData []MatrixAccountData
	query := db.GetDB().Where("user_id = ?", userID).Where("room_id IS NULL")
	if since > 0 {
		// 这里简化处理，实际应该跟踪更新时间
	}
	query.Find(&accountData)
	
	events := make([]*ClientEvent, len(accountData))
	for i, data := range accountData {
		events[i] = &ClientEvent{
			Type:    data.DataType,
			Content: data.Content,
		}
	}
	
	return &SyncAccountData{Events: events}
}

// getRoomAccountData 获取房间账户数据
func (s *SyncService) getRoomAccountData(userID, roomID string) []*ClientEvent {
	var accountData []MatrixAccountData
	db.GetDB().Where("user_id = ? AND room_id = ?", userID, roomID).Find(&accountData)
	
	events := make([]*ClientEvent, len(accountData))
	for i, data := range accountData {
		events[i] = &ClientEvent{
			Type:    data.DataType,
			Content: data.Content,
		}
	}
	
	return events
}

// getToDeviceMessages 获取 To-Device 消息
func (s *SyncService) getToDeviceMessages(userID, deviceID string) *SyncToDevice {
	var messages []MatrixToDeviceMessage
	db.GetDB().Where("target_user = ? AND target_device = ?", userID, deviceID).Find(&messages)
	
	events := make([]*ToDeviceEvent, len(messages))
	for i, msg := range messages {
		events[i] = &ToDeviceEvent{
			Sender:  msg.Sender,
			Type:    msg.EventType,
			Content: msg.Content,
		}
	}
	
	// 删除已发送的消息
	db.GetDB().Where("target_user = ? AND target_device = ?", userID, deviceID).Delete(&MatrixToDeviceMessage{})
	
	return &SyncToDevice{Events: events}
}

// getUnreadNotifications 获取未读通知
func (s *SyncService) getUnreadNotifications(roomID, userID string) *UnreadNotifications {
	// 简化实现，实际应该跟踪阅读位置
	return &UnreadNotifications{
		HighlightCount:    0,
		NotificationCount: 0,
	}
}

// getRoomSummary 获取房间摘要
func (s *SyncService) getRoomSummary(roomID string) *RoomSummary {
	// 获取成员
	members, _ := s.HS.Room.GetRoomMembers(roomID, []MembershipState{MembershipJoin, MembershipInvite})
	
	heroes := make([]string, 0)
	var joinedCount, invitedCount int64
	
	for _, m := range members {
		if m.Membership == MembershipJoin {
			joinedCount++
			if len(heroes) < 5 {
				heroes = append(heroes, m.UserID)
			}
		} else if m.Membership == MembershipInvite {
			invitedCount++
		}
	}
	
	return &RoomSummary{
		Heroes:             heroes,
		JoinedMemberCount:  joinedCount,
		InvitedMemberCount: invitedCount,
	}
}

// ===== Typing =====

// SetTyping 设置输入状态
func (s *SyncService) SetTyping(roomID, userID string, typing bool, timeout int64) *MatrixError {
	if typing {
		typingEvent := &MatrixTyping{
			RoomID:    roomID,
			UserID:    userID,
			Timeout:   timeout,
			ExpiresAt: time.Now().Add(time.Duration(timeout) * time.Millisecond),
		}
		db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).Delete(&MatrixTyping{})
		db.GetDB().Create(typingEvent)
	} else {
		db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).Delete(&MatrixTyping{})
	}
	return nil
}

// ===== Receipts =====

// SetReceipt 设置回执
func (s *SyncService) SetReceipt(roomID, eventID, userID, receiptType string) *MatrixError {
	// 检查权限
	membership, _ := s.HS.Room.GetUserMembership(roomID, userID)
	if membership != MembershipJoin {
		return ErrForbidden
	}
	
	// 删除旧回执
	db.GetDB().Where("room_id = ? AND user_id = ? AND receipt_type = ?", roomID, userID, receiptType).
		Delete(&MatrixReceipt{})
	
	// 创建新回执
	receipt := &MatrixReceipt{
		RoomID:      roomID,
		EventID:     eventID,
		UserID:      userID,
		ReceiptType: receiptType,
		TS:          time.Now().UnixMilli(),
	}
	
	if err := db.GetDB().Create(receipt).Error; err != nil {
		return ErrInternal
	}
	
	return nil
}
