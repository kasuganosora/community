package homeserver

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// FederationService 联邦服务.
type FederationService struct {
	HS         *HomeServer
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	keyID      string
}

// NewFederationService 创建联邦服务.
func NewFederationService(hs *HomeServer) *FederationService {
	// 使用 HomeServer 中缓存的密钥
	return &FederationService{
		HS:         hs,
		privateKey: hs.privateKey,
		publicKey:  hs.publicKey,
		keyID:      hs.keyID,
	}
}

// RegisterFederationRoutes 注册联邦 API 路由.
func RegisterFederationRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	fed := r.Group("/_matrix/federation")
	{
		fed.GET("/v1/version", handleFederationVersion)
		fed.Use(requireFederationAuth())
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
	RawJSON       json.RawMessage              `json:"-"`
	ServerName    string                       `json:"server_name"`
	ValidUntil    int64                        `json:"valid_until_ts"`
	VerifyKeys    map[string]KeyInfo           `json:"verify_keys"`
	OldVerifyKeys map[string]KeyInfo           `json:"old_verify_keys"`
	Signatures    map[string]map[string]string `json:"signatures"`
}

type KeyInfo struct {
	Key       string `json:"key"`
	ExpiredTS int64  `json:"expired_ts,omitempty"`
}

func (f *FederationService) GetServerKey() map[string]any {
	fed := NewFederationService(f.HS)

	resp := map[string]any{
		"server_name":    f.HS.Config.ServerName,
		"valid_until_ts": time.Now().Add(24 * time.Hour).UnixMilli(),
		"verify_keys": map[string]any{
			fed.keyID: map[string]any{
				"key": base64.RawStdEncoding.EncodeToString(fed.publicKey),
			},
		},
		"old_verify_keys": map[string]any{},
	}

	sig := fed.signJSON(resp)
	resp["signatures"] = map[string]any{
		f.HS.Config.ServerName: map[string]any{
			fed.keyID: sig,
		},
	}

	return resp
}

func (f *FederationService) signJSON(data any) string {
	// 按照 Matrix 规范：1) 转为 map 2) 移除 signatures/unsigned 3) canonical JSON 4) 直接签名
	var objMap map[string]any

	switch v := data.(type) {
	case map[string]any:
		objMap = make(map[string]any)
		maps.Copy(objMap, v)
	default:
		jsonBytes, _ := json.Marshal(data) //nolint:errchkjson // any type, checked by Unmarshal below
		err := json.Unmarshal(jsonBytes, &objMap)
		if err != nil {
			return ""
		}
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

// signEvent 签名事件并将签名嵌入事件.
func (f *FederationService) signEvent(event map[string]any) map[string]any {
	sig := f.signJSON(event)

	sigs, _ := event["signatures"].(map[string]any)
	if sigs == nil {
		sigs = make(map[string]any)
	}

	sigs[f.HS.Config.ServerName] = map[string]any{
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
			"name":    "Community Matrix Homeserver", //nolint:goconst
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
	var (
		roomID  string
		servers []string
	)

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
			"errcode": "M_NOT_FOUND",          //nolint:goconst
			"error":   "Room alias not found", //nolint:goconst
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
			"errcode": "M_INVALID_PARAM", //nolint:goconst
			"error":   "Invalid user ID",
		})

		return
	}

	// 检查是否是本地用户
	if domain != hs.Config.ServerName {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "User not found", //nolint:goconst
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

// ===== 离开房间 =====

// buildAuthChainForRoom 根据房间状态事件构建 auth chain.
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

func handleGetState(c *gin.Context)    { handleVerifiedState(c, false) }
func handleGetStateIDs(c *gin.Context) { handleVerifiedState(c, true) }
func handleVerifiedState(c *gin.Context, ids bool) {
	roomID := c.Param("roomId")
	if !requireFederationRoomAccess(c, roomID) {
		return
	}
	version, err := federationRoomVersion(roomID)
	if err != nil {
		federationFailure(c, 404, err)
		return
	}
	eventID := c.Query("event_id")
	if eventID == "" {
		federationFailure(c, 400, fmt.Errorf("event_id is required"))
		return
	}
	event, err := eventPDUFromDB(db.GetDB(), eventID, version)
	if err != nil || event.RoomID().String() != roomID {
		federationFailure(c, 404, fmt.Errorf("event not found in requested room"))
		return
	}
	state, err := stateBeforePDU(db.GetDB(), event)
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	auth, err := completeAuthChain(db.GetDB(), state, version)
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	if ids {
		stateIDs, authIDs := []string{}, []string{}
		for _, p := range state {
			stateIDs = append(stateIDs, p.EventID())
		}
		for _, p := range auth {
			authIDs = append(authIDs, p.EventID())
		}
		c.JSON(200, gin.H{"pdu_ids": stateIDs, "auth_chain_ids": authIDs})
		return
	}
	stateWire, authWire := []map[string]any{}, []map[string]any{}
	for _, p := range state {
		w, _ := wirePDU(p)
		stateWire = append(stateWire, w)
	}
	for _, p := range auth {
		w, _ := wirePDU(p)
		authWire = append(authWire, w)
	}
	c.JSON(200, gin.H{"pdus": stateWire, "auth_chain": authWire})
}
func handleGetFederationEvent(c *gin.Context) {
	var event MatrixEvent
	if err := db.GetDB().Where("event_id = ?", c.Param("eventId")).First(&event).Error; err != nil {
		federationFailure(c, 404, err)
		return
	}
	if !requireFederationRoomAccess(c, event.RoomID) {
		return
	}
	wire, err := matrixEventToPDU(&event)
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	c.JSON(200, gin.H{"origin": GetHomeServer().Config.ServerName, "origin_server_ts": time.Now().UnixMilli(), "pdus": []any{wire}})
}

// ===== 事务发送 =====

type FederationTransaction struct {
	Origin         string            `json:"origin"`
	OriginServerTS int64             `json:"origin_server_ts"`
	PDUs           []json.RawMessage `json:"pdus"`
	EDUs           []json.RawMessage `json:"edus"`
}

// computeReferenceHash computes the event ID for room version 4+ per the Matrix spec:
// 1. Redact the event (keep only allowed top-level keys and type-specific content keys)
// 2. Remove signatures, unsigned, and _-prefixed keys
// 3. Canonical JSON → SHA-256 → "$" + base64url(unpadded).
func computeReferenceHash(event map[string]any, eventType string) string {
	// Room v4+ reference hash. event_id is the hash itself, so it is not an input.
	// m.room.message body/msgtype do not survive redaction.
	allowedKeys := map[string]bool{
		"type": true, "room_id": true, "sender": true,
		"state_key": true, "content": true, "hashes": true, "signatures": true,
		"depth": true, "prev_events": true, "auth_events": true,
		"origin_server_ts": true,
	}

	protectedContent := protectedContentKeys()

	redacted := make(map[string]any)

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
	if origContent, ok := event["content"].(map[string]any); ok {
		newContent := make(map[string]any)

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
		return ""
	}

	hash := sha256.Sum256(canonicalBytes)

	return "$" + base64.RawURLEncoding.EncodeToString(hash[:])
}

// protectedContentKeys are the content keys kept by the room v10 redaction algorithm.
func protectedContentKeys() map[string][]string {
	return map[string][]string{
		"m.room.member":             {"membership", "join_authorised_via_users_server"},
		"m.room.create":             {"creator"},
		"m.room.join_rules":         {"join_rule", "allow"},
		"m.room.power_levels":       {"ban", "events", "events_default", "invite", "kick", "redact", "state_default", "users", "users_default"},
		"m.room.history_visibility": {"history_visibility"},
		"m.room.redaction":          {"redacts"},
	}
}

// redactContent keeps only the keys that survive redaction for this event type.
func redactContent(eventType string, content map[string]any) map[string]any {
	out := map[string]any{}

	allowed := map[string]bool{}
	for _, key := range protectedContentKeys()[eventType] {
		allowed[key] = true
	}

	for key, val := range content {
		if allowed[key] {
			out[key] = val
		}
	}

	return out
}

// parseFedEventIDs reads prev_events/auth_events. Room v3+ sends strings.
// Room v1/v2 sends [event_id, hash] pairs.
func parseFedEventIDs(raw any) []string {
	switch list := raw.(type) {
	case []string:
		return list
	case []any:
		ids := make([]string, 0, len(list))
		for _, item := range list {
			switch v := item.(type) {
			case string:
				if v != "" {
					ids = append(ids, v)
				}
			case []any:
				if len(v) > 0 {
					if s, ok := v[0].(string); ok && s != "" {
						ids = append(ids, s)
					}
				}
			}
		}

		return ids
	default:
		return nil
	}
}

// parseFederationPDU extracts fields from a raw PDU map and computes event_id for room v4+.
func parseFederationPDU(event map[string]any) (eventID, roomID, eventType, sender string, stateKey string, hasStateKey bool, content map[string]any, originServerTS float64) {
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

	content, _ = event["content"].(map[string]any)
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
func storeFederationPDU(hs *HomeServer, event map[string]any) (string, error) {
	roomID, _ := event["room_id"].(string)
	version, err := federationRoomVersion(roomID)
	if err != nil {
		return "", err
	}
	p, err := decodeWirePDU(version, event)
	if err != nil {
		return "", err
	}
	if err = validateIncomingPDU(context.Background(), hs, p, true); err != nil {
		return p.EventID(), err
	}
	err = db.GetDB().Transaction(func(tx *gorm.DB) error { return persistVerifiedPDU(tx, hs, p, true) })
	return p.EventID(), err
}

func fetchMissingEvents(hs *HomeServer, event map[string]any, origin string) {
	roomID, _ := event["room_id"].(string)
	if roomID == "" || origin == "" || origin == hs.Config.ServerName {
		return
	}

	prevEventIDs := parseFedEventIDs(event["prev_events"])

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
		var missingEvent map[string]any
		err := json.Unmarshal(rawPDU, &missingEvent)
		if err != nil {
			continue
		}

		if _, err := storeFederationPDU(hs, missingEvent); err != nil {
			continue
		}
	}
}

func handleFederationSend(c *gin.Context) {
	var txn FederationTransaction
	err := c.ShouldBindJSON(&txn)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Bad JSON",
		})

		return
	}

	hs := GetHomeServer()
	if txn.Origin != c.GetString("origin") {
		federationFailure(c, 403, fmt.Errorf("transaction origin mismatch"))
		return
	}
	pduResults := make(map[string]any)
	notifiedRooms := make(map[string]bool)

	// Process PDUs
	for _, pdu := range txn.PDUs {
		var event map[string]any
		if err := json.Unmarshal(pdu, &event); err != nil {
			federationFailure(c, 400, err)
			return
		}

		// Signature and auth checks precede persistence; unknown auth chains are rejected.

		eventID, err := storeFederationPDU(hs, event)
		if err != nil {
			if eventID != "" {
				pduResults[eventID] = gin.H{"error": err.Error()}
			} else {
				federationFailure(c, 400, err)
				return
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
		var eduEvent map[string]any
		err := json.Unmarshal(edu, &eduEvent)
		if err != nil {
			continue
		}

		eduType, _ := eduEvent["edu_type"].(string)
		content, _ := eduEvent["content"].(map[string]any)

		switch eduType {
		case "m.direct_to_device":
			var direct directToDeviceEDU
			raw, err := json.Marshal(content)
			if err != nil {
				federationFailure(c, 400, err)
				return
			}
			if err = json.Unmarshal(raw, &direct); err != nil {
				federationFailure(c, 400, err)
				return
			}
			if err = deliverLocalToDevice(hs, txn.Origin, direct); err != nil {
				federationFailure(c, 400, err)
				return
			}

		case "m.typing":
			// 处理 typing 事件
			roomID, _ := content["room_id"].(string)

			userIDs, _ := content["user_ids"].([]any)
			for _, uid := range userIDs {
				userID, _ := uid.(string)
				if extractServerName(userID) != txn.Origin || !federationUserJoined(roomID, userID) {
					continue
				}
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
			if extractServerName(userID) == txn.Origin && presence != "" {
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
				roomMap, ok := roomData.(map[string]any)
				if !ok {
					continue
				}

				for eventID, eventData := range roomMap {
					eventMap, ok := eventData.(map[string]any)
					if !ok {
						continue
					}

					for receiptType, typeData := range eventMap {
						typeMap, ok := typeData.(map[string]any)
						if !ok {
							continue
						}

						for fedUserID, userData := range typeMap {
							if extractServerName(fedUserID) != txn.Origin || !federationUserJoined(roomID, fedUserID) {
								continue
							}
							userMap, ok := userData.(map[string]any)
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

			eduBytes, mErr := json.Marshal(content)
			if mErr != nil {
				log.Warn("Failed to marshal EDU content: %v", mErr)
				continue
			}
			if json.Unmarshal(eduBytes, &edu) == nil {
				origin := txn.Origin
				if extractServerName(edu.UserID) != origin {
					continue
				}

				_ = hs.DeviceList.HandleIncomingDeviceListUpdate(origin, &edu)
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
		_, _ = fmt.Sscanf(l, "%d", &limit)
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
			"room_id":        room.RoomID,
			"name":           name,
			"topic":          room.Topic,
			"avatar_url":     avatarURL,
			"joined_members": joinedCount,
			"world_readable": room.HistoryVisibility == "world_readable", //nolint:goconst
			"guest_can_join": room.GuestAccess == "can_join",
			"join_rule":      room.JoinRule,
			"room_type":      room.RoomType,
		}
	}

	// 生成下一页 token
	nextBatch := ""
	if len(rooms) == limit {
		nextBatch = fmt.Sprintf("s%d", time.Now().UnixMilli())
	}

	c.JSON(http.StatusOK, gin.H{
		"origin":                    hs.Config.ServerName,
		"chunk":                     chunk,
		"total_room_count_estimate": len(rooms),
		"next_batch":                nextBatch,
		"prev_batch":                since,
	})

	_ = since
}

// ===== 用户设备 =====

type UserDevicesResponse struct {
	UserID         string          `json:"user_id"`
	Devices        []DeviceKeyInfo `json:"devices"`
	MasterKey      any             `json:"master_key,omitempty"`
	SelfSigningKey any             `json:"self_signing_key,omitempty"`
}

type DeviceKeyInfo struct {
	DeviceID    string                       `json:"device_id"`
	Keys        map[string]string            `json:"keys"`
	Algorithms  []string                     `json:"algorithms"`
	Signatures  map[string]map[string]string `json:"signatures"`
	DisplayName string                       `json:"device_display_name,omitempty"`
}

// ===== 第三方网络 =====

func handleThirdPartyOnBind(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}

// ===== 辅助函数 =====

// ParseMatrixUserID 解析 Matrix 用户 ID
// 注意：这个函数在 auth.go 中已定义，这里为了避免重复，使用外部定义的版本
