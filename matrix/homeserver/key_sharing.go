package homeserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// ===== 端到端密钥共享 (Key Sharing) 实现 =====
// 允许设备之间安全地共享加密密钥

// KeyShareRequest 密钥共享请求
type KeyShareRequest struct {
	RequestID        string `json:"request_id"`
	Requestor        string `json:"requestor"` // 请求者设备 ID
	RequestingDevice string `json:"requesting_device"`
	SessionID        string `json:"session_id"` // Megolm 会话 ID
	RoomID           string `json:"room_id"`
	Algorithm        string `json:"algorithm"`  // m.megolm.v1.aes-sha2
	SenderKey        string `json:"sender_key"` // 发送者的 Curve25519 密钥
}

// KeyShareResponse 密钥共享响应
type KeyShareResponse struct {
	RequestID  string `json:"request_id"`
	SessionID  string `json:"session_id"`
	RoomID     string `json:"room_id"`
	Algorithm  string `json:"algorithm"`
	SenderKey  string `json:"sender_key"`
	SessionKey string `json:"session_key"` // 加密的会话密钥
	ChainIndex int    `json:"chain_index"`
}

// RoomKeyRequest 房间密钥请求（存储在数据库）
type RoomKeyRequest struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement"`
	RequestID        string    `gorm:"index;type:varchar(64);not null"`
	UserID           string    `gorm:"index;type:varchar(255);not null"` // 目标用户
	RequestingUser   string    `gorm:"index;type:varchar(255);not null"` // 请求者用户 ID
	RequestingDevice string    `gorm:"type:varchar(255);not null"`
	SessionID        string    `gorm:"index;type:varchar(255);not null"`
	RoomID           string    `gorm:"index;type:varchar(255);not null"`
	Algorithm        string    `gorm:"type:varchar(64);not null"`
	SenderKey        string    `gorm:"type:varchar(512)"`
	Status           string    `gorm:"type:varchar(16);default:'pending'"` // pending, fulfilled, cancelled
	RequestedAt      time.Time `gorm:"autoCreateTime"`
	RespondedAt      *time.Time
}

func (RoomKeyRequest) TableName() string {
	return "matrix_room_key_requests"
}

// KeySharingService 密钥共享服务
type KeySharingService struct {
	hs *HomeServer
}

// NewKeySharingService 创建密钥共享服务
func NewKeySharingService(hs *HomeServer) *KeySharingService {
	return &KeySharingService{hs: hs}
}

// RequestRoomKey 请求房间密钥
func (kss *KeySharingService) RequestRoomKey(userID, deviceID string, req *KeyShareRequest) error {
	// 验证请求
	if req.SessionID == "" || req.RoomID == "" {
		return fmt.Errorf("missing required fields")
	}

	// 检查是否已存在相同的请求
	var existing RoomKeyRequest
	if err := db.GetDB().Where("request_id = ?", req.RequestID).First(&existing).Error; err == nil {
		// 请求已存在，更新状态
		return nil
	}

	// 创建请求记录
	request := &RoomKeyRequest{
		RequestID:        req.RequestID,
		UserID:           userID,
		RequestingUser:   userID,
		RequestingDevice: deviceID,
		SessionID:        req.SessionID,
		RoomID:           req.RoomID,
		Algorithm:        req.Algorithm,
		SenderKey:        req.SenderKey,
		Status:           "pending",
	}

	if err := db.GetDB().Create(request).Error; err != nil {
		return err
	}

	// 通过 To-Device 消息发送请求给用户的其他设备
	kss.sendKeyRequestToDevices(userID, deviceID, req)

	return nil
}

// sendKeyRequestToDevices 发送密钥请求给用户的其他设备
func (kss *KeySharingService) sendKeyRequestToDevices(userID, requestingDevice string, req *KeyShareRequest) {
	// 获取用户的所有设备
	var devices []MatrixDeviceKey
	db.GetDB().Where("user_id = ? AND device_id != ?", userID, requestingDevice).Find(&devices)

	for _, device := range devices {
		// 创建 To-Device 消息
		content := map[string]interface{}{
			"action":               "request",
			"requesting_device_id": requestingDevice,
			"request_id":           req.RequestID,
			"session_id":           req.SessionID,
			"room_id":              req.RoomID,
			"algorithm":            req.Algorithm,
		}
		if req.SenderKey != "" {
			content["sender_key"] = req.SenderKey
		}

		msg := &MatrixToDeviceMessage{
			Sender:       userID,
			SenderDevice: requestingDevice,
			TargetUser:   userID,
			TargetDevice: device.DeviceID,
			EventType:    "m.room_key_request",
			Content:      content,
		}

		db.GetDB().Create(msg)
	}
}

// RespondToKeyRequest 响应密钥请求
func (kss *KeySharingService) RespondToKeyRequest(userID, deviceID string, resp *KeyShareResponse, forDevice string) error {
	// 查找原始请求
	var request RoomKeyRequest
	if err := db.GetDB().Where("request_id = ? AND status = ?", resp.RequestID, "pending").First(&request).Error; err != nil {
		return fmt.Errorf("request not found or already fulfilled")
	}

	// 验证响应者是否有该密钥
	if !kss.hasSessionKey(userID, deviceID, resp.SessionID, resp.RoomID) {
		return fmt.Errorf("responder does not have the requested key")
	}

	// 更新请求状态
	now := time.Now()
	request.Status = "fulfilled"
	request.RespondedAt = &now
	db.GetDB().Save(&request)

	// 发送密钥给请求设备
	kss.sendKeyResponseToDevice(request.RequestingUser, forDevice, resp)

	return nil
}

// hasSessionKey 检查设备是否拥有会话密钥
func (kss *KeySharingService) hasSessionKey(userID, deviceID, sessionID, roomID string) bool {
	// 检查设备是否有该会话密钥
	// 实际实现应该检查密钥存储
	var count int64
	db.GetDB().Model(&MatrixKeyBackupSession{}).
		Where("room_id = ? AND session_id = ?", roomID, sessionID).
		Count(&count)
	return count > 0
}

// sendKeyResponseToDevice 发送密钥响应给设备
func (kss *KeySharingService) sendKeyResponseToDevice(targetUser, targetDevice string, resp *KeyShareResponse) {
	content := map[string]interface{}{
		"action":      "share",
		"request_id":  resp.RequestID,
		"session_id":  resp.SessionID,
		"room_id":     resp.RoomID,
		"algorithm":   resp.Algorithm,
		"sender_key":  resp.SenderKey,
		"session_key": resp.SessionKey,
		"chain_index": resp.ChainIndex,
	}

	msg := &MatrixToDeviceMessage{
		Sender:       targetUser,
		SenderDevice: "", // 由响应设备填写
		TargetUser:   targetUser,
		TargetDevice: targetDevice,
		EventType:    "m.room_key",
		Content:      content,
	}

	db.GetDB().Create(msg)

	// 触发同步通知
	kss.hs.NotifyToDeviceUpdate(targetUser, targetDevice)
}

// CancelKeyRequest 取消密钥请求
func (kss *KeySharingService) CancelKeyRequest(userID, requestID string) error {
	return db.GetDB().Model(&RoomKeyRequest{}).
		Where("request_id = ? AND requesting_user = ?", requestID, userID).
		Update("status", "cancelled").Error
}

// GetPendingKeyRequests 获取待处理的密钥请求
func (kss *KeySharingService) GetPendingKeyRequests(userID string) ([]RoomKeyRequest, error) {
	var requests []RoomKeyRequest
	if err := db.GetDB().Where("user_id = ? AND status = ?", userID, "pending").
		Order("requested_at DESC").
		Find(&requests).Error; err != nil {
		return nil, err
	}
	return requests, nil
}

// HandleIncomingKeyRequest 处理收到的密钥请求（来自其他设备）
func (kss *KeySharingService) HandleIncomingKeyRequest(userID, deviceID string, content map[string]interface{}) {
	action, _ := content["action"].(string)
	if action != "request" {
		return
	}

	requestID, _ := content["request_id"].(string)
	sessionID, _ := content["session_id"].(string)
	roomID, _ := content["room_id"].(string)
	algorithm, _ := content["algorithm"].(string)
	senderKey, _ := content["sender_key"].(string)
	requestingDevice, _ := content["requesting_device_id"].(string)

	// 检查是否有该密钥
	if !kss.hasSessionKey(userID, deviceID, sessionID, roomID) {
		// 没有密钥，忽略请求
		return
	}

	// 创建请求记录
	request := &RoomKeyRequest{
		RequestID:        requestID,
		UserID:           userID,
		RequestingUser:   userID,
		RequestingDevice: requestingDevice,
		SessionID:        sessionID,
		RoomID:           roomID,
		Algorithm:        algorithm,
		SenderKey:        senderKey,
		Status:           "pending",
	}

	// 使用 Upsert
	if err := db.GetDB().Where("request_id = ?", requestID).First(&RoomKeyRequest{}).Error; err != nil {
		db.GetDB().Create(request)
		log.Info("Received key request from %s for session %s", requestingDevice, sessionID)
	}
}

// HandleIncomingKeyShare 处理收到的密钥共享
func (kss *KeySharingService) HandleIncomingKeyShare(userID, deviceID string, content map[string]interface{}) {
	action, _ := content["action"].(string)
	if action != "share" {
		return
	}

	requestID, _ := content["request_id"].(string)
	sessionID, _ := content["session_id"].(string)
	roomID, _ := content["room_id"].(string)
	sessionKey, _ := content["session_key"].(string)

	// 更新请求状态
	db.GetDB().Model(&RoomKeyRequest{}).
		Where("request_id = ? AND requesting_user = ?", requestID, userID).
		Updates(map[string]interface{}{
			"status": "fulfilled",
		})

	// 存储收到的密钥
	if sessionKey != "" {
		kss.storeReceivedKey(userID, deviceID, roomID, sessionID, sessionKey, content)
	}

	log.Info("Received key share for session %s", sessionID)
}

// storeReceivedKey 存储收到的密钥
func (kss *KeySharingService) storeReceivedKey(userID, deviceID, roomID, sessionID, sessionKey string, content map[string]interface{}) {
	// 创建密钥备份条目
	sessionData := map[string]interface{}{
		"algorithm":   content["algorithm"],
		"sender_key":  content["sender_key"],
		"session_key": sessionKey,
		"chain_index": content["chain_index"],
	}

	dataJSON, err := json.Marshal(sessionData)
	if err != nil {
		log.Error("Failed to marshal session data: %v", err)
		return
	}

	session := &MatrixKeyBackupSession{
		UserID:      userID,
		RoomID:      roomID,
		SessionID:   sessionID,
		SessionData: string(dataJSON),
	}

	// 使用 Upsert
	if err := db.GetDB().Where("user_id = ? AND room_id = ? AND session_id = ?", userID, roomID, sessionID).
		Assign(session).
		FirstOrCreate(&MatrixKeyBackupSession{}).Error; err != nil {
		log.Error("Failed to store received key: %v", err)
	}
}

// ===== HTTP Handlers =====

// RegisterKeySharingRoutes 注册密钥共享路由
func RegisterKeySharingRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	keys := r.Group("/_matrix/client/v3/room_keys")
	keys.Use(MatrixAuthMiddleware())
	{
		// 密钥备份版本
		keys.GET("/version", handleGetKeyBackupVersion)
		keys.POST("/version", handleCreateKeyBackupVersion)
		keys.PUT("/version/:version", handleUpdateKeyBackupVersion)
		keys.DELETE("/version/:version", handleDeleteKeyBackupVersion)

		// 房间密钥
		keys.GET("/keys/:roomId", handleGetRoomKeys)
		keys.GET("/keys/:roomId/:sessionId", handleGetSessionKey)
		keys.PUT("/keys/:roomId/:sessionId", handleStoreSessionKey)
		keys.DELETE("/keys/:roomId/:sessionId", handleDeleteSessionKey)

		// 批量操作
		keys.PUT("/keys", handleStoreRoomKeys)
	}
}

// handleGetKeyBackupVersion 获取密钥备份版本
func handleGetKeyBackupVersion(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	version, err := hs.KeyBackup.GetBackupVersion(userID, "")
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "No backup found",
		})
		return
	}

	c.JSON(http.StatusOK, version)
}

// handleCreateKeyBackupVersion 创建密钥备份版本
func handleCreateKeyBackupVersion(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	var req struct {
		Algorithm string                 `json:"algorithm"`
		AuthData  map[string]interface{} `json:"auth_data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	version, err := hs.KeyBackup.CreateBackupVersion(userID, req.AuthData)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, version)
}

// handleUpdateKeyBackupVersion 更新密钥备份版本
func handleUpdateKeyBackupVersion(c *gin.Context) {
	// 简化实现
	c.JSON(http.StatusOK, gin.H{})
}

// handleDeleteKeyBackupVersion 删除密钥备份版本
func handleDeleteKeyBackupVersion(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	version := c.Param("version")

	if err := hs.KeyBackup.DeleteBackupVersion(userID, version); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleGetRoomKeys 获取房间的所有密钥
func handleGetRoomKeys(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	roomID := c.Param("roomId")
	version := c.Query("version")

	sessions, err := hs.KeyBackup.GetBackupKeysForRoom(userID, version, roomID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"sessions": sessions,
	})
}

// handleGetSessionKey 获取特定会话密钥
func handleGetSessionKey(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	roomID := c.Param("roomId")
	sessionID := c.Param("sessionId")
	version := c.Query("version")

	session, err := hs.KeyBackup.GetBackupKeys(userID, version, roomID, sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Session not found",
		})
		return
	}

	c.JSON(http.StatusOK, session)
}

// handleStoreSessionKey 存储会话密钥
func handleStoreSessionKey(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	roomID := c.Param("roomId")
	sessionID := c.Param("sessionId")
	version := c.Query("version")

	var session KeyBackupSession
	if err := c.ShouldBindJSON(&session); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	session.RoomID = roomID
	session.SessionID = sessionID

	sessions := map[string]map[string]KeyBackupSession{
		roomID: {
			sessionID: session,
		},
	}

	if err := hs.KeyBackup.StoreBackupKeys(userID, version, sessions); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	// Return updated backup info
	var backup MatrixKeyBackup
	if err := db.GetDB().Where("user_id = ? AND version = ?", userID, version).First(&backup).Error; err == nil {
		c.JSON(http.StatusOK, gin.H{
			"etag":  backup.ETag,
			"count": backup.Count,
		})
	} else {
		c.JSON(http.StatusOK, gin.H{})
	}
}

// handleDeleteSessionKey 删除会话密钥
func handleDeleteSessionKey(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	roomID := c.Param("roomId")
	sessionID := c.Param("sessionId")
	version := c.Query("version")

	if err := hs.KeyBackup.DeleteBackupKey(userID, version, roomID, sessionID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleStoreRoomKeys 批量存储房间密钥
func handleStoreRoomKeys(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	version := c.Query("version")

	// Matrix spec: body is {"rooms": {roomId: {sessions: {sessionId: KeyBackupSession}}}}
	var body struct {
		Rooms map[string]struct {
			Sessions map[string]KeyBackupSession `json:"sessions"`
		} `json:"rooms"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	// Flatten to map[roomID]map[sessionID]KeyBackupSession
	rooms := make(map[string]map[string]KeyBackupSession)
	for roomID, roomData := range body.Rooms {
		rooms[roomID] = roomData.Sessions
	}

	if err := hs.KeyBackup.StoreBackupKeys(userID, version, rooms); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	// Return updated backup info
	var backup MatrixKeyBackup
	if err := db.GetDB().Where("user_id = ? AND version = ?", userID, version).First(&backup).Error; err == nil {
		c.JSON(http.StatusOK, gin.H{
			"etag":  backup.ETag,
			"count": backup.Count,
		})
	} else {
		c.JSON(http.StatusOK, gin.H{})
	}
}
