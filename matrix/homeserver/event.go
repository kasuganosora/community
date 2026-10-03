package homeserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"gorm.io/gorm"
)

// EventService 事件服务.
type EventService struct {
	HS *HomeServer
}

// NewEventService 创建事件服务.
func NewEventService(hs *HomeServer) *EventService {
	return &EventService{HS: hs}
}

// ===== 发送事件 =====

// SendMessageRequest 发送消息请求.
type SendMessageRequest struct {
	MsgType       string         `json:"msgtype"`
	Body          string         `json:"body"`
	Format        string         `json:"format,omitempty"`
	FormattedBody string         `json:"formatted_body,omitempty"`
	URL           string         `json:"url,omitempty"`
	Info          map[string]any `json:"info,omitempty"`
	RelatesTo     *RelatesTo     `json:"m.relates_to,omitempty"`
	NewContent    map[string]any `json:"m.new_content,omitempty"`
}

// RelatesTo 关联信息.
type RelatesTo struct {
	RelType string `json:"rel_type"`
	EventID string `json:"event_id"`
	Key     string `json:"key,omitempty"`
}

// SendMessageResponse 发送消息响应.
type SendMessageResponse struct {
	EventID string `json:"event_id"`
}

// SendRoomEventFromDevice 发送房间事件（带设备 ID，用于记录 transaction_id）.
func (e *EventService) SendRoomEventFromDevice(roomID, sender, senderDeviceID, eventType string, txnID *string, content map[string]any, stateKey *string) (*SendMessageResponse, *MatrixError) {
	// Scope txnID to device for event ID generation (prevents cross-device collisions)
	deviceScopedTxnID := txnID

	if txnID != nil && senderDeviceID != "" {
		scoped := senderDeviceID + ":" + *txnID
		deviceScopedTxnID = &scoped
	}

	resp, err := e.SendRoomEvent(roomID, sender, eventType, deviceScopedTxnID, content, stateKey)
	if err != nil {
		return nil, err
	}
	// Store the original (non-scoped) txnID and device ID for unsigned.transaction_id
	if senderDeviceID != "" && txnID != nil {
		db.GetDB().Model(&MatrixEvent{}).Where("event_id = ?", resp.EventID).
			Updates(map[string]any{
				"sender_device_id": senderDeviceID,
				"txn_id":           *txnID,
			})
	}

	return resp, nil
}

// SendRoomEvent 发送房间事件.
func (e *EventService) SendRoomEvent(roomID, sender, eventType string, txnID *string, content map[string]any, stateKey *string) (*SendMessageResponse, *MatrixError) {
	// 检查用户是否在房间中
	membership, _ := e.HS.Room.GetUserMembership(roomID, sender)
	if membership != MembershipJoin {
		return nil, ErrForbidden
	}

	// 检查权限（对于状态事件）
	if stateKey != nil {
		// Users are always allowed to update their own m.room.member state
		// (e.g., to set per-room display name or avatar)
		if EventType(eventType) == EventTypeRoomMember && *stateKey == sender {
			// Ensure content has membership=join (users can only update their own join state)
			if m, ok := content["membership"].(string); !ok || m != string(MembershipJoin) {
				content["membership"] = string(MembershipJoin)
			}
		} else if !e.canUserSendStateEvent(roomID, sender, EventType(eventType)) {
			return nil, ErrForbidden
		}
	} else {
		// 检查是否可以发送消息
		if !e.canUserSendEvent(roomID, sender, EventType(eventType)) {
			return nil, ErrForbidden
		}
	}

	// State event idempotency: if the current state already has identical content,
	// return the existing event_id (Matrix spec: setting state twice is idempotent)
	if stateKey != nil {
		sk := *stateKey

		var currentState MatrixRoomState
		err := db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ?",
			roomID, eventType, sk).First(&currentState).Error

		if err == nil {
			var existingEvent MatrixEvent
			err := db.GetDB().Where("event_id = ?", currentState.EventID).First(&existingEvent).Error
			if err == nil {
				if contentEqual(existingEvent.Content, content) {
					return &SendMessageResponse{EventID: existingEvent.EventID}, nil
				}
			}
		}
	}

	// 生成事件 ID
	eventID := "$" + uuid.New().String()

	if txnID != nil {
		// 使用 (roomID + sender + txnID) 的哈希生成确定性、唯一事件 ID
		h := sha256.Sum256([]byte(roomID + ":" + sender + ":" + *txnID))
		eventID = "$" + hex.EncodeToString(h[:])
	}

	// Only explicit transaction IDs require a lookup; random new UUIDs do not.
	var err error
	if txnID != nil {
		var existing MatrixEvent
		err = db.GetDB().Where("event_id = ? OR txn_key = ?", eventID, eventID).First(&existing).Error
		if err == nil {
			return &SendMessageResponse{EventID: existing.EventID}, nil
		}
		if err != gorm.ErrRecordNotFound {
			return nil, ErrInternal
		}
	}
	prevEvents := e.getPrevEvents(roomID)

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
		AuthEvents:     e.computeAuthEvents(roomID, EventType(eventType), sender),
		Depth:          depth,
		OriginServerTS: time.Now().UnixMilli(),
		SenderDomain:   e.HS.Config.ServerName,
		TxnID:          txnID,
	}

	if txnID != nil {
		event.TxnKey = &eventID
	}

	// The persistence hook builds the room-version-specific signed PDU.

	// 开始事务
	tx := db.GetDB().Set("matrix:signer", e.HS).Begin()
	err = tx.Create(event).Error
	if err != nil {
		tx.Rollback()

		return nil, ErrInternal
	}

	// 如果是状态事件，更新状态
	if stateKey != nil {
		e.HS.Room.updateRoomState(tx, roomID, event)
	}

	// 更新房间最后事件
	tx.Model(&MatrixRoom{}).Where("room_id = ?", roomID).Updates(map[string]any{
		"last_event_id": event.EventID,
		"last_event_ts": event.OriginServerTS,
	})

	// 处理特殊事件类型
	switch EventType(eventType) {
	case EventTypeRoomMember:
		e.handleMemberEvent(tx, event)
	case EventTypeRoomRedaction:
		e.handleRedactionEvent(tx, event)
	}

	// Handle tombstone: migrate push rules to replacement room (synchronous to avoid race)
	if eventType == "m.room.tombstone" && stateKey != nil {
		if replacementRoom, ok := content["replacement_room"].(string); ok && replacementRoom != "" {
			if e.HS.RoomUpgrade != nil {
				e.HS.RoomUpgrade.migratePushRules(roomID, replacementRoom)
			}
		}
	}

	err = tx.Commit().Error
	if err != nil {
		return nil, ErrInternal
	}

	// 存储事件关系
	if e.HS.Relations != nil {
		go func() { _ = e.HS.Relations.StoreRelation(event) }()
	}

	// 通知同步等待者（唤醒 long-polling 的 /sync 请求）
	go e.HS.NotifyRoomEvent(roomID)

	// 触发推送通知（异步）
	go func() {
		if e.HS.Push != nil {
			var room MatrixRoom

			db.GetDB().Where("room_id = ?", roomID).First(&room)
			e.HS.Push.NotifyEvent(event, &room)
		}
	}()

	// 联邦事件传播：将事件发送给房间内其他服务器（异步）
	go e.federateEvent(event)

	return &SendMessageResponse{EventID: event.EventID}, nil
}

// federateEvent 将本地事件联邦传播到房间内的远程服务器.
func (e *EventService) federateEvent(event *MatrixEvent) {
	// 查找所有在房间内已 join 的远程用户，按服务器域名分组
	var members []MatrixRoomMember

	db.GetDB().Where("room_id = ? AND membership = ?", event.RoomID, MembershipJoin).Find(&members)

	remoteServers := make(map[string]bool)

	for _, m := range members {
		_, domain, ok := ParseMatrixUserID(m.UserID)
		if !ok || domain == e.HS.Config.ServerName {
			continue
		}

		remoteServers[domain] = true
	}

	if len(remoteServers) == 0 {
		return
	}

	// 构建 PDU（事件的 JSON 表示）
	pduData, err := buildEventPDU(event)
	if err != nil {
		return
	}

	pdus := []json.RawMessage{pduData}

	fedClient := NewFederationClient(e.HS)
	txnID := fmt.Sprintf("%s-%d", event.EventID, time.Now().UnixMilli())

	for serverName := range remoteServers {
		server := serverName // capture loop variable
		go func() {
			err := fedClient.SendTransaction(server, txnID, pdus, nil)
			if err != nil {
				// log failure but don't block
				_ = err
			}
		}()
	}
}

// buildEventPDU 将事件转换为联邦 PDU JSON.
func buildEventPDU(event *MatrixEvent) (json.RawMessage, error) {
	wire, err := matrixEventToPDU(event)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire)
}

// ===== 发送消息 =====

// SendRoomMessage 发送房间消息.
func (e *EventService) SendRoomMessage(roomID, sender, txnID *string, content *SendMessageRequest) (*SendMessageResponse, *MatrixError) {
	// 转换为 map
	contentMap := map[string]any{
		"msgtype": content.MsgType, //nolint:goconst
		"body":    content.Body,    //nolint:goconst
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

// computeAuthEvents 计算事件的 auth_events（按 Matrix 规范）.
func (e *EventService) computeAuthEvents(roomID string, eventType EventType, sender string) []string {
	types := []EventType{EventTypeRoomCreate, EventTypeRoomPowerLevels}
	if eventType == EventTypeRoomMember {
		types = append(types, EventTypeRoomJoinRules)
	}
	var rows []MatrixRoomState
	if db.GetDB().Select("event_id", "event_type", "state_key").Where("room_id = ? AND ((event_type IN ? AND state_key = ?) OR (event_type = ? AND state_key = ?))", roomID, types, "", EventTypeRoomMember, sender).Order("id ASC").Find(&rows).Error != nil {
		return nil
	}
	byType := map[EventType]string{}
	for _, row := range rows {
		if _, ok := byType[row.EventType]; !ok {
			byType[row.EventType] = row.EventID
		}
	}
	result := []string{}
	for _, kind := range []EventType{EventTypeRoomCreate, EventTypeRoomPowerLevels, EventTypeRoomMember, EventTypeRoomJoinRules} {
		if id, ok := byType[kind]; ok {
			result = append(result, id)
		}
	}
	return result
}

// signLocalEvent 为本地创建的事件添加签名.
func (e *EventService) signLocalEvent(event *MatrixEvent) {
	fed := NewFederationService(e.HS)

	eventMap := map[string]any{
		"event_id":         event.EventID,
		"room_id":          event.RoomID,
		"type":             string(event.EventType),
		"sender":           event.Sender,
		"content":          event.Content,
		"origin_server_ts": event.OriginServerTS,
		"depth":            event.Depth,
		"prev_events":      event.PrevEvents,
		"auth_events":      event.AuthEvents,
	}
	if event.StateKey != nil {
		eventMap["state_key"] = *event.StateKey
	}

	eventMap = fed.signEvent(eventMap)

	if sigs, ok := eventMap["signatures"].(map[string]any); ok {
		event.Signatures = make(map[string]map[string]string)

		for server, keySigs := range sigs {
			if keyMap, ok := keySigs.(map[string]any); ok {
				event.Signatures[server] = make(map[string]string)

				for keyID, sig := range keyMap {
					if sigStr, ok := sig.(string); ok {
						event.Signatures[server][keyID] = sigStr
					}
				}
			}
		}
	}
}

// ===== 获取事件 =====

// GetEvent 获取单个事件.
func (e *EventService) GetEvent(eventID string) (*ClientEvent, *MatrixError) {
	var event MatrixEvent

	err := db.GetDB().Where("event_id = ?", eventID).First(&event).Error
	if err != nil {
		return nil, ErrNotFound
	}

	return event.ToClientEvent(), nil
}

// GetRoomEvent 获取房间事件.
func (e *EventService) GetRoomEvent(roomID, eventID string) (*ClientEvent, *MatrixError) {
	var event MatrixEvent

	err := db.GetDB().Where("room_id = ? AND event_id = ?", roomID, eventID).First(&event).Error
	if err != nil {
		return nil, ErrNotFound
	}

	return event.ToClientEvent(), nil
}

// GetRoomEventForDevice returns a client event with unsigned.transaction_id for the requesting device.
func (e *EventService) GetRoomEventForDevice(roomID, eventID, deviceID string) (*ClientEvent, *MatrixError) {
	var event MatrixEvent

	err := db.GetDB().Where("room_id = ? AND event_id = ?", roomID, eventID).First(&event).Error
	if err != nil {
		return nil, ErrNotFound
	}

	return event.ToClientEventForDevice(deviceID), nil
}

// GetRoomMessagesRequest 获取房间消息请求.
type GetRoomMessagesRequest struct {
	Context context.Context `form:"-" json:"-"`
	Dir     string          `form:"dir"` // f, b
	From    string          `form:"from"`
	To      string          `form:"to"`
	Limit   int             `form:"limit"`
	Filter  string          `form:"filter"`
}

// RoomMessagesResponse 消息响应.
type RoomMessagesResponse struct {
	Chunk []*ClientEvent `json:"chunk"`
	Start string         `json:"start"`
	End   string         `json:"end,omitempty"`
	State []*ClientEvent `json:"state,omitempty"`
}

// GetRoomMessages 获取房间消息.
func (e *EventService) GetRoomMessages(roomID, userID string, req *GetRoomMessagesRequest) (*RoomMessagesResponse, *MatrixError) { //nolint:gocyclo // Matrix pagination protocol with composite cursors
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	conn := db.GetDB().WithContext(ctx)
	// 检查用户权限：已加入或曾经加入的用户都可以查看消息（history visibility 决定具体可见范围）
	// 已遗忘的房间不允许访问
	var member MatrixRoomMember
	err := conn.Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, ErrInternal
	}

	memberExists := err == nil
	if member.Forgotten {
		return nil, ErrForbidden
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	} else if limit > 1000 {
		limit = 1000
	}

	query := conn.Model(&MatrixEvent{}).Where("room_id = ?", roomID)

	// For departed users, limit visible events to those at or before their leave
	if member.Membership == MembershipLeave || member.Membership == MembershipBan {
		if member.EventID != "" {
			var leaveEvent MatrixEvent
			if conn.Where("event_id = ?", member.EventID).First(&leaveEvent).Error == nil {
				query = query.Where("origin_server_ts <= ?", leaveEvent.OriginServerTS)
			}
		}
	}

	// 方向 — use (origin_server_ts, event_id) composite ordering for stable pagination
	if req.Dir == "b" {
		query = query.Order("origin_server_ts DESC, event_id DESC")
	} else {
		query = query.Order("origin_server_ts ASC, event_id ASC")
	}

	// 分页 — use composite cursor (timestamp + event_id) to avoid infinite loops
	if req.From != "" {
		cursor := e.parsePaginationCursor(req.From)
		if cursor.TS > 0 {
			if req.Dir == "b" {
				if cursor.EventID != "" {
					// Composite cursor: events before this (ts, event_id) pair
					query = query.Where("(origin_server_ts < ? OR (origin_server_ts = ? AND event_id < ?))",
						cursor.TS, cursor.TS, cursor.EventID)
				} else {
					query = query.Where("origin_server_ts < ?", cursor.TS)
				}
			} else {
				if cursor.EventID != "" {
					query = query.Where("(origin_server_ts > ? OR (origin_server_ts = ? AND event_id > ?))",
						cursor.TS, cursor.TS, cursor.EventID)
				} else {
					query = query.Where("origin_server_ts >= ?", cursor.TS)
				}
			}
		}
	}

	// Apply filter parameters (e.g., contains_url)
	if req.Filter != "" {
		var typeFilter parsedEventFilter
		if json.Unmarshal([]byte(req.Filter), &typeFilter) == nil {
			if len(typeFilter.Types) > 0 && !slices.Contains(typeFilter.Types, "*") {
				query = query.Where("event_type IN ?", typeFilter.Types)
			}
			if slices.Contains(typeFilter.NotTypes, "*") {
				query = query.Where("1 = 0")
			} else if len(typeFilter.NotTypes) > 0 {
				query = query.Where("event_type NOT IN ?", typeFilter.NotTypes)
			}
		}

		var filter map[string]any
		if json.Unmarshal([]byte(req.Filter), &filter) == nil {
			if cu, ok := filter["contains_url"].(bool); ok {
				if cu {
					// Only return events whose content contains a "url" field
					query = query.Where("content LIKE ?", "%\"url\"%")
				} else {
					// Only return events whose content does NOT contain a "url" field
					query = query.Where("content NOT LIKE ?", "%\"url\"%")
				}
			}
		}
	}

	query = applyMessagesToCursor(query, e.parsePaginationCursor(req.To), req.Dir)
	var events []MatrixEvent

	if err := query.Limit(limit).Find(&events).Error; err != nil {
		return nil, ErrInternal
	}

	// On-demand backfill: if we got fewer events than requested while paginating backwards,
	// try to fetch more from a remote server (skip when filter is active to avoid returning unfiltered results)
	if len(events) < limit && req.Dir == "b" && req.Filter == "" && member.Membership == MembershipJoin && e.tryBackfillFromRemote(roomID, events, limit-len(events)) {
		// Re-query after backfill to include newly fetched events
		var backfilledEvents []MatrixEvent

		requery := conn.Model(&MatrixEvent{}).Where("room_id = ?", roomID)

		if member.Membership == MembershipLeave || member.Membership == MembershipBan {
			if member.EventID != "" {
				var leaveEvent MatrixEvent
				if conn.Where("event_id = ?", member.EventID).First(&leaveEvent).Error == nil {
					requery = requery.Where("origin_server_ts <= ?", leaveEvent.OriginServerTS)
				}
			}
		}

		requery = requery.Order("origin_server_ts DESC, event_id DESC")

		if req.From != "" {
			cursor := e.parsePaginationCursor(req.From)
			if cursor.TS > 0 {
				if cursor.EventID != "" {
					requery = requery.Where("(origin_server_ts < ? OR (origin_server_ts = ? AND event_id < ?))",
						cursor.TS, cursor.TS, cursor.EventID)
				} else {
					requery = requery.Where("origin_server_ts < ?", cursor.TS)
				}
			}
		}

		requery = applyMessagesToCursor(requery, e.parsePaginationCursor(req.To), req.Dir)
		if err := requery.Limit(limit).Find(&backfilledEvents).Error; err != nil {
			return nil, ErrInternal
		}

		if len(backfilledEvents) > len(events) {
			events = backfilledEvents
		}
	}

	reader := &clientHistoryReader{conn: conn, roomID: roomID, userID: userID, member: nil, state: map[string]MatrixEvent{}}
	if memberExists {
		reader.member = &member
	}
	visible := make([]MatrixEvent, 0, limit)
	for len(events) > 0 {
		batch, err := reader.filter(events)
		if err != nil {
			return nil, ErrInternal
		}
		for _, event := range batch {
			visible = append(visible, event)
			if len(visible) == limit {
				break
			}
		}
		last := events[len(events)-1]
		if len(visible) == limit || len(events) < limit {
			break
		}
		next := query.Session(&gorm.Session{})
		if req.Dir == "b" {
			next = next.Where("(origin_server_ts < ? OR (origin_server_ts = ? AND event_id < ?))", last.OriginServerTS, last.OriginServerTS, last.EventID)
		} else {
			next = next.Where("(origin_server_ts > ? OR (origin_server_ts = ? AND event_id > ?))", last.OriginServerTS, last.OriginServerTS, last.EventID)
		}
		events = nil
		if err := next.Limit(limit).Find(&events).Error; err != nil {
			return nil, ErrInternal
		}
	}
	events = visible
	if len(events) == 0 && !memberExists {
		return nil, ErrForbidden
	}
	chunk, conversionErr := batchClientEvents(conn, events, "")
	if conversionErr != nil {
		return nil, ErrInternal
	}

	// 生成分页 token
	start := req.From

	var end string

	if len(events) > 0 {
		lastEvt := events[len(events)-1]
		end = e.generatePaginationTokenForEvent(lastEvt.OriginServerTS, lastEvt.EventID)
	}

	resp := &RoomMessagesResponse{
		Chunk: chunk,
		Start: start,
		End:   end,
	}

	// Handle lazy_load_members filter: include m.room.member state for senders
	if req.Filter != "" {
		var filter map[string]any
		if json.Unmarshal([]byte(req.Filter), &filter) == nil {
			if llm, ok := filter["lazy_load_members"].(bool); ok && llm {
				senderSet := make(map[string]bool)
				for _, event := range events {
					senderSet[event.Sender] = true
				}

				var stateEvents []*ClientEvent
				senders := make([]string, 0, len(senderSet))
				for sender := range senderSet {
					senders = append(senders, sender)
				}
				if len(senders) > 0 {
					var rows []MatrixEvent
					err := conn.Raw(`SELECT * FROM (SELECT matrix_events.*, ROW_NUMBER() OVER (PARTITION BY state_key ORDER BY origin_server_ts DESC,event_id ASC) AS member_rank FROM matrix_events WHERE room_id = ? AND event_type = ? AND state_key IN ?) AS latest WHERE member_rank = 1`, roomID, EventTypeRoomMember, senders).Scan(&rows).Error
					if err != nil {
						return nil, ErrInternal
					}
					visibleStates, err := reader.filter(rows)
					if err != nil {
						return nil, ErrInternal
					}
					stateEvents, err = batchClientEvents(conn, visibleStates, "")
					if err != nil {
						return nil, ErrInternal
					}
				}

				resp.State = stateEvents
			}
		}
	}

	return resp, nil
}

// tryBackfillFromRemote attempts to backfill events from a remote server when local history is exhausted.
// maxEvents is how many additional events this page still needs.
func (e *EventService) tryBackfillFromRemote(roomID string, localEvents []MatrixEvent, maxEvents int) (attempted bool) {
	// Find a remote server in the room
	var members []MatrixRoomMember

	db.GetDB().Select("user_id").Where("room_id = ? AND membership = ?", roomID, MembershipJoin).Find(&members)

	var remoteServer string

	for _, m := range members {
		_, domain, ok := ParseMatrixUserID(m.UserID)
		if ok && domain != e.HS.Config.ServerName {
			remoteServer = domain

			break
		}
	}

	if remoteServer == "" {
		return
	}

	// Use the earliest local event as the backfill starting point
	var startEventID string
	if len(localEvents) > 0 {
		startEventID = localEvents[len(localEvents)-1].EventID
	} else {
		var earliest MatrixEvent
		if db.GetDB().Where("room_id = ?", roomID).Order("origin_server_ts ASC").First(&earliest).Error == nil {
			startEventID = earliest.EventID
		}
	}

	if startEventID == "" {
		return
	}

	fedClient := NewFederationClient(e.HS)
	e.HS.Room.backfillFromEvent(fedClient, remoteServer, roomID, maxEvents, startEventID)
	return true
}

// GetRoomContext 获取事件上下文.
func (e *EventService) GetRoomContext(roomID, eventID, userID string, limit int) (*EventContextResponse, *MatrixError) {
	return e.GetRoomContextContext(context.Background(), roomID, eventID, userID, limit)
}
func (e *EventService) GetRoomContextContext(ctx context.Context, roomID, eventID, userID string, limit int) (*EventContextResponse, *MatrixError) {
	conn := db.GetDB().WithContext(ctx)
	// 检查权限
	var member MatrixRoomMember
	if conn.Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error != nil || member.Membership != MembershipJoin || member.Forgotten {
		return nil, ErrForbidden
	}

	// 获取目标事件
	var targetEvent MatrixEvent

	err := conn.Where("room_id = ? AND event_id = ?", roomID, eventID).First(&targetEvent).Error
	if err != nil {
		return nil, ErrNotFound
	}

	reader, historyErr := newClientHistoryReader(conn, roomID, userID)
	if historyErr != nil {
		return nil, ErrInternal
	}
	visible, historyErr := reader.filter([]MatrixEvent{targetEvent})
	if historyErr != nil {
		return nil, ErrInternal
	}
	if len(visible) == 0 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}

	var eventsBefore, eventsAfter []MatrixEvent
	for _, direction := range []string{"b", "f"} {
		collected := []MatrixEvent{}
		cursor := targetEvent
		for len(collected) < limit {
			comparison, order := ">", "origin_server_ts ASC,event_id ASC"
			if direction == "b" {
				comparison, order = "<", "origin_server_ts DESC,event_id DESC"
			}
			var rows []MatrixEvent
			err := conn.Where("room_id = ? AND (origin_server_ts "+comparison+" ? OR (origin_server_ts = ? AND event_id "+comparison+" ?))", roomID, cursor.OriginServerTS, cursor.OriginServerTS, cursor.EventID).Order(order).Limit(limit - len(collected)).Find(&rows).Error
			if err != nil {
				return nil, ErrInternal
			}
			if len(rows) == 0 {
				break
			}
			allowed, err := reader.filter(rows)
			if err != nil {
				return nil, ErrInternal
			}
			collected = append(collected, allowed...)
			cursor = rows[len(rows)-1]
		}
		if direction == "b" {
			eventsBefore = collected
		} else {
			eventsAfter = collected
		}
	}

	// 获取房间状态
	state, stateErr := e.HS.Room.GetRoomStateAllContext(ctx, roomID)
	if stateErr != nil {
		return nil, stateErr
	}

	// Build pagination tokens
	startToken := e.generatePaginationTokenForEvent(targetEvent.OriginServerTS, targetEvent.EventID)
	if len(eventsBefore) > 0 {
		startToken = e.generatePaginationTokenForEvent(eventsBefore[len(eventsBefore)-1].OriginServerTS, eventsBefore[len(eventsBefore)-1].EventID)
	}

	endToken := e.generatePaginationTokenForEvent(targetEvent.OriginServerTS, targetEvent.EventID)
	if len(eventsAfter) > 0 {
		endToken = e.generatePaginationTokenForEvent(eventsAfter[len(eventsAfter)-1].OriginServerTS, eventsAfter[len(eventsAfter)-1].EventID)
	}

	allEvents := append([]MatrixEvent{targetEvent}, eventsBefore...)
	allEvents = append(allEvents, eventsAfter...)
	converted, conversionErr := batchClientEvents(conn, allEvents, "")
	if conversionErr != nil {
		return nil, ErrInternal
	}
	return &EventContextResponse{
		Start:        startToken,
		End:          endToken,
		Event:        converted[0],
		EventsBefore: converted[1 : 1+len(eventsBefore)],
		EventsAfter:  converted[1+len(eventsBefore):],
		State:        state,
	}, nil
}

// EventContextResponse 事件上下文响应.
type EventContextResponse struct {
	Start        string         `json:"start"`
	End          string         `json:"end"`
	Event        *ClientEvent   `json:"event"`
	EventsBefore []*ClientEvent `json:"events_before"`
	EventsAfter  []*ClientEvent `json:"events_after"`
	State        []*ClientEvent `json:"state"`
}

// ===== 删除事件 =====

// RedactEvent 删除事件.
func (e *EventService) RedactEvent(roomID, eventID, userID, reason string, txnID *string) (*SendMessageResponse, *MatrixError) {
	// 检查权限
	if !e.canUserRedact(roomID, userID) {
		return nil, ErrForbidden
	}

	// 创建删除事件 (Matrix spec: redaction should succeed even if target event is unknown,
	// because in federation the redaction may arrive before the target event)
	redactEventID := "$" + uuid.New().String()
	if txnID != nil {
		redactEventID = fmt.Sprintf("$%s_redact:%s", *txnID, e.HS.Config.ServerName)
	}

	redactEvent := &MatrixEvent{
		EventID:   redactEventID,
		RoomID:    roomID,
		EventType: EventTypeRoomRedaction,
		Sender:    userID,
		Content: map[string]any{
			"reason": reason,
		},
		Redacts:        &eventID,
		Depth:          e.HS.Room.getNextDepth(db.GetDB(), roomID),
		OriginServerTS: time.Now().UnixMilli(),
	}

	err := db.GetDB().Set("matrix:signer", e.HS).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(redactEvent).Error; err != nil {
			return err
		}
		return applyStoredRedactionTx(tx, roomID, eventID)
	})
	if err != nil {
		return nil, ErrInternal
	}

	go e.federateEvent(redactEvent)
	return &SendMessageResponse{EventID: redactEvent.EventID}, nil
}

// applyStoredRedaction strips a stored event down to the keys redaction keeps.
func applyStoredRedaction(roomID, targetID string) {
	_ = applyStoredRedactionTx(db.GetDB(), roomID, targetID)
}

func applyStoredRedactionTx(tx *gorm.DB, roomID, targetID string) error {
	var target MatrixEvent
	if err := tx.Where("event_id = ? AND room_id = ?", targetID, roomID).First(&target).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	if target.FederationJSON != "" {
		var room MatrixRoom
		if err := tx.Where("room_id = ?", roomID).First(&room).Error; err != nil {
			return err
		}
		version, err := gmsl.GetRoomVersion(gmsl.RoomVersion(room.RoomVersion))
		if err != nil {
			return err
		}
		raw, err := version.RedactEventJSON([]byte(target.FederationJSON))
		if err != nil {
			return err
		}
		var wire struct {
			Content map[string]any `json:"content"`
		}
		if err = json.Unmarshal(raw, &wire); err != nil {
			return err
		}
		target.Content = wire.Content
		target.FederationJSON = string(raw)
		return tx.Model(&target).Select("content", "federation_json").Updates(&target).Error
	}
	target.Content = redactContent(string(target.EventType), target.Content)
	return tx.Model(&target).Select("content").Updates(&target).Error
}

// contentEqual compares two map[string]any values for deep equality
// by marshaling to JSON (canonical comparison).
func contentEqual(a, b map[string]any) bool {
	aj, err1 := json.Marshal(a)

	bj, err2 := json.Marshal(b)

	if err1 != nil || err2 != nil {
		return false
	}

	return string(aj) == string(bj)
}

// ===== 权限检查 =====

// canUserSendEvent 检查是否可以发送事件.
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

// canUserSendStateEvent 检查是否可以发送状态事件.
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

// canUserRedact 检查是否可以删除.
func (e *EventService) canUserRedact(roomID, userID string) bool {
	pl, err := e.HS.Room.getRoomPowerLevels(roomID)
	if err != nil {
		return false
	}

	userPL := e.HS.Room.getUserPowerLevel(pl, userID)

	return userPL >= pl.Redact
}

// ===== 辅助函数 =====

// getPrevEvents 获取前序事件.
// Room v3+ 必须恰好有一个 prev_event。v1/v2 允许多个，最多保留 10 个。
func (e *EventService) getPrevEvents(roomID string) []string {
	limit := 1

	var room MatrixRoom
	if err := db.GetDB().Select("room_version").Where("room_id = ?", roomID).First(&room).Error; err == nil {
		if room.RoomVersion == "1" || room.RoomVersion == "2" {
			limit = 10
		}
	}

	var events []MatrixEvent

	db.GetDB().Where("room_id = ?", roomID).
		Order("depth DESC, origin_server_ts DESC").Limit(limit).Find(&events)

	prevEvents := make([]string, len(events))
	for i, event := range events {
		prevEvents[i] = event.EventID
	}

	return prevEvents
}

// handleMemberEvent 处理成员事件.
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

// handleRedactionEvent 处理删除事件.
func (e *EventService) handleRedactionEvent(tx *gorm.DB, event *MatrixEvent) {
	// 已在 RedactEvent 中处理
}

// parsePaginationToken 解析分页 token — handles both "t{ts}" and "s{ts}" formats
// paginationCursor holds both timestamp and event_id for precise cursor-based pagination.
// This avoids infinite loops when multiple events share the same timestamp.
type paginationCursor struct {
	TS      int64
	EventID string
}

func (e *EventService) parsePaginationToken(token string) int64 {
	c := e.parsePaginationCursor(token)

	return c.TS
}

func (e *EventService) parsePaginationCursor(token string) paginationCursor {
	var ts int64
	// Try composite format: t{ts}_{eventID} or s{ts}_{eventID}
	for _, prefix := range []string{"t", "s"} {
		if !strings.HasPrefix(token, prefix) {
			continue
		}

		rest := token[1:]
		if idx := strings.Index(rest, "_$"); idx > 0 {
			// Composite: ts part before _$, eventID is $...
			if parsed, err := strconv.ParseInt(rest[:idx], 10, 64); err == nil {
				return paginationCursor{TS: parsed, EventID: rest[idx+1:]}
			}
		}
		// Simple format: just timestamp (may have _index suffix from sync tokens)
		tsStr := rest
		if idx := strings.Index(tsStr, "_"); idx > 0 {
			tsStr = tsStr[:idx]
		}

		if parsed, err := strconv.ParseInt(tsStr, 10, 64); err == nil {
			return paginationCursor{TS: parsed}
		}
	}

	if n, _ := fmt.Sscanf(token, "t%d", &ts); n == 1 {
		return paginationCursor{TS: ts}
	}

	return paginationCursor{}
}

// generatePaginationToken 生成分页 token.
func (e *EventService) generatePaginationToken(ts int64) string {
	return fmt.Sprintf("t%d", ts)
}

func (e *EventService) generatePaginationTokenForEvent(ts int64, eventID string) string {
	return fmt.Sprintf("t%d_%s", ts, eventID)
}

// eventsToClientEvents 转换事件列表.
func (e *EventService) eventsToClientEvents(events []MatrixEvent) []*ClientEvent {
	result := make([]*ClientEvent, len(events))
	for i, event := range events {
		result[i] = event.ToClientEvent()
	}

	return result
}

// GetEventByTxnID 通过事务ID获取事件.
func (e *EventService) GetEventByTxnID(roomID, userID, txnID string) (*ClientEvent, *MatrixError) {
	eventID := fmt.Sprintf("$%s:%s", txnID, e.HS.Config.ServerName)

	return e.GetEvent(eventID)
}

// GetRoomStateEvent 获取房间状态事件.
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

func applyMessagesToCursor(query *gorm.DB, cursor paginationCursor, dir string) *gorm.DB {
	if cursor.TS <= 0 {
		return query
	}
	comparison := ">"
	if dir != "b" {
		comparison = "<"
	}
	if cursor.EventID != "" {
		return query.Where("(origin_server_ts "+comparison+" ? OR (origin_server_ts = ? AND event_id "+comparison+" ?))", cursor.TS, cursor.TS, cursor.EventID)
	}
	return query.Where("origin_server_ts "+comparison+" ?", cursor.TS)
}
