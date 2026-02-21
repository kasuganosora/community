package homeserver

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
)

// EventService 事件服务
type EventService struct {
	HS *HomeServer
}

// NewEventService 创建事件服务
func NewEventService(hs *HomeServer) *EventService {
	return &EventService{HS: hs}
}

// ===== 发送事件 =====

// SendMessageRequest 发送消息请求
type SendMessageRequest struct {
	MsgType       string                 `json:"msgtype"`
	Body          string                 `json:"body"`
	Format        string                 `json:"format,omitempty"`
	FormattedBody string                 `json:"formatted_body,omitempty"`
	URL           string                 `json:"url,omitempty"`
	Info          map[string]interface{} `json:"info,omitempty"`
	RelatesTo     *RelatesTo             `json:"m.relates_to,omitempty"`
	NewContent    map[string]interface{} `json:"m.new_content,omitempty"`
}

// RelatesTo 关联信息
type RelatesTo struct {
	RelType string `json:"rel_type"`
	EventID string `json:"event_id"`
	Key     string `json:"key,omitempty"`
}

// SendMessageResponse 发送消息响应
type SendMessageResponse struct {
	EventID string `json:"event_id"`
}

// SendRoomEvent 发送房间事件
func (e *EventService) SendRoomEvent(roomID, sender, eventType string, txnID *string, content map[string]interface{}, stateKey *string) (*SendMessageResponse, *MatrixError) {
	// 检查用户是否在房间中
	membership, _ := e.HS.Room.GetUserMembership(roomID, sender)
	if membership != MembershipJoin {
		return nil, ErrForbidden
	}
	
	// 检查权限（对于状态事件）
	if stateKey != nil {
		if !e.canUserSendStateEvent(roomID, sender, EventType(eventType)) {
			return nil, ErrForbidden
		}
	} else {
		// 检查是否可以发送消息
		if !e.canUserSendEvent(roomID, sender, EventType(eventType)) {
			return nil, ErrForbidden
		}
	}
	
	// 获取前序事件
	prevEvents := e.getPrevEvents(roomID)
	
	// 生成事件 ID
	eventID := "$" + uuid.New().String()
	if txnID != nil {
		// 使用 txnID 生成确定性事件 ID
		eventID = fmt.Sprintf("$%s:%s", *txnID, e.HS.Config.ServerName)
	}
	
	// 检查是否已存在（幂等性）
	var existing MatrixEvent
	if err := db.GetDB().Where("event_id = ?", eventID).First(&existing).Error; err == nil {
		return &SendMessageResponse{EventID: eventID}, nil
	}
	
	// 获取深度
	depth := e.HS.Room.getNextDepth(db.GetDB(), roomID)
	
	// 创建事件
	event := &MatrixEvent{
		EventID:        eventID,
		RoomID:         roomID,
		EventType:      EventType(eventType),
		Sender:         sender,
		StateKey:       stateKey,
		Content:        content,
		PrevEvents:     prevEvents,
		Depth:          depth,
		OriginServerTS: time.Now().UnixMilli(),
		SenderDomain:   e.HS.Config.ServerName,
	}
	
	// 开始事务
	tx := db.GetDB().Begin()
	if err := tx.Create(event).Error; err != nil {
		tx.Rollback()
		return nil, ErrInternal
	}
	
	// 如果是状态事件，更新状态
	if stateKey != nil {
		e.HS.Room.updateRoomState(tx, roomID, event)
	}
	
	// 更新房间最后事件
	tx.Model(&MatrixRoom{}).Where("room_id = ?", roomID).Updates(map[string]interface{}{
		"last_event_id": eventID,
		"last_event_ts": event.OriginServerTS,
	})
	
	// 处理特殊事件类型
	switch EventType(eventType) {
	case EventTypeRoomMember:
		e.handleMemberEvent(tx, event)
	case EventTypeRoomRedaction:
		e.handleRedactionEvent(tx, event)
	}
	
	if err := tx.Commit().Error; err != nil {
		return nil, ErrInternal
	}
	
	// 存储事件关系（异步）
	go func() {
		if e.HS.Relations != nil {
			e.HS.Relations.StoreRelation(event)
		}
	}()

	// 触发推送通知（异步）
	go func() {
		if e.HS.Push != nil {
			var room MatrixRoom
			db.GetDB().Where("room_id = ?", roomID).First(&room)
			e.HS.Push.NotifyEvent(event, &room)
		}
	}()
	
	return &SendMessageResponse{EventID: eventID}, nil
}

// ===== 发送消息 =====

// SendRoomMessage 发送房间消息
func (e *EventService) SendRoomMessage(roomID, sender, txnID *string, content *SendMessageRequest) (*SendMessageResponse, *MatrixError) {
	// 转换为 map
	contentMap := map[string]interface{}{
		"msgtype": content.MsgType,
		"body":    content.Body,
	}
	
	if content.Format != "" {
		contentMap["format"] = content.Format
		contentMap["formatted_body"] = content.FormattedBody
	}
	if content.URL != "" {
		contentMap["url"] = content.URL
	}
	if content.Info != nil {
		contentMap["info"] = content.Info
	}
	if content.RelatesTo != nil {
		contentMap["m.relates_to"] = content.RelatesTo
	}
	if content.NewContent != nil {
		contentMap["m.new_content"] = content.NewContent
	}
	
	// 解引用
	rID := ""
	sID := ""
	if roomID != nil {
		rID = *roomID
	}
	if sender != nil {
		sID = *sender
	}
	
	return e.SendRoomEvent(rID, sID, string(EventTypeRoomMessage), txnID, contentMap, nil)
}

// ===== 获取事件 =====

// GetEvent 获取单个事件
func (e *EventService) GetEvent(eventID string) (*ClientEvent, *MatrixError) {
	var event MatrixEvent
	err := db.GetDB().Where("event_id = ?", eventID).First(&event).Error
	if err != nil {
		return nil, ErrNotFound
	}
	
	return event.ToClientEvent(), nil
}

// GetRoomEvent 获取房间事件
func (e *EventService) GetRoomEvent(roomID, eventID string) (*ClientEvent, *MatrixError) {
	var event MatrixEvent
	err := db.GetDB().Where("room_id = ? AND event_id = ?", roomID, eventID).First(&event).Error
	if err != nil {
		return nil, ErrNotFound
	}
	
	return event.ToClientEvent(), nil
}

// GetRoomMessagesRequest 获取房间消息请求
type GetRoomMessagesRequest struct {
	Dir     string `form:"dir"` // f, b
	From    string `form:"from"`
	To      string `form:"to"`
	Limit   int    `form:"limit"`
	Filter  string `form:"filter"`
}

// RoomMessagesResponse 消息响应
type RoomMessagesResponse struct {
	Chunk  []*ClientEvent `json:"chunk"`
	Start  string         `json:"start"`
	End    string         `json:"end"`
}

// GetRoomMessages 获取房间消息
func (e *EventService) GetRoomMessages(roomID, userID string, req *GetRoomMessagesRequest) (*RoomMessagesResponse, *MatrixError) {
	// 检查用户权限
	membership, _ := e.HS.Room.GetUserMembership(roomID, userID)
	if membership != MembershipJoin {
		return nil, ErrForbidden
	}
	
	limit := req.Limit
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	
	query := db.GetDB().Model(&MatrixEvent{}).Where("room_id = ?", roomID)
	
	// 方向
	if req.Dir == "b" {
		query = query.Order("origin_server_ts DESC")
	} else {
		query = query.Order("origin_server_ts ASC")
	}
	
	// 分页
	if req.From != "" {
		// 解析 from token
		fromTS := e.parsePaginationToken(req.From)
		if fromTS > 0 {
			if req.Dir == "b" {
				query = query.Where("origin_server_ts < ?", fromTS)
			} else {
				query = query.Where("origin_server_ts > ?", fromTS)
			}
		}
	}
	
	var events []MatrixEvent
	query.Limit(limit).Find(&events)
	
	chunk := make([]*ClientEvent, len(events))
	for i, event := range events {
		chunk[i] = event.ToClientEvent()
	}
	
	// 生成分页 token
	start := req.From
	var end string
	if len(events) > 0 {
		end = e.generatePaginationToken(events[len(events)-1].OriginServerTS)
	}
	
	return &RoomMessagesResponse{
		Chunk: chunk,
		Start: start,
		End:   end,
	}, nil
}

// GetRoomContext 获取事件上下文
func (e *EventService) GetRoomContext(roomID, eventID, userID string, limit int) (*EventContextResponse, *MatrixError) {
	// 检查权限
	membership, _ := e.HS.Room.GetUserMembership(roomID, userID)
	if membership != MembershipJoin {
		return nil, ErrForbidden
	}
	
	// 获取目标事件
	var targetEvent MatrixEvent
	err := db.GetDB().Where("room_id = ? AND event_id = ?", roomID, eventID).First(&targetEvent).Error
	if err != nil {
		return nil, ErrNotFound
	}
	
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	
	// 获取之前的事件
	var eventsBefore []MatrixEvent
	db.GetDB().Where("room_id = ? AND origin_server_ts < ?", roomID, targetEvent.OriginServerTS).
		Order("origin_server_ts DESC").Limit(limit).Find(&eventsBefore)
	
	// 获取之后的事件
	var eventsAfter []MatrixEvent
	db.GetDB().Where("room_id = ? AND origin_server_ts > ?", roomID, targetEvent.OriginServerTS).
		Order("origin_server_ts ASC").Limit(limit).Find(&eventsAfter)
	
	// 获取房间状态
	state, _ := e.HS.Room.GetRoomStateAll(roomID)
	
	return &EventContextResponse{
		Event:          targetEvent.ToClientEvent(),
		EventsBefore:   e.eventsToClientEvents(eventsBefore),
		EventsAfter:    e.eventsToClientEvents(eventsAfter),
		State:          state,
	}, nil
}

// EventContextResponse 事件上下文响应
type EventContextResponse struct {
	Event        *ClientEvent   `json:"event"`
	EventsBefore []*ClientEvent `json:"events_before"`
	EventsAfter  []*ClientEvent `json:"events_after"`
	State        []*ClientEvent `json:"state"`
}

// ===== 删除事件 =====

// RedactEvent 删除事件
func (e *EventService) RedactEvent(roomID, eventID, userID, reason string, txnID *string) (*SendMessageResponse, *MatrixError) {
	// 检查权限
	if !e.canUserRedact(roomID, userID) {
		return nil, ErrForbidden
	}
	
	// 获取原事件
	var targetEvent MatrixEvent
	err := db.GetDB().Where("room_id = ? AND event_id = ?", roomID, eventID).First(&targetEvent).Error
	if err != nil {
		return nil, ErrNotFound
	}
	
	// 创建删除事件
	redactEventID := "$" + uuid.New().String()
	if txnID != nil {
		redactEventID = fmt.Sprintf("$%s_redact:%s", *txnID, e.HS.Config.ServerName)
	}
	
	redactEvent := &MatrixEvent{
		EventID:        redactEventID,
		RoomID:         roomID,
		EventType:      EventTypeRoomRedaction,
		Sender:         userID,
		Content: map[string]interface{}{
			"reason": reason,
		},
		Redacts:        &eventID,
		Depth:          e.HS.Room.getNextDepth(db.GetDB(), roomID),
		OriginServerTS: time.Now().UnixMilli(),
	}
	
	if err := db.GetDB().Create(redactEvent).Error; err != nil {
		return nil, ErrInternal
	}
	
	// 标记原事件为已删除（保留结构）
	db.GetDB().Model(&targetEvent).Update("content", map[string]interface{}{
		"__redacted_because__": redactEvent.ToClientEvent(),
	})
	
	return &SendMessageResponse{EventID: redactEventID}, nil
}

// ===== 权限检查 =====

// canUserSendEvent 检查是否可以发送事件
func (e *EventService) canUserSendEvent(roomID, userID string, eventType EventType) bool {
	pl, err := e.HS.Room.getRoomPowerLevels(roomID)
	if err != nil {
		return false
	}
	
	userPL := e.HS.Room.getUserPowerLevel(pl, userID)
	
	// 检查事件级别
	if level, ok := pl.Events[string(eventType)]; ok {
		return userPL >= level
	}
	
	return userPL >= pl.EventsDefault
}

// canUserSendStateEvent 检查是否可以发送状态事件
func (e *EventService) canUserSendStateEvent(roomID, userID string, eventType EventType) bool {
	pl, err := e.HS.Room.getRoomPowerLevels(roomID)
	if err != nil {
		return false
	}
	
	userPL := e.HS.Room.getUserPowerLevel(pl, userID)
	
	// 检查事件级别
	if level, ok := pl.Events[string(eventType)]; ok {
		return userPL >= level
	}
	
	return userPL >= pl.StateDefault
}

// canUserRedact 检查是否可以删除
func (e *EventService) canUserRedact(roomID, userID string) bool {
	pl, err := e.HS.Room.getRoomPowerLevels(roomID)
	if err != nil {
		return false
	}
	
	userPL := e.HS.Room.getUserPowerLevel(pl, userID)
	return userPL >= pl.Redact
}

// ===== 辅助函数 =====

// getPrevEvents 获取前序事件
func (e *EventService) getPrevEvents(roomID string) []string {
	var events []MatrixEvent
	db.GetDB().Where("room_id = ?", roomID).
		Order("depth DESC").Limit(10).Find(&events)
	
	prevEvents := make([]string, len(events))
	for i, event := range events {
		prevEvents[i] = event.EventID
	}
	
	return prevEvents
}

// handleMemberEvent 处理成员事件
func (e *EventService) handleMemberEvent(tx *gorm.DB, event *MatrixEvent) {
	if event.StateKey == nil {
		return
	}
	
	userID := *event.StateKey
	membership, ok := event.Content["membership"].(string)
	if !ok {
		return
	}
	
	e.HS.Room.updateRoomMember(tx, event.RoomID, userID, MembershipState(membership), event.EventID)
}

// handleRedactionEvent 处理删除事件
func (e *EventService) handleRedactionEvent(tx *gorm.DB, event *MatrixEvent) {
	// 已在 RedactEvent 中处理
}

// parsePaginationToken 解析分页 token
func (e *EventService) parsePaginationToken(token string) int64 {
	var ts int64
	fmt.Sscanf(token, "t%d", &ts)
	return ts
}

// generatePaginationToken 生成分页 token
func (e *EventService) generatePaginationToken(ts int64) string {
	return fmt.Sprintf("t%d", ts)
}

// eventsToClientEvents 转换事件列表
func (e *EventService) eventsToClientEvents(events []MatrixEvent) []*ClientEvent {
	result := make([]*ClientEvent, len(events))
	for i, event := range events {
		result[i] = event.ToClientEvent()
	}
	return result
}

// GetEventByTxnID 通过事务ID获取事件
func (e *EventService) GetEventByTxnID(roomID, userID, txnID string) (*ClientEvent, *MatrixError) {
	eventID := fmt.Sprintf("$%s:%s", txnID, e.HS.Config.ServerName)
	return e.GetEvent(eventID)
}

// GetRoomStateEvent 获取房间状态事件
func (e *EventService) GetRoomStateEvent(roomID, eventType, stateKey string) (*ClientEvent, *MatrixError) {
	var state MatrixRoomState
	err := db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, eventType, stateKey).First(&state).Error
	if err != nil {
		return nil, ErrNotFound
	}
	
	return &ClientEvent{
		EventID:  state.EventID,
		RoomID:   state.RoomID,
		Type:     string(state.EventType),
		StateKey: &state.StateKey,
		Content:  state.Content,
	}, nil
}
