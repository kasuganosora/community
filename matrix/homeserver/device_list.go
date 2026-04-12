package homeserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// ===== 设备列表联邦推送 (Device List Federation) 实现 =====
// MSC2229: 设备列表更新联邦同步

// DeviceListUpdate 设备列表更新
type DeviceListUpdate struct {
	UserID     string `json:"user_id"`
	DeviceID   string `json:"device_id"`
	StreamID   int64  `json:"stream_id"`   // 单调递增的流 ID
	Changed    bool   `json:"changed"`     // true = 设备变更, false = 设备移除
	UpdateTime int64  `json:"update_time"` // 更新时间戳
}

// DeviceListChange 设备列表变更记录
type DeviceListChange struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement"`
	UserID     string `gorm:"index;type:varchar(255);not null"`
	DeviceID   string `gorm:"index;type:varchar(255);not null"`
	StreamID   int64  `gorm:"index;not null"`
	ChangeType string `gorm:"type:varchar(16);not null"` // added, removed, changed
	ChangedAt  int64  `gorm:"not null"`
	SyncedTo   string `gorm:"type:text"` // 已同步的服务器列表 JSON
}

func (DeviceListChange) TableName() string {
	return "matrix_device_list_changes"
}

// DeviceListStream 设备列表流位置
type DeviceListStream struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	UserID    string `gorm:"uniqueIndex;type:varchar(255);not null"`
	StreamID  int64  `gorm:"not null"`
	UpdatedAt int64  `gorm:"not null"`
}

func (DeviceListStream) TableName() string {
	return "matrix_device_list_streams"
}

// DeviceListUpdateEDU 设备列表更新 EDU
type DeviceListUpdateEDU struct {
	UserID   string   `json:"user_id"`
	DeviceID string   `json:"device_id,omitempty"`
	StreamID int64    `json:"stream_id"`
	PrevID   []int64  `json:"prev_id,omitempty"`
	Changed  []string `json:"changed,omitempty"` // 变更的设备列表
	Removed  []string `json:"removed,omitempty"` // 移除的设备列表
}

// DeviceListService 设备列表服务
type DeviceListService struct {
	hs *HomeServer
}

// NewDeviceListService 创建设备列表服务
func NewDeviceListService(hs *HomeServer) *DeviceListService {
	return &DeviceListService{hs: hs}
}

// RecordDeviceChange 记录设备变更
func (dls *DeviceListService) RecordDeviceChange(userID, deviceID, changeType string) error {
	// 获取或创建流位置
	streamID, err := dls.getNextStreamID(userID)
	if err != nil {
		return err
	}

	change := &DeviceListChange{
		UserID:     userID,
		DeviceID:   deviceID,
		StreamID:   streamID,
		ChangeType: changeType,
		ChangedAt:  time.Now().UnixMilli(),
		SyncedTo:   "[]",
	}

	if err := db.GetDB().Create(change).Error; err != nil {
		return err
	}

	// 触发联邦推送
	go dls.federateDeviceListUpdate(userID, deviceID, streamID, changeType)

	return nil
}

// getNextStreamID 获取下一个流 ID
func (dls *DeviceListService) getNextStreamID(userID string) (int64, error) {
	var stream DeviceListStream
	result := db.GetDB().Where("user_id = ?", userID).First(&stream)

	if result.Error != nil {
		// 创建新的流记录
		stream = DeviceListStream{
			UserID:    userID,
			StreamID:  1,
			UpdatedAt: time.Now().UnixMilli(),
		}
		if err := db.GetDB().Create(&stream).Error; err != nil {
			return 0, err
		}
		return 1, nil
	}

	// 递增流 ID
	stream.StreamID++
	stream.UpdatedAt = time.Now().UnixMilli()
	if err := db.GetDB().Save(&stream).Error; err != nil {
		return 0, err
	}

	return stream.StreamID, nil
}

// GetCurrentStreamID 获取当前流 ID
func (dls *DeviceListService) GetCurrentStreamID(userID string) (int64, error) {
	var stream DeviceListStream
	if err := db.GetDB().Where("user_id = ?", userID).First(&stream).Error; err != nil {
		return 0, err
	}
	return stream.StreamID, nil
}

// GetDeviceListChanges 获取设备列表变更
func (dls *DeviceListService) GetDeviceListChanges(userID string, fromStreamID int64) ([]DeviceListChange, error) {
	var changes []DeviceListChange
	if err := db.GetDB().Where("user_id = ? AND stream_id > ?", userID, fromStreamID).
		Order("stream_id ASC").
		Find(&changes).Error; err != nil {
		return nil, err
	}
	return changes, nil
}

// federateDeviceListUpdate 联邦推送设备列表更新
func (dls *DeviceListService) federateDeviceListUpdate(userID, deviceID string, streamID int64, changeType string) {
	// 获取用户所在的共享房间
	rooms, err := dls.getUserSharedRooms(userID)
	if err != nil {
		log.Error("Failed to get user shared rooms: %v", err)
		return
	}

	// 收集需要通知的服务器
	servers := make(map[string]bool)
	for _, roomID := range rooms {
		roomServers, err := dls.getRoomServers(roomID)
		if err != nil {
			continue
		}
		for _, server := range roomServers {
			if server != dls.hs.Config.ServerName {
				servers[server] = true
			}
		}
	}

	// 构建 EDU
	edu := &DeviceListUpdateEDU{
		UserID:   userID,
		DeviceID: deviceID,
		StreamID: streamID,
	}

	if changeType == "removed" {
		edu.Removed = []string{deviceID}
	} else {
		edu.Changed = []string{deviceID}
	}

	// 发送到所有相关服务器
	for server := range servers {
		go dls.sendDeviceListUpdateToServer(server, edu)
	}
}

// sendDeviceListUpdateToServer 发送设备列表更新到远程服务器
func (dls *DeviceListService) sendDeviceListUpdateToServer(server string, edu *DeviceListUpdateEDU) {
	fedClient := NewFederationClient(dls.hs)

	txnID := fmt.Sprintf("dl-%d", time.Now().UnixNano())

	eduBytes, err := json.Marshal(map[string]interface{}{
		"edu_type": "m.device_list_update",
		"content":  edu,
	})
	if err != nil {
		log.Error("Failed to marshal device list EDU: %v", err)
		return
	}

	if err := fedClient.SendTransaction(server, txnID, nil, []json.RawMessage{eduBytes}); err != nil {
		log.Error("Failed to send device list update to %s: %v", server, err)
		return
	}

	// 记录同步状态
	dls.recordSyncStatus(edu.UserID, edu.StreamID, server)
}

// recordSyncStatus 记录同步状态
func (dls *DeviceListService) recordSyncStatus(userID string, streamID int64, server string) {
	var change DeviceListChange
	if err := db.GetDB().Where("user_id = ? AND stream_id = ?", userID, streamID).First(&change).Error; err != nil {
		return
	}

	var syncedTo []string
	_ = json.Unmarshal([]byte(change.SyncedTo), &syncedTo) //nolint:errcheck // best-effort parsing

	// 检查是否已记录
	found := false
	for _, s := range syncedTo {
		if s == server {
			found = true
			break
		}
	}

	if !found {
		syncedTo = append(syncedTo, server)
		syncedToJSON, _ := json.Marshal(syncedTo)
		db.GetDB().Model(&change).Update("synced_to", string(syncedToJSON))
	}
}

// getUserSharedRooms 获取用户共享的房间
func (dls *DeviceListService) getUserSharedRooms(userID string) ([]string, error) {
	var members []MatrixRoomMember
	if err := db.GetDB().Where("user_id = ? AND membership = ?", userID, MembershipJoin).Find(&members).Error; err != nil {
		return nil, err
	}

	rooms := make([]string, len(members))
	for i, m := range members {
		rooms[i] = m.RoomID
	}
	return rooms, nil
}

// getRoomServers 获取房间涉及的联邦服务器
func (dls *DeviceListService) getRoomServers(roomID string) ([]string, error) {
	// 获取房间成员
	var members []MatrixRoomMember
	if err := db.GetDB().Where("room_id = ? AND membership = ?", roomID, MembershipJoin).Find(&members).Error; err != nil {
		return nil, err
	}

	// 收集服务器
	servers := make(map[string]bool)
	for _, m := range members {
		// 从用户 ID 提取服务器名
		parts := parseUserID(m.UserID)
		if parts != "" && parts != dls.hs.Config.ServerName {
			servers[parts] = true
		}
	}

	result := make([]string, 0, len(servers))
	for s := range servers {
		result = append(result, s)
	}
	return result, nil
}

// HandleIncomingDeviceListUpdate 处理收到的设备列表更新
func (dls *DeviceListService) HandleIncomingDeviceListUpdate(origin string, edu *DeviceListUpdateEDU) error {
	// 验证来源服务器
	if origin == "" {
		return fmt.Errorf("missing origin")
	}

	// 更新设备信息
	if len(edu.Changed) > 0 {
		for _, deviceID := range edu.Changed {
			dls.updateRemoteDevice(edu.UserID, deviceID, origin)
		}
	}

	if len(edu.Removed) > 0 {
		for _, deviceID := range edu.Removed {
			dls.removeRemoteDevice(edu.UserID, deviceID)
		}
	}

	// Record the change so getDeviceListUpdates can surface it in sync
	changeType := "changed"
	if len(edu.Removed) > 0 && len(edu.Changed) == 0 {
		changeType = "removed"
	}
	deviceID := edu.DeviceID
	if deviceID == "" && len(edu.Changed) > 0 {
		deviceID = edu.Changed[0]
	}
	if deviceID == "" && len(edu.Removed) > 0 {
		deviceID = edu.Removed[0]
	}
	change := &DeviceListChange{
		UserID:     edu.UserID,
		DeviceID:   deviceID,
		StreamID:   edu.StreamID,
		ChangeType: changeType,
		ChangedAt:  time.Now().UnixMilli(),
		SyncedTo:   "[]",
	}
	if err := db.GetDB().Create(change).Error; err != nil {
		return fmt.Errorf("failed to create device list change: %w", err)
	}

	log.Info("Processed device list update for %s from %s", edu.UserID, origin)
	return nil
}

// updateRemoteDevice 更新远程设备信息
func (dls *DeviceListService) updateRemoteDevice(userID, deviceID, origin string) {
	// 标记需要重新获取设备密钥
	// 实际实现应该查询远程服务器获取最新的设备密钥
	var device MatrixDeviceKey
	result := db.GetDB().Where("user_id = ? AND device_id = ?", userID, deviceID).First(&device)

	if result.Error != nil {
		// 新设备，需要查询
		go dls.queryRemoteDeviceKeys(userID, []string{deviceID}, origin)
	}
}

// removeRemoteDevice 移除远程设备信息
func (dls *DeviceListService) removeRemoteDevice(userID, deviceID string) {
	db.GetDB().Where("user_id = ? AND device_id = ?", userID, deviceID).Delete(&MatrixDeviceKey{})
}

// queryRemoteDeviceKeys 查询远程设备密钥
func (dls *DeviceListService) queryRemoteDeviceKeys(userID string, deviceIDs []string, server string) {
	// 使用联邦客户端查询
	// 简化实现
	log.Info("Querying device keys for %s from %s", userID, server)
}

// GetUserDevices 获取用户的所有设备（联邦 API 响应）
func (dls *DeviceListService) GetUserDevices(userID string) (*UserDevicesResponseDL, error) {
	// 检查是否为本服务器用户
	if !isLocalUser(userID, dls.hs.Config.ServerName) {
		return nil, fmt.Errorf("user not found")
	}

	// 获取设备列表
	var devices []MatrixDeviceKey
	if err := db.GetDB().Where("user_id = ?", userID).Find(&devices).Error; err != nil {
		return nil, err
	}

	deviceList := make([]DeviceInfoDL, len(devices))
	for i, d := range devices {
		var dk DeviceKeys
		_ = json.Unmarshal([]byte(d.DeviceKey), &dk) //nolint:errcheck // best-effort parsing

		deviceList[i] = DeviceInfoDL{
			DeviceID:   d.DeviceID,
			Keys:       dk.Keys,
			Algorithms: dk.Algorithms,
		}
	}

	streamID, _ := dls.GetCurrentStreamID(userID)

	return &UserDevicesResponseDL{
		UserID:   userID,
		StreamID: streamID,
		Devices:  deviceList,
	}, nil
}

// UserDevicesResponseDL 用户设备响应（设备列表服务版本）
type UserDevicesResponseDL struct {
	UserID   string         `json:"user_id"`
	StreamID int64          `json:"stream_id"`
	Devices  []DeviceInfoDL `json:"devices"`
}

// DeviceInfoDL 设备信息（设备列表服务版本）
type DeviceInfoDL struct {
	DeviceID    string            `json:"device_id"`
	Keys        map[string]string `json:"keys"`
	Algorithms  []string          `json:"algorithms"`
	DisplayName string            `json:"display_name,omitempty"`
}

// ===== HTTP Handlers =====

// RegisterDeviceListRoutes 注册设备列表联邦路由
func RegisterDeviceListRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// 联邦设备列表端点
	fed := r.Group("/_matrix/federation/v1/user")
	fed.Use(FederationAuthMiddleware())
	{
		fed.GET("/devices/:userId", handleFederationGetUserDevices)
	}
}

// handleFederationGetUserDevices 处理联邦用户设备查询
func handleFederationGetUserDevices(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	resp, err := hs.DeviceList.GetUserDevices(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   err.Error(),
		})
		return
	}

	// Convert to federation response format
	devices := make([]DeviceKeyInfo, 0, len(resp.Devices))
	for _, d := range resp.Devices {
		devices = append(devices, DeviceKeyInfo{
			DeviceID:    d.DeviceID,
			Keys:        d.Keys,
			Algorithms:  d.Algorithms,
			DisplayName: d.DisplayName,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id":   resp.UserID,
		"stream_id": resp.StreamID,
		"devices":   devices,
	})
}

// 辅助函数
func parseUserID(userID string) string {
	parts := strings.Split(userID, ":")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

func isLocalUser(userID, serverName string) bool {
	parts := strings.Split(userID, ":")
	return len(parts) == 2 && parts[1] == serverName
}
