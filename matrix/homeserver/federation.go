package homeserver

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/utils"
)

// FederationService 联邦服务
type FederationService struct {
	HS         *HomeServer
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	keyID      string
}

// NewFederationService 创建联邦服务
func NewFederationService(hs *HomeServer) *FederationService {
	// 使用 HomeServer 中缓存的密钥
	return &FederationService{
		HS:         hs,
		privateKey: hs.privateKey,
		publicKey:  hs.publicKey,
		keyID:      hs.keyID,
	}
}

// RegisterFederationRoutes 注册联邦 API 路由
func RegisterFederationRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}
	
	fed := r.Group("/_matrix/federation")
	{
		fed.GET("/v1/version", handleFederationVersion)
		fed.GET("/v1/query/directory", handleQueryDirectory)
		fed.GET("/v1/query/profile", handleQueryProfile)
		fed.GET("/v1/make_join/:roomId/:userId", handleMakeJoin)
		fed.PUT("/v1/send_join/:roomId/:eventId", handleSendJoinV1)
		fed.PUT("/v2/send_join/:roomId/:eventId", handleSendJoin)
		fed.GET("/v1/make_leave/:roomId/:userId", handleMakeLeave)
		fed.PUT("/v1/send_leave/:roomId/:eventId", handleSendLeave)
		fed.PUT("/v2/send_leave/:roomId/:eventId", handleSendLeave)
		fed.PUT("/v1/invite/:roomId/:eventId", handleFederationInvite)
		fed.PUT("/v2/invite/:roomId/:eventId", handleFederationInvite)
		fed.GET("/v1/state/:roomId", handleGetState)
		fed.GET("/v1/state_ids/:roomId", handleGetStateIDs)
		fed.GET("/v1/event/:eventId", handleGetFederationEvent)
		fed.PUT("/v1/send/:txnId", handleFederationSend)
		fed.GET("/v1/publicRooms", handleGetPublicRooms)
		// Note: /v1/user/devices/:userId is registered by RegisterDeviceListRoutes
		fed.GET("/v1/query/server_key", handleGetServerKey)
		fed.GET("/v1/3pid/onbind", handleThirdPartyOnBind)
	}
	
	// 密钥端点
	r.GET("/_matrix/key/v2/server", handleGetServerKey)
	r.GET("/_matrix/key/v2/server/:keyId", handleGetServerKey)
}

// ===== 服务器密钥 =====

type ServerKeyResponse struct {
	ServerName string            `json:"server_name"`
	ValidUntil int64             `json:"valid_until_ts"`
	VerifyKeys map[string]KeyInfo `json:"verify_keys"`
	OldVerifyKeys map[string]KeyInfo `json:"old_verify_keys"`
	Signatures map[string]map[string]string `json:"signatures"`
}

type KeyInfo struct {
	Key string `json:"key"`
}

func (f *FederationService) GetServerKey() map[string]interface{} {
	fed := NewFederationService(f.HS)

	resp := map[string]interface{}{
		"server_name":    f.HS.Config.ServerName,
		"valid_until_ts": time.Now().Add(24 * time.Hour).UnixMilli(),
		"verify_keys": map[string]interface{}{
			fed.keyID: map[string]interface{}{
				"key": base64.RawStdEncoding.EncodeToString(fed.publicKey),
			},
		},
		"old_verify_keys": map[string]interface{}{},
	}

	sig := fed.signJSON(resp)
	resp["signatures"] = map[string]interface{}{
		f.HS.Config.ServerName: map[string]interface{}{
			fed.keyID: sig,
		},
	}

	return resp
}

func (f *FederationService) signJSON(data interface{}) string {
	// 按照 Matrix 规范：1) 转为 map 2) 移除 signatures/unsigned 3) canonical JSON 4) 直接签名
	var objMap map[string]interface{}
	switch v := data.(type) {
	case map[string]interface{}:
		objMap = make(map[string]interface{})
		for k, val := range v {
			objMap[k] = val
		}
	default:
		jsonBytes, _ := json.Marshal(data)
		json.Unmarshal(jsonBytes, &objMap)
	}

	delete(objMap, "signatures")
	delete(objMap, "unsigned")

	canonicalBytes, err := CanonicalJSON(objMap)
	if err != nil {
		return ""
	}

	signature := ed25519.Sign(f.privateKey, canonicalBytes)
	return base64.RawStdEncoding.EncodeToString(signature)
}

// signEvent 签名事件并将签名嵌入事件
func (f *FederationService) signEvent(event map[string]interface{}) map[string]interface{} {
	sig := f.signJSON(event)
	sigs, _ := event["signatures"].(map[string]interface{})
	if sigs == nil {
		sigs = make(map[string]interface{})
	}
	sigs[f.HS.Config.ServerName] = map[string]interface{}{
		f.keyID: sig,
	}
	event["signatures"] = sigs
	return event
}

func handleGetServerKey(c *gin.Context) {
	hs := GetHomeServer()
	fed := NewFederationService(hs)
	c.JSON(http.StatusOK, fed.GetServerKey())
}

// ===== 版本信息 =====

func handleFederationVersion(c *gin.Context) {
	_ = GetHomeServer() // hs - unused but kept for potential future use
	c.JSON(http.StatusOK, gin.H{
		"server": gin.H{
			"name":    "Community Matrix Homeserver",
			"version": "1.0.0",
		},
	})
}

// ===== 目录查询 =====

type QueryDirectoryResponse struct {
	RoomID  string   `json:"room_id"`
	Servers []string `json:"servers"`
}

func handleQueryDirectory(c *gin.Context) {
	roomAlias := c.Query("room_alias")
	hs := GetHomeServer()
	
	// 查找房间别名对应的房间
	var roomID string
	var servers []string
	
	// 检查是否是本地别名
	if strings.HasSuffix(roomAlias, ":"+hs.Config.ServerName) {
		// 解析别名
		localpart := strings.TrimPrefix(roomAlias, "#")
		localpart = strings.Split(localpart, ":")[0]
		
		// 查找房间
		aliasService := NewAliasService(hs)
		resolvedRoomID, err := aliasService.GetRoomIDByAlias(roomAlias)
		if err == nil {
			roomID = resolvedRoomID
			servers = []string{hs.Config.ServerName}
		}
		_ = localpart
	}
	
	if roomID == "" {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room alias not found",
		})
		return
	}
	
	c.JSON(http.StatusOK, QueryDirectoryResponse{
		RoomID:  roomID,
		Servers: servers,
	})
}

// ===== 用户档案查询 =====

type QueryProfileResponse struct {
	DisplayName string `json:"displayname"`
	AvatarURL   string `json:"avatar_url"`
}

func handleQueryProfile(c *gin.Context) {
	userID := c.Query("user_id")
	hs := GetHomeServer()
	
	// 解析用户 ID
	localpart, domain, ok := ParseMatrixUserID(userID)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_INVALID_PARAM",
			"error":   "Invalid user ID",
		})
		return
	}
	
	// 检查是否是本地用户
	if domain != hs.Config.ServerName {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "User not found",
		})
		return
	}
	
	// 获取用户档案
	accountID, err := hs.Auth.GetLocalAccountID(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "User not found",
		})
		return
	}
	
	_ = accountID
	_ = localpart
	
	c.JSON(http.StatusOK, QueryProfileResponse{
		DisplayName: localpart,
		AvatarURL:   "",
	})
}

// ===== 加入房间 =====

type MakeJoinResponse struct {
	Origin         string `json:"origin"`
	OriginServerTS int64  `json:"origin_server_ts"`
	Event          gin.H  `json:"event"`
	RoomVersion    string `json:"room_version"` // Required by Matrix spec at top level
}

func handleMakeJoin(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := c.Param("userId")
	hs := GetHomeServer()
	
	// 检查房间是否存在
	room, err := hs.Room.GetRoom(roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found",
		})
		return
	}
	
	// 创建加入事件模板（不含 room_version，room_version 在顶层返回）
	event := gin.H{
		"type":             "m.room.member",
		"room_id":          roomID,
		"sender":           userID,
		"state_key":        userID,
		"content":          gin.H{"membership": "join"},
		"origin":           hs.Config.ServerName,
		"origin_server_ts": time.Now().UnixMilli(),
		"depth":            1,
		"auth_events":      []string{},
		"prev_events":      []string{},
	}

	roomVersion := room.RoomVersion
	if roomVersion == "" {
		roomVersion = "10"
	}

	c.JSON(http.StatusOK, MakeJoinResponse{
		Origin:         hs.Config.ServerName,
		OriginServerTS: time.Now().UnixMilli(),
		Event:          event,
		RoomVersion:    roomVersion,
	})
}

type SendJoinResponse struct {
	Origin string   `json:"origin"`
	State  []gin.H  `json:"state"`
	AuthChain []gin.H `json:"auth_chain"`
}

func handleSendJoin(c *gin.Context) {
	roomID := c.Param("roomId")

	// 解析请求体
	var event map[string]interface{}
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})
		return
	}

	hs := GetHomeServer()

	// 验证事件签名
	sender, _ := event["sender"].(string)
	origin, _ := event["origin"].(string)
	if origin == "" {
		origin = hs.Config.ServerName
	}

	// 在生产环境中，应该验证签名
	// 这里简化处理，直接处理加入事件

	// 处理加入事件
	userID, _ := event["sender"].(string)
	content, _ := event["content"].(map[string]interface{})
	if content == nil {
		content = map[string]interface{}{"membership": "join"}
	}
	displayName, _ := content["displayname"].(string)
	avatarURL, _ := content["avatar_url"].(string)

	// 存储加入事件到事件表（如果不存在）
	eventID, _ := event["event_id"].(string)
	if eventID == "" {
		eventID = "$" + generateRandomString(32)
	}
	originServerTS, _ := event["origin_server_ts"].(float64)
	if originServerTS == 0 {
		originServerTS = float64(time.Now().UnixMilli())
	}
	senderDomain := extractServerName(userID)
	stateKeyStr := userID
	joinEvent := &MatrixEvent{
		EventID:        eventID,
		RoomID:         roomID,
		EventType:      EventTypeRoomMember,
		Sender:         userID,
		StateKey:       &stateKeyStr,
		Content:        content,
		OriginServerTS: int64(originServerTS),
		SenderDomain:   senderDomain,
		Depth:          hs.Room.getNextDepth(db.GetDB(), roomID),
	}
	if db.GetDB().Where("event_id = ?", eventID).First(&MatrixEvent{}).Error != nil {
		if err := db.GetDB().Create(joinEvent).Error; err == nil {
			hs.Room.updateRoomState(db.GetDB(), roomID, joinEvent)
		}
	}

	// 更新房间成员
	member := &MatrixRoomMember{
		RoomID:      roomID,
		UserID:      userID,
		Membership:  MembershipJoin,
		DisplayName: &displayName,
		AvatarURL:   &avatarURL,
		EventID:     eventID,
	}
	db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).Assign(member).FirstOrCreate(member)

	// 返回房间状态
	state, _ := hs.Room.GetRoomStateAll(roomID)
	stateEvents := make([]gin.H, len(state))
	for i, e := range state {
		stateEvents[i] = gin.H{
			"event_id":         e.EventID,
			"type":             e.Type,
			"room_id":          e.RoomID,
			"sender":           e.Sender,
			"state_key":        e.StateKey,
			"content":          e.Content,
			"origin_server_ts": e.OriginServerTS,
			"auth_events":      []string{},
			"prev_events":      []string{},
			"depth":            0,
		}
	}

	// 构建 auth chain
	authChain := buildAuthChainForRoom(roomID, state)

	c.JSON(http.StatusOK, SendJoinResponse{
		Origin:    hs.Config.ServerName,
		State:     stateEvents,
		AuthChain: authChain,
	})

	_ = sender
	_ = origin
}

// handleSendJoinV1 wraps the v2 send_join response in a [200, {...}] array per the v1 spec
func handleSendJoinV1(c *gin.Context) {
	roomID := c.Param("roomId")

	var event map[string]interface{}
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": "Bad JSON"})
		return
	}

	hs := GetHomeServer()
	sender, _ := event["sender"].(string)
	origin, _ := event["origin"].(string)
	if origin == "" {
		origin = hs.Config.ServerName
	}

	userID, _ := event["sender"].(string)
	content, _ := event["content"].(map[string]interface{})
	if content == nil {
		content = map[string]interface{}{"membership": "join"}
	}
	displayName, _ := content["displayname"].(string)
	avatarURL, _ := content["avatar_url"].(string)

	eventID, _ := event["event_id"].(string)
	if eventID == "" {
		eventID = "$" + generateRandomString(32)
	}
	originServerTS, _ := event["origin_server_ts"].(float64)
	if originServerTS == 0 {
		originServerTS = float64(time.Now().UnixMilli())
	}
	senderDomain := extractServerName(userID)
	stateKeyStr := userID
	joinEvent := &MatrixEvent{
		EventID:        eventID,
		RoomID:         roomID,
		EventType:      EventTypeRoomMember,
		Sender:         userID,
		StateKey:       &stateKeyStr,
		Content:        content,
		OriginServerTS: int64(originServerTS),
		SenderDomain:   senderDomain,
		Depth:          hs.Room.getNextDepth(db.GetDB(), roomID),
	}
	if db.GetDB().Where("event_id = ?", eventID).First(&MatrixEvent{}).Error != nil {
		if err := db.GetDB().Create(joinEvent).Error; err == nil {
			hs.Room.updateRoomState(db.GetDB(), roomID, joinEvent)
		}
	}

	member := &MatrixRoomMember{
		RoomID:      roomID,
		UserID:      userID,
		Membership:  MembershipJoin,
		DisplayName: &displayName,
		AvatarURL:   &avatarURL,
		EventID:     eventID,
	}
	db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).Assign(member).FirstOrCreate(member)

	state, _ := hs.Room.GetRoomStateAll(roomID)
	stateEvents := make([]gin.H, len(state))
	for i, e := range state {
		stateEvents[i] = gin.H{
			"event_id": e.EventID, "type": e.Type, "room_id": e.RoomID,
			"sender": e.Sender, "state_key": e.StateKey, "content": e.Content,
			"origin_server_ts": e.OriginServerTS, "auth_events": []string{},
			"prev_events": []string{}, "depth": 0,
		}
	}
	authChain := buildAuthChainForRoom(roomID, state)

	// V1 format: wrap response in [200, {...}]
	c.JSON(http.StatusOK, []interface{}{
		200,
		SendJoinResponse{
			Origin:    hs.Config.ServerName,
			State:     stateEvents,
			AuthChain: authChain,
		},
	})

	_ = sender
	_ = origin
}

// ===== 离开房间 =====

func handleMakeLeave(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := c.Param("userId")
	hs := GetHomeServer()
	
	// 检查房间是否存在
	_, err := hs.Room.GetRoom(roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found",
		})
		return
	}
	
	// 创建离开事件模板
	event := gin.H{
		"type":      "m.room.member",
		"room_id":   roomID,
		"sender":    userID,
		"state_key": userID,
		"content": gin.H{
			"membership": "leave",
		},
		"origin":          hs.Config.ServerName,
		"origin_server_ts": time.Now().UnixMilli(),
	}
	
	c.JSON(http.StatusOK, MakeJoinResponse{
		Origin:          hs.Config.ServerName,
		OriginServerTS:  time.Now().UnixMilli(),
		Event:           event,
	})
}

func handleSendLeave(c *gin.Context) {
	roomID := c.Param("roomId")

	// 解析请求体
	var event map[string]interface{}
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})
		return
	}

	// 验证并处理离开事件
	userID, _ := event["sender"].(string)

	// 更新房间成员状态
	db.GetDB().Model(&MatrixRoomMember{}).
		Where("room_id = ? AND user_id = ?", roomID, userID).
		Update("membership", MembershipLeave)

	c.JSON(http.StatusOK, gin.H{})
}

// ===== 邀请 =====

func handleFederationInvite(c *gin.Context) {
	roomID := c.Param("roomId")

	// 解析请求体
	var event map[string]interface{}
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})
		return
	}

	hs := GetHomeServer()

	// 获取被邀请用户
	stateKey, _ := event["state_key"].(string)
	content, _ := event["content"].(map[string]interface{})
	sender, _ := event["sender"].(string)
	displayName, _ := content["displayname"].(string)
	avatarURL, _ := content["avatar_url"].(string)

	// 创建邀请成员记录
	member := &MatrixRoomMember{
		RoomID:      roomID,
		UserID:      stateKey,
		Membership:  MembershipInvite,
		DisplayName: &displayName,
		AvatarURL:   &avatarURL,
	}
	db.GetDB().Where("room_id = ? AND user_id = ?", roomID, stateKey).Assign(member).FirstOrCreate(member)

	// 创建邀请事件
	inviteEvent := &MatrixEvent{
		EventID:       fmt.Sprintf("$%s", generateEventID()),
		RoomID:        roomID,
		EventType:     EventTypeRoomMember,
		Sender:        sender,
		StateKey:      &stateKey,
		Content:       content,
		OriginServerTS: time.Now().UnixMilli(),
	}
	db.GetDB().Create(inviteEvent)

	_ = hs

	c.JSON(http.StatusOK, gin.H{
		"event": gin.H{
			"event_id":        inviteEvent.EventID,
			"room_id":         roomID,
			"type":            "m.room.member",
			"sender":          sender,
			"state_key":       stateKey,
			"content":         content,
			"origin_server_ts": inviteEvent.OriginServerTS,
		},
	})
}

// buildAuthChainForRoom 根据房间状态事件构建 auth chain
func buildAuthChainForRoom(roomID string, stateEvents []*ClientEvent) []gin.H {
	resolver := NewStateResolver(roomID)
	authChainMap := make(map[string]*MatrixEvent)

	for _, e := range stateEvents {
		evt, err := resolver.loadEvent(e.EventID)
		if err != nil {
			continue
		}
		chain, err := resolver.BuildAuthChain(evt)
		if err != nil {
			continue
		}
		for _, ae := range chain {
			authChainMap[ae.EventID] = ae
		}
	}

	authChain := make([]gin.H, 0, len(authChainMap))
	for _, e := range authChainMap {
		authChain = append(authChain, gin.H{
			"event_id":         e.EventID,
			"type":             e.EventType,
			"room_id":          e.RoomID,
			"sender":           e.Sender,
			"state_key":        e.StateKey,
			"content":          e.Content,
			"origin_server_ts": e.OriginServerTS,
			"auth_events":      e.AuthEvents,
			"depth":            e.Depth,
		})
	}

	return authChain
}

// ===== 状态获取 =====

func handleGetState(c *gin.Context) {
	roomID := c.Param("roomId")
	hs := GetHomeServer()
	
	// 获取房间状态
	state, _ := hs.Room.GetRoomStateAll(roomID)
	
	stateEvents := make([]gin.H, len(state))
	for i, e := range state {
		stateEvents[i] = gin.H{
			"event_id":   e.EventID,
			"type":       e.Type,
			"room_id":    e.RoomID,
			"sender":     e.Sender,
			"state_key":  e.StateKey,
			"content":    e.Content,
			"origin_server_ts": e.OriginServerTS,
		}
	}
	
	// 构建 auth chain
	authChain := buildAuthChainForRoom(roomID, state)

	c.JSON(http.StatusOK, gin.H{
		"origin":     hs.Config.ServerName,
		"pdus":       stateEvents,
		"auth_chain": authChain,
	})
}

func handleGetStateIDs(c *gin.Context) {
	roomID := c.Param("roomId")
	hs := GetHomeServer()
	
	// 获取房间状态
	state, _ := hs.Room.GetRoomStateAll(roomID)
	
	stateEventIDs := make([]string, len(state))
	for i, e := range state {
		stateEventIDs[i] = e.EventID
	}
	
	// 构建 auth chain IDs
	authChainEvents := buildAuthChainForRoom(roomID, state)
	authChainIDs := make([]string, 0, len(authChainEvents))
	for _, e := range authChainEvents {
		if id, ok := e["event_id"].(string); ok {
			authChainIDs = append(authChainIDs, id)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"origin":          hs.Config.ServerName,
		"pdu_ids":         stateEventIDs,
		"auth_chain_ids":  authChainIDs,
	})
}

// ===== 事件获取 =====

func handleGetFederationEvent(c *gin.Context) {
	eventID := c.Param("eventId")
	hs := GetHomeServer()
	
	event, err := hs.Event.GetEvent(eventID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Event not found",
		})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{
		"origin": hs.Config.ServerName,
		"origin_server_ts": time.Now().UnixMilli(),
		"pdus": []gin.H{
			{
				"event_id":        event.EventID,
				"room_id":         event.RoomID,
				"type":            event.Type,
				"sender":          event.Sender,
				"state_key":       event.StateKey,
				"content":         event.Content,
				"origin_server_ts": event.OriginServerTS,
			},
		},
	})
}

// ===== 事务发送 =====

type FederationTransaction struct {
	Origin      string             `json:"origin"`
	OriginServerTS int64           `json:"origin_server_ts"`
	PDUs        []json.RawMessage  `json:"pdus"`
	EDUs        []json.RawMessage  `json:"edus"`
}

// computeReferenceHash computes the event ID for room version 4+ per the Matrix spec:
// 1. Redact the event (keep only allowed top-level keys and type-specific content keys)
// 2. Remove signatures, unsigned, and _-prefixed keys
// 3. Canonical JSON → SHA-256 → "$" + base64url(unpadded)
func computeReferenceHash(event map[string]interface{}, eventType string) string {
	// Allowed top-level keys after redaction (room version 4+)
	// Per Matrix spec: event_id, type, room_id, sender, state_key, content,
	// hashes, signatures, depth, prev_events, auth_events, origin_server_ts
	allowedKeys := map[string]bool{
		"event_id": true, "type": true, "room_id": true, "sender": true,
		"state_key": true, "content": true, "hashes": true, "signatures": true,
		"depth": true, "prev_events": true, "auth_events": true,
		"origin_server_ts": true,
	}

	// Content keys protected from redaction, by event type
	protectedContent := map[string][]string{
		"m.room.member":               {"membership", "join_authorised_via_users_server", "third_party_invite"},
		"m.room.create":               {"creator"},
		"m.room.join_rules":           {"join_rule", "allow"},
		"m.room.power_levels":         {"ban", "events", "events_default", "invite", "kick", "redact", "state_default", "users", "users_default"},
		"m.room.history_visibility":   {"history_visibility"},
		"m.room.redaction":            {},
		"m.room.message":              {"msgtype", "body"},
	}

	redacted := make(map[string]interface{})
	for k, v := range event {
		if !allowedKeys[k] {
			continue
		}
		if strings.HasPrefix(k, "_") {
			continue
		}
		redacted[k] = v
	}

	// Redact content: keep only protected keys for this event type
	if origContent, ok := event["content"].(map[string]interface{}); ok {
		newContent := make(map[string]interface{})
		if protected, ok := protectedContent[eventType]; ok {
			for _, key := range protected {
				if val, exists := origContent[key]; exists {
					newContent[key] = val
				}
			}
		}
		redacted["content"] = newContent
	}

	// Remove signatures and unsigned (step 2)
	delete(redacted, "signatures")
	delete(redacted, "unsigned")

	canonicalBytes, err := CanonicalJSON(redacted)
	if err != nil {
		return "$" + fmt.Sprintf("%x", time.Now().UnixNano())
	}
	hash := sha256.Sum256(canonicalBytes)
	return "$" + base64.RawURLEncoding.EncodeToString(hash[:])
}

// parseFederationPDU extracts fields from a raw PDU map and computes event_id for room v4+.
func parseFederationPDU(event map[string]interface{}) (eventID, roomID, eventType, sender string, stateKey string, hasStateKey bool, content map[string]interface{}, originServerTS float64) {
	eventID, _ = event["event_id"].(string)
	roomID, _ = event["room_id"].(string)
	eventType, _ = event["type"].(string)
	sender, _ = event["sender"].(string)
	stateKey, hasStateKey = event["state_key"].(string)
	if !hasStateKey {
		if val, exists := event["state_key"]; exists && val != nil {
			hasStateKey = true
		}
	}
	content, _ = event["content"].(map[string]interface{})
	originServerTS, _ = event["origin_server_ts"].(float64)

	// Room version 4+: compute event_id from reference hash
	if eventID == "" {
		eventID = computeReferenceHash(event, eventType)
		event["event_id"] = eventID
	}
	return
}

// storeFederationPDU stores a single federation event and updates state/membership.
// Returns the event ID and any error.
func storeFederationPDU(hs *HomeServer, event map[string]interface{}) (string, error) {
	eventID, roomID, eventType, sender, stateKey, hasStateKey, content, originServerTS := parseFederationPDU(event)

	if eventID == "" || roomID == "" || eventType == "" || sender == "" {
		return eventID, fmt.Errorf("missing required fields")
	}

	// Check if event already exists
	var existing MatrixEvent
	if db.GetDB().Where("event_id = ?", eventID).First(&existing).Error == nil {
		return eventID, nil // already stored
	}

	// Ensure room record exists
	var existingRoom MatrixRoom
	if db.GetDB().Where("room_id = ?", roomID).First(&existingRoom).Error != nil {
		newRoom := MatrixRoom{RoomID: roomID, RoomVersion: "10"}
		if eventType == "m.room.create" {
			newRoom.Creator = sender
			if ver, ok := content["room_version"].(string); ok {
				newRoom.RoomVersion = ver
			}
		}
		db.GetDB().Create(&newRoom)
	}

	// Extract additional fields
	var redactsPtr *string
	if r, ok := event["redacts"].(string); ok && r != "" {
		redactsPtr = &r
	}
	var depth int64
	if d, ok := event["depth"].(float64); ok {
		depth = int64(d)
	}
	var prevEvents []string
	if pe, ok := event["prev_events"].([]interface{}); ok {
		for _, v := range pe {
			if s, ok := v.(string); ok {
				prevEvents = append(prevEvents, s)
			}
		}
	}
	var authEvents []string
	if ae, ok := event["auth_events"].([]interface{}); ok {
		for _, v := range ae {
			if s, ok := v.(string); ok {
				authEvents = append(authEvents, s)
			}
		}
	}

	var stateKeyPtr *string
	if hasStateKey {
		stateKeyPtr = &stateKey
	}
	matrixEvent := &MatrixEvent{
		EventID:        eventID,
		RoomID:         roomID,
		EventType:      EventType(eventType),
		Sender:         sender,
		StateKey:       stateKeyPtr,
		Content:        content,
		OriginServerTS: int64(originServerTS),
		SenderDomain:   extractServerName(sender),
		Redacts:        redactsPtr,
		Depth:          depth,
		PrevEvents:     prevEvents,
		AuthEvents:     authEvents,
	}

	if err := db.GetDB().Create(matrixEvent).Error; err != nil {
		return eventID, err
	}

	// Handle redaction
	if eventType == "m.room.redaction" {
		redactsID, _ := content["redacts"].(string)
		if redactsID == "" {
			if r, ok := event["redacts"].(string); ok {
				redactsID = r
			}
		}
		if redactsID != "" {
			db.GetDB().Exec("UPDATE matrix_events SET content = '{}' WHERE event_id = ? AND room_id = ?", redactsID, roomID)
		}
	}

	// Handle tombstone: migrate push rules
	if eventType == "m.room.tombstone" {
		if replacementRoom, ok := content["replacement_room"].(string); ok && replacementRoom != "" {
			if hs.RoomUpgrade != nil {
				hs.RoomUpgrade.migratePushRules(roomID, replacementRoom)
			}
		}
	}

	// Update state
	if hasStateKey {
		roomState := &MatrixRoomState{
			RoomID:    roomID,
			EventType: EventType(eventType),
			StateKey:  stateKey,
			Content:   content,
			EventID:   eventID,
		}
		db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, eventType, stateKey).
			Assign(roomState).FirstOrCreate(roomState)

		if eventType == "m.room.member" {
			membership, _ := content["membership"].(string)
			displayName, _ := content["displayname"].(string)
			avatarURL, _ := content["avatar_url"].(string)
			member := &MatrixRoomMember{
				RoomID:      roomID,
				UserID:      stateKey,
				Membership:  MembershipState(membership),
				DisplayName: utils.GetPtrIfNotEmpty(displayName),
				AvatarURL:   utils.GetPtrIfNotEmpty(avatarURL),
				EventID:     eventID,
			}
			db.GetDB().Where("room_id = ? AND user_id = ?", roomID, stateKey).
				Assign(member).FirstOrCreate(member)
		}
	}

	return eventID, nil
}

// fetchMissingEvents detects gaps in prev_events and fetches missing events from the origin server.
func fetchMissingEvents(hs *HomeServer, event map[string]interface{}, origin string) {
	roomID, _ := event["room_id"].(string)
	if roomID == "" || origin == "" || origin == hs.Config.ServerName {
		return
	}

	// Collect prev_events
	var prevEventIDs []string
	if pe, ok := event["prev_events"].([]interface{}); ok {
		for _, v := range pe {
			if s, ok := v.(string); ok {
				prevEventIDs = append(prevEventIDs, s)
			}
		}
	}
	if len(prevEventIDs) == 0 {
		return
	}

	// Check which prev_events we're missing
	var knownIDs []string
	db.GetDB().Model(&MatrixEvent{}).Where("event_id IN ?", prevEventIDs).Pluck("event_id", &knownIDs)
	knownSet := make(map[string]bool, len(knownIDs))
	for _, id := range knownIDs {
		knownSet[id] = true
	}

	hasMissing := false
	for _, id := range prevEventIDs {
		if !knownSet[id] {
			hasMissing = true
			break
		}
	}
	if !hasMissing {
		return
	}

	// Get our earliest known events in this room for the boundary
	var earliestEvents []string
	db.GetDB().Model(&MatrixEvent{}).Where("room_id = ?", roomID).
		Order("origin_server_ts ASC").Limit(20).Pluck("event_id", &earliestEvents)
	if len(earliestEvents) == 0 {
		return
	}

	// Compute event ID for the latest event
	eventID, _ := event["event_id"].(string)
	if eventID == "" {
		eventType, _ := event["type"].(string)
		eventID = computeReferenceHash(event, eventType)
	}

	// Fetch missing events from origin server
	fedClient := NewFederationClient(hs)
	missingPDUs, err := fedClient.GetMissingEvents(origin, roomID, earliestEvents, []string{eventID}, 100)
	if err != nil {
		return
	}

	// Store each missing event (they come in chronological order from oldest to newest)
	for _, rawPDU := range missingPDUs {
		var missingEvent map[string]interface{}
		if json.Unmarshal(rawPDU, &missingEvent) != nil {
			continue
		}
		storeFederationPDU(hs, missingEvent)
	}
}

func handleFederationSend(c *gin.Context) {
	var txn FederationTransaction
	if err := c.ShouldBindJSON(&txn); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})
		return
	}

	hs := GetHomeServer()
	pduResults := make(map[string]interface{})
	notifiedRooms := make(map[string]bool)

	// Process PDUs
	for _, pdu := range txn.PDUs {
		var event map[string]interface{}
		if err := json.Unmarshal(pdu, &event); err != nil {
			continue
		}

		// Detect and fill gaps in prev_events before storing
		fetchMissingEvents(hs, event, txn.Origin)

		eventID, err := storeFederationPDU(hs, event)
		if err != nil {
			if eventID != "" {
				pduResults[eventID] = gin.H{"error": err.Error()}
			}
			continue
		}
		pduResults[eventID] = gin.H{}

		// Notify local users
		roomID, _ := event["room_id"].(string)
		if roomID != "" && !notifiedRooms[roomID] {
			notifiedRooms[roomID] = true
			go hs.NotifyRoomEvent(roomID)
		}
	}

	// 处理 EDUs（临时事件）
	for _, edu := range txn.EDUs {
		var eduEvent map[string]interface{}
		if err := json.Unmarshal(edu, &eduEvent); err != nil {
			continue
		}

		eduType, _ := eduEvent["edu_type"].(string)
		content, _ := eduEvent["content"].(map[string]interface{})

		switch eduType {
		case "m.typing":
			// 处理 typing 事件
			roomID, _ := content["room_id"].(string)
			userIDs, _ := content["user_ids"].([]interface{})
			for _, uid := range userIDs {
				userID, _ := uid.(string)
				typing := &MatrixTyping{
					RoomID:    roomID,
					UserID:    userID,
					Timeout:   30000,
					ExpiresAt: time.Now().Add(30 * time.Second),
				}
				db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).Assign(typing).FirstOrCreate(typing)
			}
		case "m.presence":
			// 处理 presence 事件
			userID, _ := content["user_id"].(string)
			presence, _ := content["presence"].(string)
			if userID != "" && presence != "" {
				presenceRecord := &MatrixPresence{
					UserID:       userID,
					Presence:     presence,
					LastActiveTS: time.Now().UnixMilli(),
				}
				db.GetDB().Where("user_id = ?", userID).Assign(presenceRecord).FirstOrCreate(presenceRecord)
			}
		case "m.receipt":
			// Process federated receipt EDUs
			// Content format: { "room_id": { "event_id": { "m.read": { "user_id": { "ts": 12345, "thread_id": "..." } } } } }
			receiptRooms := make(map[string]bool)
			for roomID, roomData := range content {
				roomMap, ok := roomData.(map[string]interface{})
				if !ok {
					continue
				}
				for eventID, eventData := range roomMap {
					eventMap, ok := eventData.(map[string]interface{})
					if !ok {
						continue
					}
					for receiptType, typeData := range eventMap {
						typeMap, ok := typeData.(map[string]interface{})
						if !ok {
							continue
						}
						for fedUserID, userData := range typeMap {
							userMap, ok := userData.(map[string]interface{})
							if !ok {
								continue
							}
							ts, _ := userMap["ts"].(float64)
							threadID, _ := userMap["thread_id"].(string)

							// Store the receipt locally
							if threadID != "" {
								db.GetDB().Where("room_id = ? AND user_id = ? AND receipt_type = ? AND thread_id = ?",
									roomID, fedUserID, receiptType, threadID).Delete(&MatrixReceipt{})
							} else {
								db.GetDB().Where("room_id = ? AND user_id = ? AND receipt_type = ? AND (thread_id = '' OR thread_id IS NULL)",
									roomID, fedUserID, receiptType).Delete(&MatrixReceipt{})
							}
							receipt := &MatrixReceipt{
								RoomID:      roomID,
								EventID:     eventID,
								UserID:      fedUserID,
								ReceiptType: receiptType,
								ThreadID:    threadID,
								TS:          int64(ts),
							}
							db.GetDB().Create(receipt)
							receiptRooms[roomID] = true
						}
					}
				}
			}
			// Notify local users in rooms that received receipts
			for roomID := range receiptRooms {
				go hs.NotifyRoomEvent(roomID)
			}
		case "m.device_list_update":
			// Process incoming device list update EDU
			var edu DeviceListUpdateEDU
			eduBytes, _ := json.Marshal(content)
			if json.Unmarshal(eduBytes, &edu) == nil {
				origin, _ := eduEvent["origin"].(string)
				if origin == "" {
					origin = txn.Origin
				}
				hs.DeviceList.HandleIncomingDeviceListUpdate(origin, &edu)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"pdus": pduResults,
	})
}

// ===== 公共房间 =====

func handleGetPublicRooms(c *gin.Context) {
	hs := GetHomeServer()

	// 解析分页参数
	since := c.Query("since")
	limit := 100
	if l := c.Query("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	// 获取公共房间
	var rooms []MatrixRoom
	db.GetDB().Where("join_rule = ?", "public").
		Order("created_at DESC").
		Limit(limit).
		Find(&rooms)

	// 构建响应
	chunk := make([]gin.H, len(rooms))
	for i, room := range rooms {
		// 获取成员数
		var joinedCount int64
		db.GetDB().Model(&MatrixRoomMember{}).
			Where("room_id = ? AND membership = ?", room.RoomID, MembershipJoin).
			Count(&joinedCount)

		// 获取头像 URL
		avatarURL := ""
		if room.AvatarURL != nil {
			avatarURL = *room.AvatarURL
		}

		// 获取房间名称
		name := ""
		if room.Name != nil {
			name = *room.Name
		}

		chunk[i] = gin.H{
			"room_id":          room.RoomID,
			"name":             name,
			"topic":            room.Topic,
			"avatar_url":       avatarURL,
			"joined_members":   joinedCount,
			"world_readable":   room.HistoryVisibility == "world_readable",
			"guest_can_join":   room.GuestAccess == "can_join",
			"join_rule":        room.JoinRule,
			"room_type":        room.RoomType,
		}
	}

	// 生成下一页 token
	nextBatch := ""
	if len(rooms) == limit {
		nextBatch = fmt.Sprintf("s%d", time.Now().UnixMilli())
	}

	c.JSON(http.StatusOK, gin.H{
		"origin":     hs.Config.ServerName,
		"chunk":      chunk,
		"total_room_count_estimate": len(rooms),
		"next_batch": nextBatch,
		"prev_batch": since,
	})

	_ = since
}

// ===== 用户设备 =====

type UserDevicesResponse struct {
	UserID   string          `json:"user_id"`
	Devices  []DeviceKeyInfo `json:"devices"`
	MasterKey interface{}    `json:"master_key,omitempty"`
	SelfSigningKey interface{} `json:"self_signing_key,omitempty"`
}

type DeviceKeyInfo struct {
	DeviceID  string            `json:"device_id"`
	Keys      map[string]string `json:"keys"`
	Algorithms []string         `json:"algorithms"`
	Signatures map[string]map[string]string `json:"signatures"`
	DisplayName string          `json:"device_display_name,omitempty"`
}

func handleGetUserDevices(c *gin.Context) {
	userID := c.Param("userId")
	hs := GetHomeServer()
	
	// 解析用户 ID
	_, domain, ok := ParseMatrixUserID(userID)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_INVALID_PARAM",
			"error":   "Invalid user ID",
		})
		return
	}
	
	// 检查是否是本地用户
	if domain != hs.Config.ServerName {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "User not found",
		})
		return
	}
	
	// 获取用户设备
	c.JSON(http.StatusOK, UserDevicesResponse{
		UserID:  userID,
		Devices: []DeviceKeyInfo{},
	})
}

// ===== 第三方网络 =====

func handleThirdPartyOnBind(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// ===== 辅助函数 =====

// ParseMatrixUserID 解析 Matrix 用户 ID
// 注意：这个函数在 auth.go 中已定义，这里为了避免重复，使用外部定义的版本
