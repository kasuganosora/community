package homeserver

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
)

// RoomService 房间服务.
type RoomService struct {
	HS *HomeServer
}

// NewRoomService 创建房间服务.
func NewRoomService(hs *HomeServer) *RoomService {
	return &RoomService{HS: hs}
}

// ===== 房间创建 =====

// CreateRoomRequest 创建房间请求.
type CreateRoomRequest struct {
	Visibility                string                 `json:"visibility"` // public, private
	RoomAliasName             *string                `json:"room_alias_name"`
	Name                      *string                `json:"name"`
	Topic                     *string                `json:"topic"`
	Invite                    []string               `json:"invite"`
	Invite3PID                []Invite3PID           `json:"invite_3pid"`
	RoomVersion               string                 `json:"room_version"`
	CreationContent           map[string]any         `json:"creation_content"`
	InitialState              []CreateRoomStateEvent `json:"initial_state"`
	Preset                    string                 `json:"preset"` // private_chat, trusted_private_chat, public_chat
	IsDirect                  bool                   `json:"is_direct"`
	PowerLevelContentOverride *PowerLevelContent     `json:"power_level_content_override"`
}

// Invite3PID 第三方邀请.
type Invite3PID struct {
	Medium        string `json:"medium"`
	Address       string `json:"address"`
	IDServer      string `json:"id_server"`
	IDAccessToken string `json:"id_access_token"`
}

// CreateRoomStateEvent 初始状态事件.
type CreateRoomStateEvent struct {
	Type     string         `json:"type"`
	StateKey string         `json:"state_key"`
	Content  map[string]any `json:"content"`
}

// PowerLevelContent 权限级别.
type PowerLevelContent struct {
	Users         map[string]int64 `json:"users"`
	UsersDefault  int64            `json:"users_default"`
	Events        map[string]int64 `json:"events"`
	EventsDefault int64            `json:"events_default"`
	StateDefault  int64            `json:"state_default"`
	Ban           int64            `json:"ban"`
	Kick          int64            `json:"kick"`
	Redact        int64            `json:"redact"`
	Invite        int64            `json:"invite"`
}

// CreateRoomResponse 创建房间响应.
type CreateRoomResponse struct {
	RoomID string `json:"room_id"`
}

// CreateRoom 创建房间.
func (r *RoomService) CreateRoom(creatorID string, req *CreateRoomRequest) (*CreateRoomResponse, *MatrixError) { //nolint:gocyclo // Matrix room creation protocol requires sequential event creation
	// 生成房间 ID
	roomID := "!" + uuid.New().String() + ":" + r.HS.Config.ServerName

	// 默认房间版本
	roomVersion := req.RoomVersion
	if roomVersion == "" {
		roomVersion = "10"
	}

	// 验证房间版本（Matrix spec 仅支持整数版本 1-11）
	validVersions := map[string]bool{
		"1": true, "2": true, "3": true, "4": true, "5": true,
		"6": true, "7": true, "8": true, "9": true, "10": true, "11": true,
	}
	if !validVersions[roomVersion] {
		return nil, &MatrixError{
			ErrCode: "M_UNSUPPORTED_ROOM_VERSION",
			Error:   fmt.Sprintf("Room version %q is not supported", roomVersion),
		}
	}

	// 开始事务
	tx := db.GetDB().Begin()

	// 创建房间记录
	visibility := req.Visibility
	if visibility == "" {
		visibility = "private"
	}

	room := &MatrixRoom{
		RoomID:      roomID,
		RoomVersion: roomVersion,
		Creator:     creatorID,
		IsDirect:    req.IsDirect,
		Visibility:  visibility,
	}

	if req.Name != nil {
		room.Name = req.Name
	}

	if req.Topic != nil {
		room.Topic = req.Topic
	}

	err := tx.Create(room).Error
	if err != nil {
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

	// Set JoinRule on room record based on preset (denormalized for queries)
	if preset == "public_chat" {
		tx.Model(&MatrixRoom{}).Where("room_id = ?", roomID).Update("join_rule", "public")
	}

	// 构建 auth events
	var authEvents []string
	// Use a base timestamp and increment for each event to ensure deterministic ordering
	baseTS := time.Now().UnixMilli()
	tsCounter := int64(0)

	// 1. 创建 m.room.create 事件
	createEvent := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomCreate,
		Sender:    creatorID,
		StateKey:  new(""),
		Content: map[string]any{
			"creator":      creatorID,
			"room_version": roomVersion,
		},
		Depth:          1,
		AuthEvents:     []string{},
		OriginServerTS: baseTS + tsCounter,
		SenderDomain:   r.HS.Config.ServerName,
	}
	tsCounter++

	// 添加 creation_content（Matrix spec: room_version in creation_content is ignored）
	if req.CreationContent != nil {
		for k, v := range req.CreationContent {
			if k != "room_version" {
				// 特殊处理 predecessor 字段，确保格式正确
				if k == "predecessor" {
					if predecessor, ok := v.(map[string]any); ok {
						// 验证 predecessor 包含必需的字段
						if roomID, hasRoomID := predecessor["room_id"].(string); hasRoomID && roomID != "" {
							createEvent.Content[k] = predecessor
						}
					}
				} else {
					createEvent.Content[k] = v
				}
			}
		}
	}

	err = tx.Create(createEvent).Error
	if err != nil {
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
		StateKey:  new(""),
		Content: map[string]any{
			"users":          plContent.Users,        //nolint:goconst
			"users_default":  plContent.UsersDefault, //nolint:goconst
			"events":         plContent.Events,
			"events_default": plContent.EventsDefault, //nolint:goconst
			"state_default":  plContent.StateDefault,  //nolint:goconst
			"ban":            plContent.Ban,           //nolint:goconst
			"kick":           plContent.Kick,          //nolint:goconst
			"redact":         plContent.Redact,        //nolint:goconst
			"invite":         plContent.Invite,        //nolint:goconst
		},
		Depth:          2,
		AuthEvents:     append([]string{}, authEvents...),
		OriginServerTS: baseTS + tsCounter,
		SenderDomain:   r.HS.Config.ServerName,
	}
	tsCounter++

	err = tx.Create(plEvent).Error
	if err != nil {
		tx.Rollback()

		return nil, ErrInternal
	}

	authEvents = append(authEvents, plEvent.EventID)
	r.updateRoomState(tx, roomID, plEvent)

	// 3. 根据 preset 设置其他默认事件
	// Pass tsCounter to ensure deterministic timestamp ordering
	eventTS := baseTS + tsCounter

	switch preset {
	case "private_chat":
		r.createDefaultEventTS(tx, roomID, creatorID, EventTypeRoomJoinRules, map[string]any{
			"join_rule": "invite", //nolint:goconst
		}, &authEvents, &eventTS)
		r.createDefaultEventTS(tx, roomID, creatorID, EventTypeRoomHistoryVisibility, map[string]any{
			"history_visibility": "shared",
		}, nil, &eventTS)
		r.createDefaultEventTS(tx, roomID, creatorID, EventTypeRoomGuestAccess, map[string]any{
			"guest_access": "forbidden",
		}, nil, &eventTS)
	case "trusted_private_chat":
		r.createDefaultEventTS(tx, roomID, creatorID, EventTypeRoomJoinRules, map[string]any{
			"join_rule": "invite",
		}, &authEvents, &eventTS)
		r.createDefaultEventTS(tx, roomID, creatorID, EventTypeRoomHistoryVisibility, map[string]any{
			"history_visibility": "shared",
		}, nil, &eventTS)
		r.createDefaultEventTS(tx, roomID, creatorID, EventTypeRoomGuestAccess, map[string]any{
			"guest_access": "forbidden",
		}, nil, &eventTS)
	case "public_chat":
		r.createDefaultEventTS(tx, roomID, creatorID, EventTypeRoomJoinRules, map[string]any{
			"join_rule": "public",
		}, &authEvents, &eventTS)
		r.createDefaultEventTS(tx, roomID, creatorID, EventTypeRoomHistoryVisibility, map[string]any{
			"history_visibility": "shared",
		}, nil, &eventTS)
		r.createDefaultEventTS(tx, roomID, creatorID, EventTypeRoomGuestAccess, map[string]any{
			"guest_access": "can_join",
		}, nil, &eventTS)
	}

	tsCounter = eventTS - baseTS

	// 4. 处理 initial_state
	for _, state := range req.InitialState {
		tsCounter++

		event := &MatrixEvent{
			EventID:        "$" + uuid.New().String(),
			RoomID:         roomID,
			EventType:      EventType(state.Type),
			Sender:         creatorID,
			StateKey:       &state.StateKey,
			Content:        state.Content,
			Depth:          r.getNextDepth(tx, roomID),
			OriginServerTS: baseTS + tsCounter,
			SenderDomain:   r.HS.Config.ServerName,
		}
		err := tx.Create(event).Error
		if err != nil {
			continue // 忽略错误
		}

		r.updateRoomState(tx, roomID, event)
	}

	// 5. 创建者加入房间
	tsCounter++

	memberEvent := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomMember,
		Sender:    creatorID,
		StateKey:  new(creatorID),
		Content: map[string]any{
			"membership": "join", //nolint:goconst
		},
		Depth:          r.getNextDepth(tx, roomID),
		AuthEvents:     append([]string{}, authEvents...),
		OriginServerTS: baseTS + tsCounter,
		SenderDomain:   r.HS.Config.ServerName,
	}
	err = tx.Create(memberEvent).Error
	if err != nil {
		tx.Rollback()

		return nil, ErrInternal
	}

	authEvents = append(authEvents, memberEvent.EventID)
	r.updateRoomState(tx, roomID, memberEvent)
	r.updateRoomMember(tx, roomID, creatorID, MembershipJoin, memberEvent.EventID)

	// 6. 设置房间名称和主题
	if req.Name != nil {
		tsCounter++

		nameEvent := &MatrixEvent{
			EventID:   "$" + uuid.New().String(),
			RoomID:    roomID,
			EventType: EventTypeRoomName,
			Sender:    creatorID,
			StateKey:  new(""),
			Content: map[string]any{
				"name": *req.Name, //nolint:goconst
			},
			Depth:          r.getNextDepth(tx, roomID),
			AuthEvents:     append([]string{}, authEvents...),
			OriginServerTS: baseTS + tsCounter,
			SenderDomain:   r.HS.Config.ServerName,
		}
		err := tx.Create(nameEvent).Error
		if err != nil {
			tx.Rollback()

			return nil, ErrInternal
		}

		r.updateRoomState(tx, roomID, nameEvent)
	}

	if req.Topic != nil {
		tsCounter++

		topicEvent := &MatrixEvent{
			EventID:   "$" + uuid.New().String(),
			RoomID:    roomID,
			EventType: EventTypeRoomTopic,
			Sender:    creatorID,
			StateKey:  new(""),
			Content: map[string]any{
				"topic": *req.Topic,
				"m.topic": map[string]any{
					"m.text": []any{ //nolint:goconst
						map[string]any{"body": *req.Topic}, //nolint:goconst
					},
				},
			},
			Depth:          r.getNextDepth(tx, roomID),
			AuthEvents:     append([]string{}, authEvents...),
			OriginServerTS: baseTS + tsCounter,
			SenderDomain:   r.HS.Config.ServerName,
		}
		err := tx.Create(topicEvent).Error
		if err != nil {
			tx.Rollback()

			return nil, ErrInternal
		}

		r.updateRoomState(tx, roomID, topicEvent)
	}

	// 7. Set canonical alias state event if alias requested
	var pendingAlias string
	if req.RoomAliasName != nil && *req.RoomAliasName != "" {
		pendingAlias = "#" + *req.RoomAliasName + ":" + r.HS.Config.ServerName
		tsCounter++

		canonicalAliasEvent := &MatrixEvent{
			EventID:   "$" + uuid.New().String(),
			RoomID:    roomID,
			EventType: EventTypeRoomCanonicalAlias,
			Sender:    creatorID,
			StateKey:  new(""),
			Content: map[string]any{
				"alias": pendingAlias,
			},
			Depth:          r.getNextDepth(tx, roomID),
			AuthEvents:     append([]string{}, authEvents...),
			OriginServerTS: baseTS + tsCounter,
			SenderDomain:   r.HS.Config.ServerName,
		}
		err := tx.Create(canonicalAliasEvent).Error
		if err != nil {
			tx.Rollback()

			return nil, ErrInternal
		}

		r.updateRoomState(tx, roomID, canonicalAliasEvent)
	}

	// 8. 处理邀请
	for _, inviteeID := range req.Invite {
		err := r.inviteUser(tx, roomID, creatorID, inviteeID)
		if err != nil {
			continue // 忽略邀请错误
		}
	}

	err = tx.Commit().Error
	if err != nil {
		return nil, ErrInternal
	}

	// Register room alias AFTER commit to avoid SQLite BUSY deadlock
	if pendingAlias != "" {
		aliasService := NewAliasService(r.HS)
		_ = aliasService.SetAlias(pendingAlias, roomID, creatorID)
	}

	return &CreateRoomResponse{RoomID: roomID}, nil
}

func (r *RoomService) createDefaultEventTS(tx *gorm.DB, roomID, sender string, eventType EventType, content map[string]any, authEvents *[]string, tsCounter *int64) {
	// 收集当前 auth events
	var currentAuth []string
	if authEvents != nil {
		currentAuth = append([]string{}, *authEvents...)
	}

	ts := time.Now().UnixMilli()
	if tsCounter != nil {
		ts = *tsCounter
		*tsCounter++
	}

	event := &MatrixEvent{
		EventID:        "$" + uuid.New().String(),
		RoomID:         roomID,
		EventType:      eventType,
		Sender:         sender,
		StateKey:       new(""),
		Content:        content,
		Depth:          r.getNextDepth(tx, roomID),
		AuthEvents:     currentAuth,
		OriginServerTS: ts,
		SenderDomain:   r.HS.Config.ServerName,
	}
	err := tx.Create(event).Error
	if err == nil {
		r.updateRoomState(tx, roomID, event)

		if authEvents != nil {
			*authEvents = append(*authEvents, event.EventID)
		}
	}
}

// ===== 房间加入 =====

// JoinRoomRequest 加入房间请求.
type JoinRoomRequest struct {
	Reason            string         `json:"reason"`
	AdditionalContent map[string]any `json:"-"` // extra fields from request body
	Servers           []string       `json:"-"` // via=server_name query params for federation
}

// JoinRoom 加入房间.
func (r *RoomService) JoinRoom(userID, roomIDOrAlias string, req *JoinRoomRequest) (string, *MatrixError) {
	roomID := roomIDOrAlias

	// 如果是别名，解析为房间ID
	if strings.HasPrefix(roomIDOrAlias, "#") {
		aliasService := NewAliasService(r.HS)

		resolvedRoomID, err := aliasService.GetRoomIDByAlias(roomIDOrAlias)
		if err != nil {
			// Alias not found locally; try federation servers if provided
			if len(req.Servers) > 0 {
				return r.federationJoin(userID, roomIDOrAlias, req.Servers)
			}

			return "", ErrNotFound
		}

		roomID = resolvedRoomID
	}

	// 检查房间是否存在
	var room MatrixRoom

	err := db.GetDB().Where("room_id = ?", roomID).First(&room).Error
	if err != nil {
		// Room not found locally; attempt federation join if server hints provided
		if len(req.Servers) > 0 {
			return r.federationJoin(userID, roomID, req.Servers)
		}

		return "", ErrNotFound
	}

	// 检查用户是否已在房间中
	var member MatrixRoomMember
	// Build member event content, merging any extra content from request
	joinContent := map[string]any{"membership": "join"}

	for k, v := range req.AdditionalContent {
		if k != "membership" {
			joinContent[k] = v
		}
	}

	err = db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error
	if err == nil {
		if member.Membership == MembershipBan {
			return "", ErrForbidden
		}
		// Forgotten rooms cannot be rejoined unless user has a pending invite
		if member.Forgotten && member.Membership != MembershipInvite {
			return "", ErrForbidden
		}
		// 如果是 invite 或 leave，更新为 join
		if member.Membership == MembershipInvite || member.Membership == MembershipLeave {
			event := &MatrixEvent{
				EventID:        "$" + uuid.New().String(),
				RoomID:         roomID,
				EventType:      EventTypeRoomMember,
				Sender:         userID,
				StateKey:       new(userID),
				Content:        joinContent,
				Depth:          r.getNextDepth(db.GetDB(), roomID),
				OriginServerTS: time.Now().UnixMilli(),
				SenderDomain:   r.HS.Config.ServerName,
			}
			err := db.GetDB().Create(event).Error
			if err != nil {
				return "", ErrInternal
			}

			r.updateRoomState(db.GetDB(), roomID, event)
			r.updateRoomMember(db.GetDB(), roomID, userID, MembershipJoin, event.EventID)
			// Clear forgotten flag on re-join (e.g. after re-invite)
			if member.Forgotten {
				db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ?", roomID, userID).Update("forgotten", false)
			}

			go r.HS.NotifyRoomEvent(roomID)
		}

		return roomID, nil
	}

	// 检查加入规则
	joinRuleContent, _ := r.GetRoomState(roomID, EventTypeRoomJoinRules, "")
	joinRule := "invite" // default to restrictive

	if m, ok := joinRuleContent.(map[string]any); ok {
		if jr, ok := m["join_rule"].(string); ok {
			joinRule = jr
		}
	}

	if joinRule == "invite" || joinRule == "knock" {
		return "", ErrForbidden
	}

	// 创建成员事件
	event := &MatrixEvent{
		EventID:        "$" + uuid.New().String(),
		RoomID:         roomID,
		EventType:      EventTypeRoomMember,
		Sender:         userID,
		StateKey:       new(userID),
		Content:        joinContent,
		Depth:          r.getNextDepth(db.GetDB(), roomID),
		OriginServerTS: time.Now().UnixMilli(),
		SenderDomain:   r.HS.Config.ServerName,
	}

	if err := db.GetDB().Create(event).Error; err != nil {
		return "", ErrInternal
	}

	r.updateRoomState(db.GetDB(), roomID, event)

	r.updateRoomMember(db.GetDB(), roomID, userID, MembershipJoin, event.EventID)
	go r.HS.NotifyRoomEvent(roomID)

	return roomID, nil
}

// ===== 联邦加入 =====

// federationJoin 通过联邦协议加入远程房间.
func (r *RoomService) federationJoin(userID, roomID string, servers []string) (string, *MatrixError) {
	fedClient := NewFederationClient(r.HS)
	fed := NewFederationService(r.HS)

	for _, serverName := range servers {
		// Step 1: make_join — 获取加入事件模板
		makeJoinResp, err := fedClient.MakeJoin(serverName, roomID, userID)
		if err != nil {
			log.Warn("federationJoin: make_join to %s failed: %v", serverName, err)

			continue
		}

		// Step 2: 从响应中提取事件模板
		eventMap, _ := makeJoinResp["event"].(map[string]any)
		if eventMap == nil {
			log.Warn("federationJoin: make_join response missing event field")

			continue
		}

		// Step 3: 完善事件字段
		eventMap["sender"] = userID
		eventMap["state_key"] = userID
		eventMap["origin"] = r.HS.Config.ServerName
		eventMap["origin_server_ts"] = time.Now().UnixMilli()
		eventMap["content"] = map[string]any{"membership": "join"}
		eventMap["event_id"] = "$" + utils.GetRandomString(32)

		// Step 4: 签名事件
		eventMap = fed.signEvent(eventMap)

		// Step 5: send_join — 发送已签名的加入事件
		sendJoinResp, err := fedClient.SendJoin(serverName, roomID, eventMap)
		if err != nil {
			log.Warn("federationJoin: send_join to %s failed: %v", serverName, err)

			continue
		}

		// Step 6: 处理响应，在本地存储房间状态
		if err := r.processFederationJoinResponse(roomID, userID, sendJoinResp); err != nil {
			log.Warn("federationJoin: failed to process response from %s: %v", serverName, err)

			continue
		}

		// Step 7: Backfill message history from the remote server (synchronous to ensure
		// events are available when the client starts querying /messages)
		r.backfillFromRemote(fedClient, serverName, roomID)

		return roomID, nil
	}

	return "", ErrNotFound
}

// processFederationJoinResponse 处理联邦加入响应，在本地存储房间状态.
func (r *RoomService) processFederationJoinResponse(roomID, joiningUserID string, resp map[string]any) error { //nolint:unparam // param reserved for future use
	// 确保房间记录存在
	var room MatrixRoom
	if db.GetDB().Where("room_id = ?", roomID).First(&room).Error != nil {
		room = MatrixRoom{
			RoomID:      roomID,
			RoomVersion: "10",
		}
	}

	// 从 m.room.create state 事件提取 room version 和 creator
	if stateList, ok := resp["state"].([]any); ok {
		for _, stateRaw := range stateList {
			stateMap, ok := stateRaw.(map[string]any)
			if !ok {
				continue
			}

			if stateMap["type"] == "m.room.create" { //nolint:goconst
				if content, ok := stateMap["content"].(map[string]any); ok {
					if ver, ok := content["room_version"].(string); ok {
						room.RoomVersion = ver
					}
				}

				room.Creator, _ = stateMap["sender"].(string)

				break
			}
		}
	}

	if db.GetDB().Where("room_id = ?", roomID).First(&MatrixRoom{}).Error != nil {
		db.GetDB().Create(&room)
	}

	// 处理 auth chain 事件（先存储，确保 auth event 先于 state event 存在）
	if authChain, ok := resp["auth_chain"].([]any); ok {
		for _, evtRaw := range authChain {
			if evtMap, ok := evtRaw.(map[string]any); ok {
				r.processRemoteStateEvent(evtMap)
			}
		}
	}

	// 处理 state 事件
	if stateList, ok := resp["state"].([]any); ok {
		for _, stateRaw := range stateList {
			if stateMap, ok := stateRaw.(map[string]any); ok {
				r.processRemoteStateEvent(stateMap)
			}
		}
	}

	// 确保 joining user 被记录为 join 成员
	displayName := ""
	avatarURL := ""
	member := &MatrixRoomMember{
		RoomID:      roomID,
		UserID:      joiningUserID,
		Membership:  MembershipJoin,
		DisplayName: &displayName,
		AvatarURL:   &avatarURL,
	}
	db.GetDB().Where("room_id = ? AND user_id = ?", roomID, joiningUserID).Assign(member).FirstOrCreate(member)

	return nil
}

// processRemoteStateEvent 处理远程状态事件，存储到本地数据库.
func (r *RoomService) processRemoteStateEvent(stateMap map[string]any) {
	eventID, _ := stateMap["event_id"].(string)
	eventType, _ := stateMap["type"].(string)
	roomID, _ := stateMap["room_id"].(string)
	sender, _ := stateMap["sender"].(string)
	stateKey, _ := stateMap["state_key"].(string)
	originServerTS, _ := stateMap["origin_server_ts"].(float64)
	content, _ := stateMap["content"].(map[string]any)

	if eventID == "" || eventType == "" || roomID == "" {
		return
	}

	if content == nil {
		content = map[string]any{}
	}

	// 跳过已存在的事件
	var existing MatrixEvent
	if db.GetDB().Where("event_id = ?", eventID).First(&existing).Error == nil {
		return
	}

	senderDomain := extractServerName(sender)

	event := &MatrixEvent{
		EventID:        eventID,
		RoomID:         roomID,
		EventType:      EventType(eventType),
		Sender:         sender,
		StateKey:       utils.GetPtrIfNotEmpty(stateKey),
		Content:        content,
		OriginServerTS: int64(originServerTS),
		SenderDomain:   senderDomain,
	}
	err := db.GetDB().Create(event).Error
	if err != nil {
		return
	}

	// 更新房间状态表
	r.updateRoomState(db.GetDB(), roomID, event)

	// 处理成员事件
	if eventType == "m.room.member" { //nolint:goconst
		membership, _ := content["membership"].(string)
		displayName, _ := content["displayname"].(string)
		avatarURL, _ := content["avatar_url"].(string)

		m := &MatrixRoomMember{
			RoomID:      roomID,
			UserID:      stateKey,
			Membership:  MembershipState(membership),
			DisplayName: utils.GetPtrIfNotEmpty(displayName),
			AvatarURL:   utils.GetPtrIfNotEmpty(avatarURL),
			EventID:     eventID,
		}
		db.GetDB().Where("room_id = ? AND user_id = ?", roomID, stateKey).Assign(m).FirstOrCreate(m)
	}
}

// backfillFromRemote fetches message history from a remote server after joining.
func (r *RoomService) backfillFromRemote(fedClient *FederationClient, serverName, roomID string) {
	r.backfillFromRemoteWithLimit(fedClient, serverName, roomID, 0)
}

// backfillFromRemoteWithLimit 从远程服务器 backfill 事件，支持指定最大事件数
// maxEvents: 最大要获取的事件数，0 表示使用默认值（100）.
func (r *RoomService) backfillFromRemoteWithLimit(fedClient *FederationClient, serverName, roomID string, maxEvents int) {
	if maxEvents <= 0 {
		maxEvents = 100
	}

	// Use the LATEST local event as the backfill starting point.
	// After a federation join, the latest event is the join event itself.
	// Backfill returns events at or before this point, giving us the room history.
	var latestEvent MatrixEvent
	err := db.GetDB().Where("room_id = ?", roomID).
		Order("origin_server_ts DESC").First(&latestEvent).Error
	if err != nil {
		return
	}

	// 迭代 backfill，直到获取足够的事件或没有更多事件
	fetchedCount := 0
	batchLimit := 100
	startEventIDs := []string{latestEvent.EventID}
	maxIterations := 10 // 防止无限循环

	for i := 0; i < maxIterations && fetchedCount < maxEvents; i++ {
		remaining := maxEvents - fetchedCount
		if remaining < batchLimit {
			batchLimit = remaining
		}

		pdus, err := fedClient.FetchBackfill(serverName, roomID, startEventIDs, batchLimit)
		if err != nil {
			log.Warn("backfillFromRemote: failed to fetch backfill from %s: %v", serverName, err)

			return
		}

		if len(pdus) == 0 {
			// 没有更多事件
			break
		}

		// 找到这批事件中最早的，用于下一次 backfill
		var (
			earliestTS      int64 = -1
			earliestEventID string
		)

		for _, pdu := range pdus {
			eventID, _ := pdu["event_id"].(string)
			if eventID == "" {
				continue
			}
			// Skip events we already have
			var existing MatrixEvent
			if db.GetDB().Where("event_id = ?", eventID).First(&existing).Error == nil {
				continue
			}

			eventType, _ := pdu["type"].(string)
			sender, _ := pdu["sender"].(string)
			originTS, _ := pdu["origin_server_ts"].(float64)

			content, _ := pdu["content"].(map[string]any)
			if content == nil {
				content = map[string]any{}
			}

			event := &MatrixEvent{
				EventID:        eventID,
				RoomID:         roomID,
				EventType:      EventType(eventType),
				Sender:         sender,
				Content:        content,
				OriginServerTS: int64(originTS),
				SenderDomain:   extractServerName(sender),
			}
			if sk, ok := pdu["state_key"].(string); ok {
				event.StateKey = &sk
			}

			if depth, ok := pdu["depth"].(float64); ok {
				event.Depth = int64(depth)
			}

			if prevEvents, ok := pdu["prev_events"].([]any); ok {
				for _, v := range prevEvents {
					if s, ok := v.(string); ok {
						event.PrevEvents = append(event.PrevEvents, s)
					}
				}
			}

			if authEvents, ok := pdu["auth_events"].([]any); ok {
				for _, v := range authEvents {
					if s, ok := v.(string); ok {
						event.AuthEvents = append(event.AuthEvents, s)
					}
				}
			}

			err := db.GetDB().Create(event).Error
			if err != nil {
				log.Error("Failed to store backfilled event: %v", err)

				continue
			}

			fetchedCount++

			// 跟踪最早的事件
			if earliestTS == -1 || int64(originTS) < earliestTS {
				earliestTS = int64(originTS)
				earliestEventID = eventID
			}
		}

		// 如果没有获取到新事件或者没有找到更早的事件，停止
		if earliestEventID == "" {
			break
		}

		// 使用最早的事件作为下一次 backfill 的起点
		startEventIDs = []string{earliestEventID}
	}

	log.Info("Backfilled %d events for room %s from %s", fetchedCount, roomID, serverName)
}

// ===== 房间离开 =====

// LeaveRoomRequest 离开房间请求.
type LeaveRoomRequest struct {
	Reason string `json:"reason"`
}

// LeaveRoom 离开房间.
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
		StateKey:  new(userID),
		Content: map[string]any{
			"membership": "leave", //nolint:goconst
		},
		Depth:          r.getNextDepth(db.GetDB(), roomID),
		OriginServerTS: time.Now().UnixMilli(),
		SenderDomain:   r.HS.Config.ServerName,
	}

	if err := db.GetDB().Create(event).Error; err != nil {
		return ErrInternal
	}

	r.updateRoomState(db.GetDB(), roomID, event)

	r.updateRoomMember(db.GetDB(), roomID, userID, MembershipLeave, event.EventID)
	go r.HS.NotifyRoomEvent(roomID)

	return nil
}

// ===== 邀请 =====

// InviteUserRequest 邀请用户请求.
type InviteUserRequest struct {
	Reason   string         `json:"reason"`
	UserID   string         `json:"user_id"`
	IsDirect *bool          `json:"is_direct"`
	Content  map[string]any `json:"-"`
}

// InviteUser 邀请用户.
func (r *RoomService) InviteUser(roomID, inviterID string, req *InviteUserRequest) *MatrixError {
	// Cannot invite yourself
	if inviterID == req.UserID {
		return ErrForbidden
	}

	// 检查邀请者权限
	if !r.canUserInvite(roomID, inviterID) {
		return ErrForbidden
	}

	// 检查被邀请者是否已在房间中
	var member MatrixRoomMember

	err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, req.UserID).First(&member).Error
	if err == nil {
		if member.Membership == MembershipJoin {
			return ErrForbidden // Cannot invite user already in room
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

// inviteUser 内部邀请函数.
func (r *RoomService) inviteUser(tx *gorm.DB, roomID, inviterID, inviteeID string) error {
	event := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomMember,
		Sender:    inviterID,
		StateKey:  new(inviteeID),
		Content: map[string]any{
			"membership": "invite",
		},
		Depth:          r.getNextDepth(tx, roomID),
		OriginServerTS: time.Now().UnixMilli(),
		SenderDomain:   r.HS.Config.ServerName,
	}

	err := tx.Create(event).Error
	if err != nil {
		return err
	}

	r.updateRoomState(tx, roomID, event)

	r.updateRoomMember(tx, roomID, inviteeID, MembershipInvite, event.EventID)
	go r.HS.NotifyRoomEvent(roomID)

	return nil
}

// ===== 踢人/封禁 =====

// KickUserRequest 踢人请求.
type KickUserRequest struct {
	Reason string `json:"reason"`
	UserID string `json:"user_id"`
}

// KickUser 踢出用户.
func (r *RoomService) KickUser(roomID, kickerID string, req *KickUserRequest) *MatrixError {
	// 检查权限
	if !r.canUserKick(roomID, kickerID) {
		return ErrForbidden
	}

	// 检查被踢者是否在房间中且是 joined 状态
	var member MatrixRoomMember

	err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, req.UserID).First(&member).Error
	if err != nil || member.Membership != MembershipJoin {
		return ErrForbidden
	}

	// 创建 leave 事件
	event := &MatrixEvent{
		EventID:   "$" + uuid.New().String(),
		RoomID:    roomID,
		EventType: EventTypeRoomMember,
		Sender:    kickerID,
		StateKey:  new(req.UserID),
		Content: map[string]any{
			"membership": "leave",
			"reason":     req.Reason,
		},
		Depth:          r.getNextDepth(db.GetDB(), roomID),
		OriginServerTS: time.Now().UnixMilli(),
		SenderDomain:   r.HS.Config.ServerName,
	}

	if err := db.GetDB().Create(event).Error; err != nil {
		return ErrInternal
	}

	r.updateRoomState(db.GetDB(), roomID, event)

	r.updateRoomMember(db.GetDB(), roomID, req.UserID, MembershipLeave, event.EventID)
	go r.HS.NotifyRoomEvent(roomID)

	return nil
}

// BanUserRequest 封禁请求.
type BanUserRequest struct {
	Reason string `json:"reason"`
	UserID string `json:"user_id"`
}

// BanUser 封禁用户.
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
		StateKey:  new(req.UserID),
		Content: map[string]any{
			"membership": "ban",
			"reason":     req.Reason,
		},
		Depth:          r.getNextDepth(db.GetDB(), roomID),
		OriginServerTS: time.Now().UnixMilli(),
		SenderDomain:   r.HS.Config.ServerName,
	}

	err := db.GetDB().Create(event).Error
	if err != nil {
		return ErrInternal
	}

	r.updateRoomState(db.GetDB(), roomID, event)

	r.updateRoomMember(db.GetDB(), roomID, req.UserID, MembershipBan, event.EventID)
	go r.HS.NotifyRoomEvent(roomID)

	return nil
}

// ===== 房间状态 =====

// GetRoomState 获取房间状态.
func (r *RoomService) GetRoomState(roomID string, eventType EventType, stateKey string) (any, *MatrixError) {
	var state MatrixRoomState

	err := db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, eventType, stateKey).First(&state).Error
	if err != nil {
		return nil, ErrNotFound
	}

	return state.Content, nil
}

// GetRoomStateAll 获取所有房间状态（返回完整事件枚举，包含 sender 和 origin_server_ts）.
func (r *RoomService) GetRoomStateAll(roomID string) ([]*ClientEvent, *MatrixError) {
	var states []MatrixRoomState
	db.GetDB().Where("room_id = ?", roomID).Find(&states)

	events := make([]*ClientEvent, 0, len(states))
	for _, state := range states {
		stateKey := state.StateKey
		ce := &ClientEvent{
			EventID:  state.EventID,
			RoomID:   state.RoomID,
			Type:     string(state.EventType),
			StateKey: &stateKey,
			Content:  state.Content,
		}
		// Enrich with sender + origin_server_ts from the events table
		if state.EventID != "" {
			var evt MatrixEvent
			err := db.GetDB().Where("event_id = ?", state.EventID).First(&evt).Error
			if err == nil {
				ce.Sender = evt.Sender
				ce.OriginServerTS = evt.OriginServerTS
			}
		}

		events = append(events, ce)
	}

	return events, nil
}

// ===== 房间成员 =====

// GetRoomMembers 获取房间成员.
func (r *RoomService) GetRoomMembers(roomID string, membership []MembershipState) ([]*MatrixRoomMember, *MatrixError) {
	query := db.GetDB().Where("room_id = ?", roomID)

	if len(membership) > 0 {
		query = query.Where("membership IN ?", membership)
	}

	var members []*MatrixRoomMember
	err := query.Find(&members).Error
	if err != nil {
		return nil, ErrInternal
	}

	return members, nil
}

// GetUserMembership 获取用户在房间中的状态.
func (r *RoomService) GetUserMembership(roomID, userID string) (MembershipState, error) {
	var member MatrixRoomMember

	err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error
	if err != nil {
		return MembershipLeave, nil //nolint:nilerr
	}

	return member.Membership, nil
}

// ===== 权限检查 =====

// canUserInvite 检查用户是否可以邀请.
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

// canUserKick 检查用户是否可以踢人.
func (r *RoomService) canUserKick(roomID, userID string) bool {
	// User must be a joined member to kick
	var member MatrixRoomMember
	if err := db.GetDB().Where("room_id = ? AND user_id = ? AND membership = ?",
		roomID, userID, MembershipJoin).First(&member).Error; err != nil {
		return false
	}

	pl, err := r.getRoomPowerLevels(roomID)
	if err != nil {
		return false
	}

	userPL := r.getUserPowerLevel(pl, userID)

	return userPL >= pl.Kick
}

// canUserBan 检查用户是否可以封禁.
func (r *RoomService) canUserBan(roomID, userID string) bool {
	// User must be a joined member to ban
	var member MatrixRoomMember
	if err := db.GetDB().Where("room_id = ? AND user_id = ? AND membership = ?",
		roomID, userID, MembershipJoin).First(&member).Error; err != nil {
		return false
	}

	pl, err := r.getRoomPowerLevels(roomID)
	if err != nil {
		return false
	}

	userPL := r.getUserPowerLevel(pl, userID)

	return userPL >= pl.Ban
}

// ===== 辅助函数 =====

// getDefaultPowerLevels 获取默认权限级别.
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

// getRoomPowerLevels 获取房间权限级别.
func (r *RoomService) getRoomPowerLevels(roomID string) (*PowerLevelContent, error) { //nolint:unparam // param reserved for future use
	content, err := r.GetRoomState(roomID, EventTypeRoomPowerLevels, "")
	if err != nil {
		return r.getDefaultPowerLevels(), nil
	}

	// Matrix spec defaults: state_default=50, ban=50, kick=50, redact=50, invite=0
	pl := &PowerLevelContent{
		StateDefault: 50,
		Ban:          50,
		Kick:         50,
		Redact:       50,
	}

	if b, ok := content.(map[string]any); ok {
		if users, ok := b["users"].(map[string]any); ok {
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

		if events, ok := b["events"].(map[string]any); ok {
			pl.Events = make(map[string]int64)

			for k, v := range events {
				if fv, ok := v.(float64); ok {
					pl.Events[k] = int64(fv)
				}
			}
		}
	}

	return pl, nil
}

// getUserPowerLevel 获取用户权限级别.
func (r *RoomService) getUserPowerLevel(pl *PowerLevelContent, userID string) int64 {
	if level, ok := pl.Users[userID]; ok {
		return level
	}

	return pl.UsersDefault
}

// updateRoomState 更新房间状态.
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

	// 同步 MatrixRoom 的去正规化字段
	updates := map[string]any{}

	switch event.EventType {
	case EventTypeRoomJoinRules:
		if v, ok := event.Content["join_rule"].(string); ok {
			updates["join_rule"] = v
		}
	case EventTypeRoomHistoryVisibility:
		if v, ok := event.Content["history_visibility"].(string); ok {
			updates["history_visibility"] = v
		}
	case EventTypeRoomGuestAccess:
		if v, ok := event.Content["guest_access"].(string); ok {
			updates["guest_access"] = v
		}
	case EventTypeRoomName:
		if v, ok := event.Content["name"].(string); ok {
			updates["name"] = v
		}
	case EventTypeRoomTopic:
		if v, ok := event.Content["topic"].(string); ok {
			updates["topic"] = v
		}
	case EventTypeRoomAvatar:
		if v, ok := event.Content["url"].(string); ok {
			updates["avatar_url"] = v
		}
	case EventTypeRoomEncryption:
		updates["is_encrypted"] = true
	}

	if len(updates) > 0 {
		tx.Model(&MatrixRoom{}).Where("room_id = ?", roomID).Updates(updates)
	}
}

// updateRoomMember 更新房间成员.
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
		updates := map[string]any{
			"membership": membership,
			"event_id":   eventID, //nolint:goconst
		}
		// Clear forgotten flag when user receives an invite or joins (new membership overrides forget)
		if membership == MembershipInvite || membership == MembershipJoin {
			updates["forgotten"] = false
		}

		tx.Model(&member).Updates(updates)
	}
}

// getNextDepth 获取下一个深度.
func (r *RoomService) getNextDepth(tx *gorm.DB, roomID string) int64 {
	var maxDepth int64
	tx.Model(&MatrixEvent{}).Where("room_id = ?", roomID).Select("COALESCE(MAX(depth), 0)").Scan(&maxDepth)

	return maxDepth + 1
}

// GetRoom 获取房间信息.
func (r *RoomService) GetRoom(roomID string) (*MatrixRoom, error) {
	var room MatrixRoom

	err := db.GetDB().Where("room_id = ?", roomID).First(&room).Error
	if err != nil {
		return nil, err
	}

	return &room, nil
}

// GetJoinedRooms 获取用户加入的房间.
func (r *RoomService) GetJoinedRooms(userID string) ([]string, error) {
	var members []MatrixRoomMember
	db.GetDB().Where("user_id = ? AND membership = ?", userID, MembershipJoin).Find(&members)

	roomIDs := make([]string, len(members))
	for i, m := range members {
		roomIDs[i] = m.RoomID
	}

	return roomIDs, nil
}
