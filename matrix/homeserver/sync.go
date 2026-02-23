package homeserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
)

// parsedFilter represents a parsed sync filter
type parsedFilter struct {
	Room *parsedRoomFilter `json:"room,omitempty"`
}

type parsedRoomFilter struct {
	IncludeLeave bool                    `json:"include_leave,omitempty"`
	Timeline     *parsedEventFilter      `json:"timeline,omitempty"`
	State        *parsedEventFilter      `json:"state,omitempty"`
}

type parsedEventFilter struct {
	Types                      []string `json:"types,omitempty"`
	NotTypes                   []string `json:"not_types,omitempty"`
	Limit                      *int     `json:"limit,omitempty"`
	UnreadThreadNotifications  *bool    `json:"unread_thread_notifications,omitempty"`
}

// loadFilter loads and parses a sync filter from the request
func (s *SyncService) loadFilter(userID, filterStr string) *parsedFilter {
	if filterStr == "" {
		return nil
	}
	// If it starts with '{', it's an inline JSON filter
	if strings.HasPrefix(filterStr, "{") {
		var f parsedFilter
		if json.Unmarshal([]byte(filterStr), &f) == nil {
			return &f
		}
		return nil
	}
	// Otherwise it's a filter ID - load from DB
	var mf MatrixFilter
	if err := db.GetDB().Where("user_id = ? AND filter_id = ?", userID, filterStr).First(&mf).Error; err != nil {
		return nil
	}
	var f parsedFilter
	if json.Unmarshal([]byte(mf.Filter), &f) == nil {
		return &f
	}
	return nil
}

// filterEvents applies a type filter to a list of events
func filterEvents(events []*ClientEvent, ef *parsedEventFilter) []*ClientEvent {
	if ef == nil {
		return events
	}
	result := make([]*ClientEvent, 0, len(events))
	for _, e := range events {
		if len(ef.Types) > 0 {
			matched := false
			for _, t := range ef.Types {
				if matchEventType(e.Type, t) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if len(ef.NotTypes) > 0 {
			excluded := false
			for _, t := range ef.NotTypes {
				if matchEventType(e.Type, t) {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
		}
		result = append(result, e)
	}
	return result
}

// matchEventType matches an event type against a filter pattern (supports * wildcard)
func matchEventType(eventType, pattern string) bool {
	if pattern == "*" {
		return true
	}
	return eventType == pattern
}

// typingChangeTracker tracks the last time typing status changed per room
var typingChangeTracker = struct {
	sync.RWMutex
	rooms map[string]int64
}{rooms: make(map[string]int64)}

func markTypingChanged(roomID string) {
	typingChangeTracker.Lock()
	typingChangeTracker.rooms[roomID] = time.Now().UnixMilli()
	typingChangeTracker.Unlock()
}

func getLastTypingChange(roomID string) int64 {
	typingChangeTracker.RLock()
	defer typingChangeTracker.RUnlock()
	return typingChangeTracker.rooms[roomID]
}

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
	UnreadNotifications       *UnreadNotifications            `json:"unread_notifications,omitempty"`
	UnreadThreadNotifications map[string]*UnreadNotifications `json:"unread_thread_notifications,omitempty"`
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
	Events []*ClientEvent `json:"events"`
}

// SyncTimeline 时间线同步
type SyncTimeline struct {
	Events    []*ClientEvent `json:"events"`
	Limited   bool           `json:"limited"`
	PrevBatch string         `json:"prev_batch,omitempty"`
}

// SyncEphemeral 临时数据
type SyncEphemeral struct {
	Events []*ClientEvent `json:"events"`
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
	// Capture current time at the START of the sync for next_batch token.
	// This ensures events created between query time and response time are
	// captured in the following sync (not lost between syncs).
	syncStartTS := time.Now().UnixMilli()

	// 解析上次同步的 token
	since := s.parseSyncToken(req.Since)

	// Load sync filter
	filter := s.loadFilter(userID, req.Filter)

	// 设置在线状态 — Matrix spec: if set_presence is omitted, default to "online"
	presence := req.SetPresence
	if presence == "" {
		presence = "online"
	}
	if presence != "offline" {
		s.setPresence(userID, presence)
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
		joinedRoom := s.buildJoinedRoomForDevice(roomID, userID, deviceID, since, req.FullState, filter)
		if joinedRoom != nil {
			// For incremental sync, skip rooms with no changes
			if since > 0 && !req.FullState {
				hasChanges := len(joinedRoom.Timeline.Events) > 0 ||
					len(joinedRoom.State.Events) > 0
				// Include room if typing status changed since last sync
				if !hasChanges && getLastTypingChange(roomID) > since {
					hasChanges = true
				}
				// Check ephemeral: non-typing events also count as changes
				if !hasChanges && joinedRoom.Ephemeral != nil {
					for _, ev := range joinedRoom.Ephemeral.Events {
						if ev.Type != "m.typing" {
							hasChanges = true
						}
					}
				}
				if !hasChanges {
					continue
				}
			}
			resp.Rooms.Join[roomID] = joinedRoom
		}
	}
	
	// 获取被邀请的房间
	// Get ignored user list
	ignoredUsers := s.getIgnoredUsers(userID)

	invitedRooms := s.getInvitedRooms(userID)
	for _, roomID := range invitedRooms {
		// Filter out invites from ignored users
		if len(ignoredUsers) > 0 {
			var inviter MatrixRoomMember
			if db.GetDB().Where("room_id = ? AND user_id = ? AND membership = ?",
				roomID, userID, MembershipInvite).First(&inviter).Error == nil {
				// Find who sent the invite by looking at the invite event sender
				if inviter.EventID != "" {
					var inviteEvent MatrixEvent
					if db.GetDB().Where("event_id = ?", inviter.EventID).First(&inviteEvent).Error == nil {
						if ignoredUsers[inviteEvent.Sender] {
							continue
						}
					}
				}
			}
		}
		invitedRoom := s.buildInvitedRoom(roomID, userID)
		if invitedRoom != nil {
			resp.Rooms.Invite[roomID] = invitedRoom
		}
	}
	
	// 获取已离开的房间
	// include_leave defaults to false; only include left rooms if filter says so or no filter
	includeLeave := true
	if filter != nil && filter.Room != nil && !filter.Room.IncludeLeave {
		includeLeave = false
	}
	// But if no filter is set, still include left rooms (default behavior for full sync)
	if filter == nil {
		includeLeave = true
	}
	if includeLeave {
		leftRooms := s.getLeftRooms(userID, since)
		for _, roomID := range leftRooms {
			leftRoom := s.buildLeftRoom(roomID, userID, since, filter)
			if leftRoom != nil {
				resp.Rooms.Leave[roomID] = leftRoom
			}
		}
	}
	
	// 获取账户数据
	resp.AccountData = s.getAccountData(userID, since)

	// 获取 To-Device 消息
	resp.ToDevice = s.getToDeviceMessages(userID, deviceID)

	// 获取在线状态（包含自己和房间成员）
	resp.Presence = s.getPresenceEvents(userID, joinedRooms, since)

	// Populate device_lists.changed and device_lists.left for incremental syncs
	if since > 0 {
		changed, left := s.getDeviceListUpdates(userID, joinedRooms, since)
		resp.DeviceLists.Changed = changed
		resp.DeviceLists.Left = left
	}

	// 生成下一个 token — use time captured at start of sync to avoid missing events
	resp.NextBatch = fmt.Sprintf("s%d", syncStartTS)
	
	return resp, nil
}

// buildJoinedRoom 构建已加入房间
func (s *SyncService) buildJoinedRoom(roomID, userID string, since int64, fullState bool) *JoinedRoom {
	return s.buildJoinedRoomForDevice(roomID, userID, "", since, fullState, nil)
}

// buildJoinedRoomForDevice 构建已加入房间（带设备 ID，用于 unsigned.transaction_id）
func (s *SyncService) buildJoinedRoomForDevice(roomID, userID, deviceID string, since int64, fullState bool, filter *parsedFilter) *JoinedRoom {
	room := &JoinedRoom{
		State:       &SyncState{Events: make([]*ClientEvent, 0)},
		Timeline:    &SyncTimeline{Events: make([]*ClientEvent, 0)},
		Ephemeral:   &SyncEphemeral{Events: make([]*ClientEvent, 0)},
		AccountData: &SyncAccountData{Events: make([]*ClientEvent, 0)},
	}
	
	// Resolve filter settings
	var timelineFilter *parsedEventFilter
	var stateFilter *parsedEventFilter
	if filter != nil && filter.Room != nil {
		timelineFilter = filter.Room.Timeline
		stateFilter = filter.Room.State
	}

	// 获取房间状态
	if fullState || since == 0 {
		stateEvents, _ := s.HS.Room.GetRoomStateAll(roomID)
		room.State.Events = stateEvents
	} else {
		// 只获取变更的状态
		room.State.Events = s.getChangedStateEvents(roomID, since)
	}
	// Apply state filter
	if stateFilter != nil {
		room.State.Events = filterEvents(room.State.Events, stateFilter)
	}

	// 获取时间线事件（多取一个用于判断 limited）
	timelineLimit := 50
	if timelineFilter != nil && timelineFilter.Limit != nil {
		timelineLimit = *timelineFilter.Limit
		if timelineLimit < 0 {
			timelineLimit = 0
		}
	}
	var events []MatrixEvent
	if since > 0 {
		// Check if user newly joined this room since the last sync
		joinTS := s.getUserJoinTS(roomID, userID)
		isNewlyJoined := joinTS > since

		if isNewlyJoined {
			// Newly-joined room: treat like initial sync for this room.
			// Show recent events (up to timeline limit), mark as limited if room has more.
			db.GetDB().Where("room_id = ?", roomID).
				Order("origin_server_ts DESC").Limit(timelineLimit + 1).Find(&events)
			if len(events) > timelineLimit {
				events = events[:timelineLimit]
				room.Timeline.Limited = true
			}
			// Reverse to ascending order
			for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
				events[i], events[j] = events[j], events[i]
			}
			if len(events) > 0 {
				// Use created_at for token generation to match the created_at-based query filter
				tokenTS := events[0].CreatedAt.UnixMilli()
				room.Timeline.PrevBatch = s.generatePaginationToken(tokenTS)
			}
			// Include full state for newly joined rooms
			if len(room.State.Events) == 0 {
				stateEvents, _ := s.HS.Room.GetRoomStateAll(roomID)
				room.State.Events = stateEvents
			}
		} else {
			// 增量同步：返回 since 之后的所有事件（按时间升序）
			// Use created_at (local receive time) for the "since" filter to correctly
			// capture federation events whose origin_server_ts may predate our sync token
			sinceTime := time.UnixMilli(since)
			db.GetDB().Where("room_id = ? AND created_at > ?", roomID, sinceTime).
				Order("origin_server_ts ASC").Limit(timelineLimit + 1).Find(&events)
			if len(events) > timelineLimit {
				events = events[:timelineLimit]
				room.Timeline.Limited = true
								tokenTS := events[0].CreatedAt.UnixMilli()
				room.Timeline.PrevBatch = s.generatePaginationToken(tokenTS)
			}
		}
	} else {
		// 初始同步：返回最近的 50 个事件，按时间升序排列
		db.GetDB().Where("room_id = ?", roomID).
			Order("origin_server_ts DESC").Limit(timelineLimit + 1).Find(&events)
		if len(events) > timelineLimit {
			events = events[:timelineLimit]
			room.Timeline.Limited = true
		}
		// 反转为时间升序
		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}
		if len(events) > 0 {
			// For initial sync, prev_batch represents the state after the whole batch
			// so use the last event's timestamp to ensure /members?at= includes all state
						tokenTS := events[len(events)-1].CreatedAt.UnixMilli()
			room.Timeline.PrevBatch = s.generatePaginationToken(tokenTS)
		}
	}

	// Get user's membership history for unsigned.membership
	userJoinTS := s.getUserJoinTS(roomID, userID)

	room.Timeline.Events = make([]*ClientEvent, 0, len(events))
	for _, event := range events {
		ce := event.ToClientEventForDevice(deviceID)
		// Add unsigned.membership per MSC4115
		membership := "join" // default for joined rooms
		if userJoinTS > 0 && event.OriginServerTS < userJoinTS {
			membership = "leave"
		}
		if ce.Unsigned == nil {
			ce.Unsigned = map[string]interface{}{"membership": membership}
		} else {
			ce.Unsigned["membership"] = membership
		}
		room.Timeline.Events = append(room.Timeline.Events, ce)
	}
	// Apply timeline type filter
	if timelineFilter != nil {
		room.Timeline.Events = filterEvents(room.Timeline.Events, timelineFilter)
	}
	
	// 获取临时事件（typing, receipts）
	room.Ephemeral.Events = s.getEphemeralEvents(roomID, since)
	
	// 获取房间账户数据
	room.AccountData.Events = s.getRoomAccountData(userID, roomID)
	
	// 获取未读通知数
	// Check if the filter requests per-thread unread notifications
	wantThreadNotifs := false
	if filter != nil && filter.Room != nil && filter.Room.Timeline != nil &&
		filter.Room.Timeline.UnreadThreadNotifications != nil && *filter.Room.Timeline.UnreadThreadNotifications {
		wantThreadNotifs = true
	}
	if wantThreadNotifs {
		// When thread filter is active, unread_notifications counts only main-timeline events
		room.UnreadNotifications = s.getUnreadNotificationsMainTimeline(roomID, userID)
		room.UnreadThreadNotifications = s.getUnreadThreadNotifications(roomID, userID)
	} else {
		// Without thread filter, count all events and do NOT include unread_thread_notifications
		room.UnreadNotifications = s.getUnreadNotifications(roomID, userID)
		room.UnreadThreadNotifications = nil
	}
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
func (s *SyncService) buildLeftRoom(roomID, userID string, since int64, filter *parsedFilter) *LeftRoom {
	room := &LeftRoom{
		State:    &SyncState{Events: make([]*ClientEvent, 0)},
		Timeline: &SyncTimeline{Events: make([]*ClientEvent, 0)},
	}

	var timelineFilter *parsedEventFilter
	var stateFilter *parsedEventFilter
	if filter != nil && filter.Room != nil {
		timelineFilter = filter.Room.Timeline
		stateFilter = filter.Room.State
	}

	// Resolve timeline limit
	timelineLimit := 20
	if timelineFilter != nil && timelineFilter.Limit != nil {
		timelineLimit = *timelineFilter.Limit
		if timelineLimit < 0 {
			timelineLimit = 0
		}
	}

	// Find the user's leave event to determine the cutoff point
	var member MatrixRoomMember
	if err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error; err != nil {
		return room
	}

	var leaveTS int64
	if member.EventID != "" {
		var leaveEvent MatrixEvent
		if db.GetDB().Where("event_id = ?", member.EventID).First(&leaveEvent).Error == nil {
			leaveTS = leaveEvent.OriginServerTS
		}
	}

	// Get timeline events up to and including the leave event
	if leaveTS > 0 && timelineLimit > 0 {
		var events []MatrixEvent
		query := db.GetDB().Where("room_id = ? AND origin_server_ts <= ?", roomID, leaveTS)
		if since > 0 {
			// Incremental sync: only events since the last sync token
			sinceTime := time.UnixMilli(since)
			query = query.Where("created_at > ?", sinceTime)
		}
		query.Order("origin_server_ts DESC, depth DESC").Limit(timelineLimit).Find(&events)
		// Reverse to chronological order
		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}
		for i := range events {
			room.Timeline.Events = append(room.Timeline.Events, events[i].ToClientEvent())
		}
		// Apply timeline type filter
		if timelineFilter != nil {
			room.Timeline.Events = filterEvents(room.Timeline.Events, timelineFilter)
		}
	}

	// Per Matrix spec: when timeline has events, state block should be empty
	// (state is inferred from timeline). State is only populated when timeline is empty.
	if len(room.Timeline.Events) == 0 {
		var stateEntries []MatrixRoomState
		db.GetDB().Where("room_id = ?", roomID).Find(&stateEntries)
		for _, entry := range stateEntries {
			var evt MatrixEvent
			if db.GetDB().Where("event_id = ?", entry.EventID).First(&evt).Error == nil {
				if leaveTS == 0 || evt.OriginServerTS <= leaveTS {
					room.State.Events = append(room.State.Events, evt.ToClientEvent())
				}
			}
		}
		// Apply state filter
		if stateFilter != nil {
			room.State.Events = filterEvents(room.State.Events, stateFilter)
		}
	}

	return room
}

// ===== 辅助函数 =====

// getUserJoinTS returns the timestamp when the user joined the room (0 if not found)
func (s *SyncService) getUserJoinTS(roomID, userID string) int64 {
	var member MatrixRoomMember
	if err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error; err != nil {
		return 0
	}
	if member.EventID == "" {
		return 0
	}
	var event MatrixEvent
	if err := db.GetDB().Where("event_id = ?", member.EventID).First(&event).Error; err != nil {
		return 0
	}
	return event.OriginServerTS
}

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

// getPresenceEvents 获取在线状态事件（自己和房间成员）
// since: for incremental sync, only return presence updated after this timestamp
func (s *SyncService) getPresenceEvents(userID string, joinedRooms []string, since int64) *SyncPresence {
	userSet := map[string]bool{userID: true}
	if len(joinedRooms) > 0 {
		var members []MatrixRoomMember
		db.GetDB().Where("room_id IN ? AND membership = ?", joinedRooms, MembershipJoin).Find(&members)
		for _, m := range members {
			userSet[m.UserID] = true
		}
	}
	userIDs := make([]string, 0, len(userSet))
	for uid := range userSet {
		userIDs = append(userIDs, uid)
	}

	var presences []MatrixPresence
	if since > 0 {
		// Incremental sync: return presence for users whose presence changed
		// OR who newly joined a room shared with the caller since last sync.
		newJoinUserIDs := make(map[string]bool)
		if len(joinedRooms) > 0 {
			var memberEvents []MatrixEvent
			sinceTimeForPresence := time.UnixMilli(since)
			db.GetDB().Where("room_id IN ? AND event_type = ? AND created_at > ?",
				joinedRooms, string(EventTypeRoomMember), sinceTimeForPresence).Find(&memberEvents)
			for _, evt := range memberEvents {
				if evt.StateKey == nil {
					continue
				}
				if m, ok := evt.Content["membership"].(string); ok && m == "join" {
					if *evt.StateKey != userID {
						newJoinUserIDs[*evt.StateKey] = true
					}
				}
			}
		}
		// Query presence for changed OR newly-visible users
		if len(newJoinUserIDs) > 0 {
			newJoinIDs := make([]string, 0, len(newJoinUserIDs))
			for uid := range newJoinUserIDs {
				newJoinIDs = append(newJoinIDs, uid)
			}
			db.GetDB().Where("(user_id IN ? AND last_active_ts > ?) OR user_id IN ?",
				userIDs, since, newJoinIDs).Find(&presences)
		} else {
			db.GetDB().Where("user_id IN ? AND last_active_ts > ?", userIDs, since).Find(&presences)
		}
	} else {
		db.GetDB().Where("user_id IN ?", userIDs).Find(&presences)
	}

	events := make([]*ClientEvent, 0, len(presences))
	now := time.Now().UnixMilli()
	for _, p := range presences {
		content := map[string]interface{}{
			"presence":        p.Presence,
			"last_active_ago": now - p.LastActiveTS,
		}
		if p.StatusMsg != nil {
			content["status_msg"] = *p.StatusMsg
		}
		events = append(events, &ClientEvent{
			Type:    "m.presence",
			Content: content,
			Sender:  p.UserID,
		})
	}
	return &SyncPresence{Events: events}
}

// getIgnoredUsers returns the set of ignored user IDs from account data
func (s *SyncService) getIgnoredUsers(userID string) map[string]bool {
	var data MatrixAccountData
	if err := db.GetDB().Where("user_id = ? AND data_type = ? AND room_id IS NULL",
		userID, "m.ignored_user_list").First(&data).Error; err != nil {
		return nil
	}
	result := make(map[string]bool)
	if ignoredMap, ok := data.Content["ignored_users"].(map[string]interface{}); ok {
		for uid := range ignoredMap {
			result[uid] = true
		}
	}
	return result
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
// For full sync (since==0): excludes forgotten rooms.
// For incremental sync (since>0): also includes forgotten rooms where the leave event happened after 'since'.
func (s *SyncService) getLeftRooms(userID string, since int64) []string {
	var members []MatrixRoomMember
	db.GetDB().Where("user_id = ? AND (membership = ? OR membership = ?)",
		userID, MembershipLeave, MembershipBan).Find(&members)

	var roomIDs []string
	for _, m := range members {
		if m.Forgotten {
			if since <= 0 {
				// Full sync: skip forgotten rooms
				continue
			}
			// Incremental sync: include forgotten room only if leave event is newer than since
			var event MatrixEvent
			if db.GetDB().Where("event_id = ?", m.EventID).First(&event).Error == nil {
				if event.OriginServerTS > since {
					roomIDs = append(roomIDs, m.RoomID)
				}
			}
		} else {
			if since > 0 {
				// Incremental sync: only include rooms left since the since token
				var event MatrixEvent
				if db.GetDB().Where("event_id = ?", m.EventID).First(&event).Error == nil {
					if event.OriginServerTS > since {
						roomIDs = append(roomIDs, m.RoomID)
					}
				}
			} else {
				roomIDs = append(roomIDs, m.RoomID)
			}
		}
	}
	return roomIDs
}

// getChangedStateEvents 获取变更的状态事件
func (s *SyncService) getChangedStateEvents(roomID string, since int64) []*ClientEvent {
	var events []MatrixEvent
	sinceTime := time.UnixMilli(since)
	db.GetDB().Where("room_id = ? AND state_key IS NOT NULL AND created_at > ?", roomID, sinceTime).
		Order("origin_server_ts ASC").Find(&events)
	
	result := make([]*ClientEvent, len(events))
	for i, event := range events {
		result[i] = event.ToClientEvent()
	}
	return result
}

// getEphemeralEvents 获取临时事件
// since: only return receipts newer than this timestamp (0 = return all)
func (s *SyncService) getEphemeralEvents(roomID string, since int64) []*ClientEvent {
	events := make([]*ClientEvent, 0)

	// Always include m.typing event (even with empty user_ids) so clients
	// get notified when typing stops. The sync notifier wakes up long-pollers
	// on typing changes, so this event will appear in the next sync response.
	typing := s.getTypingUsers(roomID)
	if typing == nil {
		typing = []string{}
	}
	events = append(events, &ClientEvent{
		Type: "m.typing",
		Content: map[string]interface{}{
			"user_ids": typing,
		},
	})

	// 获取 receipts（only those newer than since for incremental sync）
	receipts := s.getReceipts(roomID, since)
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
// since: only return receipts newer than this timestamp (0 = return all)
func (s *SyncService) getReceipts(roomID string, since int64) map[string]interface{} {
	var receipts []MatrixReceipt
	query := db.GetDB().Where("room_id = ?", roomID)
	if since > 0 {
		query = query.Where("ts > ?", since)
	}
	query.Find(&receipts)
	
	// Per MSC4102: when both unthreaded and threaded receipts exist for the same
	// user+event+type, prefer the unthreaded receipt (no thread_id).
	// Two-pass approach: first collect unthreaded receipts, then add threaded ones
	// only if no unthreaded receipt exists for the same user+event+type.

	// Track which user+event+type combinations have unthreaded receipts
	type receiptKey struct {
		eventID     string
		receiptType string
		userID      string
	}
	unthreadedSet := make(map[receiptKey]bool)

	// First pass: identify unthreaded receipts
	for _, r := range receipts {
		if r.ThreadID == "" {
			unthreadedSet[receiptKey{r.EventID, r.ReceiptType, r.UserID}] = true
		}
	}

	result := make(map[string]interface{})
	for _, r := range receipts {
		// Skip threaded receipts when an unthreaded one exists for same user+event+type
		if r.ThreadID != "" {
			key := receiptKey{r.EventID, r.ReceiptType, r.UserID}
			if unthreadedSet[key] {
				continue
			}
		}

		if _, ok := result[r.EventID]; !ok {
			result[r.EventID] = make(map[string]interface{})
		}
		eventReceipts := result[r.EventID].(map[string]interface{})
		if _, ok := eventReceipts[r.ReceiptType]; !ok {
			eventReceipts[r.ReceiptType] = make(map[string]interface{})
		}
		typeReceipts := eventReceipts[r.ReceiptType].(map[string]interface{})

		userData := map[string]interface{}{
			"ts": r.TS,
		}
		if r.ThreadID != "" {
			userData["thread_id"] = r.ThreadID
		}
		typeReceipts[r.UserID] = userData
	}
	
	return result
}

// getAccountData 获取账户数据
func (s *SyncService) getAccountData(userID string, since int64) *SyncAccountData {
	var accountData []MatrixAccountData
	query := db.GetDB().Where("user_id = ?", userID).Where("room_id IS NULL")
	if since > 0 {
		// Incremental sync: only return account data changed since last sync
		sinceTime := time.UnixMilli(since)
		query = query.Where("updated_at > ?", sinceTime)
	}
	query.Find(&accountData)

	events := make([]*ClientEvent, 0, len(accountData)+1)
	for _, data := range accountData {
		events = append(events, &ClientEvent{
			Type:    data.DataType,
			Content: data.Content,
		})
	}

	if since == 0 {
		// Initial sync: always include m.push_rules
		hasPushRules := false
		for _, data := range accountData {
			if data.DataType == "m.push_rules" {
				hasPushRules = true
				break
			}
		}
		if !hasPushRules {
			if rules, err := s.HS.Push.GetPushRules(userID); err == nil {
				rulesContent := map[string]interface{}{
					"global": rules.Global,
				}
				events = append(events, &ClientEvent{
					Type:    "m.push_rules",
					Content: rulesContent,
				})
			}
		}
	} else {
		// Incremental sync: include m.push_rules only if any push rules changed
		sinceTime := time.UnixMilli(since)
		var pushRuleCount int64
		db.GetDB().Model(&PushRule{}).Where("user_id = ? AND updated_at > ?", userID, sinceTime).Count(&pushRuleCount)
		if pushRuleCount > 0 {
			if rules, err := s.HS.Push.GetPushRules(userID); err == nil {
				rulesContent := map[string]interface{}{
					"global": rules.Global,
				}
				events = append(events, &ClientEvent{
					Type:    "m.push_rules",
					Content: rulesContent,
				})
			}
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

// getLastReadTS returns the timestamp of the latest m.read receipt event for the user.
// If threadScope is non-empty, only considers receipts with that thread_id.
// If threadScope is empty, considers all receipts (any thread).
func (s *SyncService) getLastReadTS(roomID, userID, threadScope string) int64 {
	var receipt MatrixReceipt
	query := db.GetDB().Where("room_id = ? AND user_id = ? AND receipt_type = ?",
		roomID, userID, "m.read")
	if threadScope != "" {
		query = query.Where("thread_id = ?", threadScope)
	} else {
		query = query.Where("thread_id = '' OR thread_id IS NULL")
	}
	if err := query.Order("ts DESC").First(&receipt).Error; err == nil {
		var evt MatrixEvent
		if db.GetDB().Where("event_id = ?", receipt.EventID).First(&evt).Error == nil {
			return evt.OriginServerTS
		}
	}
	return 0
}

// isThreadEvent checks if an event is a thread reply (has m.relates_to with rel_type=m.thread)
func isThreadEvent(evt *MatrixEvent) (bool, string) {
	relatesTo := extractRelatesToMap(evt.Content)
	if relatesTo == nil {
		return false, ""
	}
	relType, _ := relatesTo["rel_type"].(string)
	if relType != "m.thread" {
		return false, ""
	}
	threadID, _ := relatesTo["event_id"].(string)
	return threadID != "", threadID
}

// isAnnotation checks if an event is an annotation/reaction (rel_type=m.annotation)
func isAnnotation(evt *MatrixEvent) bool {
	relatesTo := extractRelatesToMap(evt.Content)
	if relatesTo == nil {
		return false
	}
	relType, _ := relatesTo["rel_type"].(string)
	return relType == "m.annotation"
}

// extractRelatesToMap robustly extracts m.relates_to as map[string]interface{}.
// GORM's JSON serializer may decode nested objects as json.RawMessage or other types.
func extractRelatesToMap(content map[string]interface{}) map[string]interface{} {
	if content == nil {
		return nil
	}
	rt := content["m.relates_to"]
	if rt == nil {
		return nil
	}
	if m, ok := rt.(map[string]interface{}); ok {
		return m
	}
	// Handle json.RawMessage, []byte, or string
	var raw []byte
	switch v := rt.(type) {
	case json.RawMessage:
		raw = []byte(v)
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		b, err := json.Marshal(rt)
		if err != nil {
			return nil
		}
		raw = b
	}
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) == nil {
		return m
	}
	return nil
}

// shouldNotify determines if an event should count as a notification.
// Only m.room.message and m.room.encrypted events generate notifications,
// and annotations (reactions) are excluded.
func shouldNotify(evt *MatrixEvent) bool {
	if EventType(evt.EventType) != EventTypeRoomMessage && evt.EventType != "m.room.encrypted" {
		return false
	}
	if isAnnotation(evt) {
		return false
	}
	return true
}

// getUnreadNotifications 获取未读通知 (counts ALL notifiable events — main + threads)
// Per Matrix spec, each receipt only applies to its scope:
// - Unthreaded receipt: marks ALL events (main + threads) as read up to that point
// - Threaded receipt (thread_id="main"): only marks main-timeline events as read
// - Threaded receipt (thread_id=<event>): only marks that thread's events as read
func (s *SyncService) getUnreadNotifications(roomID, userID string) *UnreadNotifications {
	// Gather all receipts for this user in this room
	var receipts []MatrixReceipt
	db.GetDB().Where("room_id = ? AND user_id = ? AND receipt_type = ?",
		roomID, userID, "m.read").Find(&receipts)

	// Build a map of receipt scope -> event timestamp
	// Scopes: "" (unthreaded), "main", or a thread root event ID
	receiptTSByScope := make(map[string]int64)
	for _, r := range receipts {
		var evt MatrixEvent
		if db.GetDB().Where("event_id = ?", r.EventID).First(&evt).Error == nil {
			scope := r.ThreadID // "" for unthreaded, "main" for main, or thread root ID
			if evt.OriginServerTS > receiptTSByScope[scope] {
				receiptTSByScope[scope] = evt.OriginServerTS
			}
		}
	}

	unthreadedTS := receiptTSByScope[""]   // applies to ALL events
	mainTS := receiptTSByScope["main"]      // applies to main-timeline events only

	// Fetch all events from other senders
	var events []MatrixEvent
	db.GetDB().Where("room_id = ? AND sender != ?", roomID, userID).Find(&events)

	var notifCount int64
	var highlightCount int64
	for _, evt := range events {
		if !shouldNotify(&evt) {
			continue
		}

		// Determine which receipt applies to this event
		isThread, threadID := isThreadEvent(&evt)
		var effectiveReadTS int64
		if isThread {
			// Thread event: check thread-specific receipt and unthreaded receipt
			threadTS := receiptTSByScope[threadID]
			effectiveReadTS = unthreadedTS
			if threadTS > effectiveReadTS {
				effectiveReadTS = threadTS
			}
		} else {
			// Main timeline event: check "main" receipt and unthreaded receipt
			effectiveReadTS = unthreadedTS
			if mainTS > effectiveReadTS {
				effectiveReadTS = mainTS
			}
		}

		if effectiveReadTS > 0 && evt.OriginServerTS <= effectiveReadTS {
			continue // event is read
		}

		notifCount++
		if body, ok := evt.Content["body"].(string); ok {
			if strings.Contains(body, userID) {
				highlightCount++
			}
		}
	}

	return &UnreadNotifications{
		HighlightCount:    highlightCount,
		NotificationCount: notifCount,
	}
}

// getUnreadNotificationsMainTimeline counts only main-timeline events (not thread replies).
// Uses the "main" thread receipt and unthreaded receipt to determine read position.
func (s *SyncService) getUnreadNotificationsMainTimeline(roomID, userID string) *UnreadNotifications {
	// Per spec, use the best of: unthreaded receipt, or thread_id="main" receipt
	unthreadedTS := s.getLastReadTS(roomID, userID, "")
	mainTS := s.getLastReadTS(roomID, userID, "main")
	lastReadTS := unthreadedTS
	if mainTS > lastReadTS {
		lastReadTS = mainTS
	}

	// Get all events from other senders, filter by type in Go
	var events []MatrixEvent
	q := db.GetDB().Where("room_id = ? AND sender != ?", roomID, userID)
	if lastReadTS > 0 {
		q = q.Where("origin_server_ts > ?", lastReadTS)
	}
	q.Find(&events)

	var notifCount int64
	var highlightCount int64
	for _, evt := range events {
		// Skip thread replies — only count main-timeline events
		if isThread, _ := isThreadEvent(&evt); isThread {
			continue
		}
		if !shouldNotify(&evt) {
			continue
		}
		notifCount++
		if body, ok := evt.Content["body"].(string); ok {
			if strings.Contains(body, userID) {
				highlightCount++
			}
		}
	}

	return &UnreadNotifications{
		HighlightCount:    highlightCount,
		NotificationCount: notifCount,
	}
}

// getUnreadThreadNotifications returns per-thread unread notification counts
func (s *SyncService) getUnreadThreadNotifications(roomID, userID string) map[string]*UnreadNotifications {
	// Find all threaded receipts for this user in this room
	var receipts []MatrixReceipt
	db.GetDB().Where("room_id = ? AND user_id = ? AND receipt_type = ? AND thread_id != '' AND thread_id != 'main' AND thread_id IS NOT NULL",
		roomID, userID, "m.read").Find(&receipts)

	// Build a map of thread_id -> receipt event's timestamp
	threadReceiptTS := make(map[string]int64)
	for _, r := range receipts {
		var evt MatrixEvent
		if db.GetDB().Where("event_id = ?", r.EventID).First(&evt).Error == nil {
			threadReceiptTS[r.ThreadID] = evt.OriginServerTS
		}
	}

	// Per spec, an unthreaded receipt also marks all threads as read up to that point
	unthreadedTS := s.getLastReadTS(roomID, userID, "")

	// Find all events in this room from other users, filter by type in Go
	var allEvents []MatrixEvent
	db.GetDB().Where("room_id = ? AND sender != ?",
		roomID, userID).Find(&allEvents)

	// Group events by thread (using m.relates_to.event_id where rel_type=m.thread)
	threadCounts := make(map[string]*UnreadNotifications)
	for _, evt := range allEvents {
		if !shouldNotify(&evt) {
			continue
		}
		isThread, threadID := isThreadEvent(&evt)
		if !isThread {
			continue
		}

		// Check against thread-specific receipt
		receiptTS := threadReceiptTS[threadID]
		// Also check against unthreaded receipt (marks everything as read)
		if unthreadedTS > receiptTS {
			receiptTS = unthreadedTS
		}
		if receiptTS > 0 && evt.OriginServerTS <= receiptTS {
			continue
		}

		if threadCounts[threadID] == nil {
			threadCounts[threadID] = &UnreadNotifications{}
		}
		threadCounts[threadID].NotificationCount++

		// Check for highlights (mentions)
		if body, ok := evt.Content["body"].(string); ok {
			if strings.Contains(body, userID) {
				threadCounts[threadID].HighlightCount++
			}
		}
	}

	if len(threadCounts) == 0 {
		return nil
	}
	return threadCounts
}

// getDeviceListUpdates returns users whose device lists changed/left since the given timestamp.
// "changed" = users who joined rooms shared with userID since `since`
// "left" = users who left all shared rooms with userID since `since`
func (s *SyncService) getDeviceListUpdates(userID string, joinedRooms []string, since int64) (changed, left []string) {
	changed = make([]string, 0)
	left = make([]string, 0)
	if len(joinedRooms) == 0 {
		return
	}

	// Find m.room.member events in joined rooms since the last sync
	// Use created_at to correctly capture federation events
	sinceTimeForMembers := time.UnixMilli(since)
	var memberEvents []MatrixEvent
	db.GetDB().Where("room_id IN ? AND event_type = ? AND created_at > ? AND sender != ?",
		joinedRooms, string(EventTypeRoomMember), sinceTimeForMembers, userID).
		Find(&memberEvents)

	changedSet := make(map[string]bool)
	leftSet := make(map[string]bool)

	// Check if user newly joined any rooms since last sync — all existing members
	// in those rooms should appear in device_lists.changed
	for _, roomID := range joinedRooms {
		joinTS := s.getUserJoinTS(roomID, userID)
		if joinTS > since {
			// Newly joined room — add all existing members to changed
			var existingMembers []MatrixRoomMember
			db.GetDB().Where("room_id = ? AND membership = ? AND user_id != ?",
				roomID, MembershipJoin, userID).Find(&existingMembers)
			for _, m := range existingMembers {
				changedSet[m.UserID] = true
			}
		}
	}

	// Check rooms the user themselves left since last sync.
	// Users who were in those rooms and are no longer in ANY shared room → device_lists.left
	leftRooms := s.getLeftRooms(userID, since)
	if len(leftRooms) > 0 {
		// Build set of current joined room members for quick lookup
		currentRoomMemberSet := make(map[string]bool)
		for _, roomID := range joinedRooms {
			var members []MatrixRoomMember
			db.GetDB().Where("room_id = ? AND membership = ? AND user_id != ?",
				roomID, MembershipJoin, userID).Find(&members)
			for _, m := range members {
				currentRoomMemberSet[m.UserID] = true
			}
		}

		// For each left room, find users who were members
		for _, roomID := range leftRooms {
			var members []MatrixRoomMember
			db.GetDB().Where("room_id = ? AND user_id != ?", roomID, userID).Find(&members)
			for _, m := range members {
				// If this user is not in any currently joined room, they go in device_lists.left
				if !currentRoomMemberSet[m.UserID] {
					leftSet[m.UserID] = true
				}
			}
		}
	}

	for _, evt := range memberEvents {
		if evt.StateKey == nil {
			continue
		}
		targetUser := *evt.StateKey
		if targetUser == userID {
			continue
		}
		membership, _ := evt.Content["membership"].(string)
		if membership == "join" {
			changedSet[targetUser] = true
			delete(leftSet, targetUser)
		} else if membership == "leave" || membership == "ban" {
			// Only mark as left if no longer sharing any room with the local user
			var sharedCount int64
			db.GetDB().Model(&MatrixRoomMember{}).
				Where("user_id = ? AND membership = ? AND room_id IN ?",
					targetUser, MembershipJoin, joinedRooms).
				Count(&sharedCount)
			if sharedCount == 0 {
				leftSet[targetUser] = true
				delete(changedSet, targetUser)
			}
		}
	}

	// Also check for new device sessions created by room members since last sync
	var roomMembers []MatrixRoomMember
	db.GetDB().Where("room_id IN ? AND membership = ? AND user_id != ?",
		joinedRooms, MembershipJoin, userID).Find(&roomMembers)
	sinceTime := time.Unix(0, since*int64(time.Millisecond))
	roomMemberSet := make(map[string]bool)
	for _, m := range roomMembers {
		roomMemberSet[m.UserID] = true
		if changedSet[m.UserID] || leftSet[m.UserID] {
			continue
		}
		var count int64
		db.GetDB().Model(&MatrixUserSession{}).
			Where("user_id = ? AND created_at > ?", m.UserID, sinceTime).
			Count(&count)
		if count > 0 {
			changedSet[m.UserID] = true
		}
	}

	// Check DeviceListChange records for incoming federation device list updates
	var dlChanges []DeviceListChange
	db.GetDB().Where("changed_at > ?", since).Find(&dlChanges)
	for _, dlc := range dlChanges {
		if dlc.UserID == userID {
			continue
		}
		if !roomMemberSet[dlc.UserID] {
			continue
		}
		if dlc.ChangeType == "removed" {
			if !changedSet[dlc.UserID] {
				leftSet[dlc.UserID] = true
			}
		} else {
			changedSet[dlc.UserID] = true
			delete(leftSet, dlc.UserID)
		}
	}

	for u := range changedSet {
		changed = append(changed, u)
	}
	for u := range leftSet {
		left = append(left, u)
	}
	return
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
	markTypingChanged(roomID)
	// Wake up waiting syncs for all room members
	go s.HS.NotifyRoomEvent(roomID)
	return nil
}

// ===== Receipts =====

// SetReceipt 设置回执
func (s *SyncService) SetReceipt(roomID, eventID, userID, receiptType string) *MatrixError {
	return s.SetReceiptWithThread(roomID, eventID, userID, receiptType, "")
}

// SetReceiptWithThread 设置回执（支持 thread_id）
func (s *SyncService) SetReceiptWithThread(roomID, eventID, userID, receiptType, threadID string) *MatrixError {
	// 检查权限
	membership, _ := s.HS.Room.GetUserMembership(roomID, userID)
	if membership != MembershipJoin {
		return ErrForbidden
	}

	// 删除旧回执 (scoped by thread_id)
	if threadID != "" {
		db.GetDB().Where("room_id = ? AND user_id = ? AND receipt_type = ? AND thread_id = ?",
			roomID, userID, receiptType, threadID).Delete(&MatrixReceipt{})
	} else {
		db.GetDB().Where("room_id = ? AND user_id = ? AND receipt_type = ? AND (thread_id = '' OR thread_id IS NULL)",
			roomID, userID, receiptType).Delete(&MatrixReceipt{})
	}

	// 创建新回执
	receipt := &MatrixReceipt{
		RoomID:      roomID,
		EventID:     eventID,
		UserID:      userID,
		ReceiptType: receiptType,
		ThreadID:    threadID,
		TS:          time.Now().UnixMilli(),
	}

	if err := db.GetDB().Create(receipt).Error; err != nil {
		return ErrInternal
	}

	// Wake up waiting syncs for all room members
	go s.HS.NotifyRoomEvent(roomID)

	// Federate the receipt to remote servers in the room
	go s.federateReceipt(roomID, eventID, userID, receiptType, threadID, receipt.TS)

	return nil
}

// federateReceipt sends an m.receipt EDU to remote servers participating in the room
func (s *SyncService) federateReceipt(roomID, eventID, userID, receiptType, threadID string, ts int64) {
	// Only federate for local users
	_, domain, ok := ParseMatrixUserID(userID)
	if !ok || domain != s.HS.Config.ServerName {
		return
	}

	// Find remote servers in the room
	var members []MatrixRoomMember
	db.GetDB().Where("room_id = ? AND membership = ?", roomID, MembershipJoin).Find(&members)

	remoteServers := make(map[string]bool)
	for _, m := range members {
		_, d, ok := ParseMatrixUserID(m.UserID)
		if !ok || d == s.HS.Config.ServerName {
			continue
		}
		remoteServers[d] = true
	}

	if len(remoteServers) == 0 {
		return
	}

	// Build receipt EDU content
	userData := map[string]interface{}{
		"ts": ts,
	}
	if threadID != "" {
		userData["thread_id"] = threadID
	}

	eduContent := map[string]interface{}{
		roomID: map[string]interface{}{
			eventID: map[string]interface{}{
				receiptType: map[string]interface{}{
					userID: userData,
				},
			},
		},
	}

	edu := map[string]interface{}{
		"edu_type": "m.receipt",
		"content":  eduContent,
	}

	eduBytes, err := json.Marshal(edu)
	if err != nil {
		return
	}

	fedClient := NewFederationClient(s.HS)
	txnID := fmt.Sprintf("receipt-%d", time.Now().UnixNano())

	for serverName := range remoteServers {
		server := serverName
		go func() {
			_ = fedClient.SendTransaction(server, txnID, nil, []json.RawMessage{eduBytes})
		}()
	}
}
