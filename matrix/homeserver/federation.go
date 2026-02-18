package homeserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
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
	// 生成或加载服务器密钥
	// 在生产环境中应该从持久化存储加载
	_, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	
	return &FederationService{
		HS:         hs,
		privateKey: privateKey,
		publicKey:  privateKey.Public().(ed25519.PublicKey),
		keyID:      fmt.Sprintf("ed25519:%d", time.Now().Unix()),
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
		fed.PUT("/v1/send_join/:roomId/:eventId", handleSendJoin)
		fed.GET("/v1/make_leave/:roomId/:userId", handleMakeLeave)
		fed.PUT("/v1/send_leave/:roomId/:eventId", handleSendLeave)
		fed.PUT("/v1/invite/:roomId/:eventId", handleFederationInvite)
		fed.GET("/v1/state/:roomId", handleGetState)
		fed.GET("/v1/state_ids/:roomId", handleGetStateIDs)
		fed.GET("/v1/event/:eventId", handleGetFederationEvent)
		fed.PUT("/v1/send/:txnId", handleFederationSend)
		fed.GET("/v1/publicRooms", handleGetPublicRooms)
		fed.GET("/v1/user/devices/:userId", handleGetUserDevices)
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

func (f *FederationService) GetServerKey() *ServerKeyResponse {
	fed := NewFederationService(f.HS)
	
	resp := &ServerKeyResponse{
		ServerName: f.HS.Config.ServerName,
		ValidUntil: time.Now().Add(24 * time.Hour).UnixMilli(),
		VerifyKeys: make(map[string]KeyInfo),
		OldVerifyKeys: make(map[string]KeyInfo),
		Signatures: make(map[string]map[string]string),
	}
	
	// 添加验证密钥
	resp.VerifyKeys[fed.keyID] = KeyInfo{
		Key: base64.RawStdEncoding.EncodeToString(fed.publicKey),
	}
	
	// 签名
	signature := fed.signJSON(resp)
	resp.Signatures = map[string]map[string]string{
		f.HS.Config.ServerName: {
			fed.keyID: signature,
		},
	}
	
	return resp
}

func (f *FederationService) signJSON(data interface{}) string {
	// 规范化 JSON 并签名
	jsonBytes, _ := json.Marshal(data)
	hash := sha256.Sum256(jsonBytes)
	signature := ed25519.Sign(f.privateKey, hash[:])
	return base64.RawStdEncoding.EncodeToString(signature)
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
	Origin    string         `json:"origin"`
	OriginServerTS int64     `json:"origin_server_ts"`
	Event     gin.H          `json:"event"`
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
	
	// 创建加入事件模板
	event := gin.H{
		"type":      "m.room.member",
		"room_id":   roomID,
		"sender":    userID,
		"state_key": userID,
		"content": gin.H{
			"membership": "join",
		},
		"origin":          hs.Config.ServerName,
		"origin_server_ts": time.Now().UnixMilli(),
		"room_version":    room.RoomVersion,
	}
	
	c.JSON(http.StatusOK, MakeJoinResponse{
		Origin:          hs.Config.ServerName,
		OriginServerTS:  time.Now().UnixMilli(),
		Event:           event,
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
	displayName, _ := content["displayname"].(string)
	avatarURL, _ := content["avatar_url"].(string)

	// 更新房间成员
	member := &MatrixRoomMember{
		RoomID:      roomID,
		UserID:      userID,
		Membership:  MembershipJoin,
		DisplayName: &displayName,
		AvatarURL:   &avatarURL,
	}
	db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).Assign(member).FirstOrCreate(member)

	// 返回房间状态
	state, _ := hs.Room.GetRoomStateAll(roomID)
	stateEvents := make([]gin.H, len(state))
	for i, e := range state {
		stateEvents[i] = gin.H{
			"event_id":        e.EventID,
			"type":            e.Type,
			"room_id":         e.RoomID,
			"sender":          e.Sender,
			"state_key":       e.StateKey,
			"content":         e.Content,
			"origin_server_ts": e.OriginServerTS,
		}
	}

	c.JSON(http.StatusOK, SendJoinResponse{
		Origin:    hs.Config.ServerName,
		State:     stateEvents,
		AuthChain: []gin.H{},
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
	
	c.JSON(http.StatusOK, gin.H{
		"origin":     hs.Config.ServerName,
		"pdus":       stateEvents,
		"auth_chain": []gin.H{},
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
	
	c.JSON(http.StatusOK, gin.H{
		"origin":     hs.Config.ServerName,
		"pdu_ids":    stateEventIDs,
		"auth_chain_ids": []string{},
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

func handleFederationSend(c *gin.Context) {
	txnID := c.Param("txnId")
	_ = txnID

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

	// 处理 PDUs（持久化事件）
	for _, pdu := range txn.PDUs {
		var event map[string]interface{}
		if err := json.Unmarshal(pdu, &event); err != nil {
			continue
		}

		// 提取事件信息
		eventID, _ := event["event_id"].(string)
		roomID, _ := event["room_id"].(string)
		eventType, _ := event["type"].(string)
		sender, _ := event["sender"].(string)
		stateKey, _ := event["state_key"].(string)
		content, _ := event["content"].(map[string]interface{})
		originServerTS, _ := event["origin_server_ts"].(float64)

		// 创建事件记录
		matrixEvent := &MatrixEvent{
			EventID:       eventID,
			RoomID:        roomID,
			EventType:     EventType(eventType),
			Sender:        sender,
			StateKey:      stringPtr(stateKey),
			Content:       content,
			OriginServerTS: int64(originServerTS),
		}

		if err := db.GetDB().Create(matrixEvent).Error; err == nil {
			pduResults[eventID] = gin.H{}
		} else {
			pduResults[eventID] = gin.H{"error": err.Error()}
		}

		// 处理状态事件
		if stateKey != "" {
			// 更新房间状态表
			roomState := &MatrixRoomState{
				RoomID:    roomID,
				EventType: EventType(eventType),
				StateKey:  stateKey,
				Content:   content,
				EventID:   eventID,
			}
			db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, eventType, stateKey).
				Assign(roomState).FirstOrCreate(roomState)

			// 处理成员事件
			if eventType == "m.room.member" {
				membership, _ := content["membership"].(string)
				displayName, _ := content["displayname"].(string)
				avatarURL, _ := content["avatar_url"].(string)

				member := &MatrixRoomMember{
					RoomID:      roomID,
					UserID:      stateKey,
					Membership:  MembershipState(membership),
					DisplayName: stringPtr(displayName),
					AvatarURL:   stringPtr(avatarURL),
					EventID:     eventID,
				}
				db.GetDB().Where("room_id = ? AND user_id = ?", roomID, stateKey).
					Assign(member).FirstOrCreate(member)
			}
		}
	}

	// 处理 EDUs（临时事件）
	for _, edu := range txn.EDUs {
		var eduEvent map[string]interface{}
		if err := json.Unmarshal(edu, &eduEvent); err != nil {
			continue
		}

		eduType, _ := eduEvent["type"].(string)
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
		}
	}

	_ = hs

	c.JSON(http.StatusOK, gin.H{
		"pdus": pduResults,
	})
}

// stringPtr 辅助函数
func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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
