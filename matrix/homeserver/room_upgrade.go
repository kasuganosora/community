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

// ===== 房间升级 (Room Upgrade) 实现 =====
// MSC1501: 房间版本升级

type RoomUpgradeVersion string

const (
	RoomVersion1  RoomUpgradeVersion = "1"
	RoomVersion2  RoomUpgradeVersion = "2"
	RoomVersion3  RoomUpgradeVersion = "3"
	RoomVersion4  RoomUpgradeVersion = "4"
	RoomVersion5  RoomUpgradeVersion = "5"
	RoomVersion6  RoomUpgradeVersion = "6"
	RoomVersion7  RoomUpgradeVersion = "7"
	RoomVersion8  RoomUpgradeVersion = "8"
	RoomVersion9  RoomUpgradeVersion = "9"
	RoomVersion10 RoomUpgradeVersion = "10"
	RoomVersion11 RoomUpgradeVersion = "11"
)

// UpgradeRoomRequest 房间升级请求
type UpgradeRoomRequest struct {
	NewVersion string `json:"new_version" binding:"required"`
}

// UpgradeRoomResponse 房间升级响应
type UpgradeRoomResponse struct {
	ReplacementRoom string `json:"replacement_room"`
}

// RoomUpgradeService 房间升级服务
type RoomUpgradeService struct {
	hs *HomeServer
}

// NewRoomUpgradeService 创建房间升级服务
func NewRoomUpgradeService(hs *HomeServer) *RoomUpgradeService {
	return &RoomUpgradeService{hs: hs}
}

// UpgradeRoom 升级房间到新版本
func (rus *RoomUpgradeService) UpgradeRoom(userID, roomID, newVersion string) (*UpgradeRoomResponse, *MatrixError) {
	// 验证新版本是否支持
	if !isSupportedRoomVersion(newVersion) {
		return nil, &MatrixError{
			ErrCode: "M_UNSUPPORTED_ROOM_VERSION",
			Error:   fmt.Sprintf("Room version %s is not supported", newVersion),
		}
	}

	// 获取旧房间信息
	oldRoom, err := rus.hs.Room.GetRoom(roomID)
	if err != nil {
		return nil, ErrNotFound
	}

	// 检查用户权限（需要是管理员或有足够权限）
	if !rus.canUpgradeRoom(roomID, userID) {
		return nil, ErrForbidden
	}

	// 检查房间是否已经是目标版本
	if oldRoom.RoomVersion == newVersion {
		return nil, &MatrixError{
			ErrCode: "M_BAD_REQUEST",
			Error:   "Room is already at the requested version",
		}
	}

	// 获取旧房间的当前状态
	oldState, err := rus.getRoomState(roomID)
	if err != nil {
		return nil, ErrInternal
	}

	// 创建新房间
	newRoomReq := &CreateRoomRequest{
		RoomVersion:     newVersion,
		Name:            oldRoom.Name,
		Topic:           oldRoom.Topic,
		Visibility:      "private", // 升级过程中保持私有，之后改为与旧房间相同
		RoomAliasName:   nil,       // 不创建别名，保留旧房间的别名
		CreationContent: map[string]interface{}{
			"room_version": newVersion,
			"predecessor": map[string]interface{}{
				"room_id":   roomID,
				"event_id":  oldRoom.LastEventID,
			},
		},
		InitialState: rus.buildInitialState(oldState),
		Preset:       "private_chat",
	}

	// 使用创建者 ID 创建新房间
	newRoomResp, matrixErr := rus.hs.Room.CreateRoom(userID, newRoomReq)
	if matrixErr != nil {
		return nil, matrixErr
	}

	newRoomID := newRoomResp.RoomID

	// 迁移成员（邀请/加入）
	rus.migrateMembers(roomID, newRoomID, userID)

	// 迁移推送规则
	rus.migratePushRules(roomID, newRoomID)

	// 在旧房间发送 tombstone 事件
	tombstoneContent := map[string]interface{}{
		"body":             fmt.Sprintf("This room has been replaced by %s", newRoomID),
		"replacement_room": newRoomID,
	}
	stateKey := ""
	rus.hs.Event.SendRoomEvent(roomID, userID, EventTypeRoomTombstone, nil, tombstoneContent, &stateKey)

	// 更新旧房间的 join_rules 为 upgrade
	rus.updateJoinRule(roomID, userID, "upgrade")

	// 更新房间别名指向新房间
	rus.migrateRoomAlias(roomID, newRoomID)

	// 更新数据库中的房间关系
	rus.recordRoomUpgrade(roomID, newRoomID, newVersion)

	log.Info("Room %s upgraded to %s (new room: %s)", roomID, newVersion, newRoomID)

	return &UpgradeRoomResponse{
		ReplacementRoom: newRoomID,
	}, nil
}

const (
	// EventTypeRoomTombstone 房间墓碑事件
	EventTypeRoomTombstone = "m.room.tombstone"
)

// canUpgradeRoom 检查用户是否可以升级房间
func (rus *RoomUpgradeService) canUpgradeRoom(roomID, userID string) bool {
	// 获取用户的权限等级
	var member MatrixRoomMember
	if err := db.GetDB().Where("room_id = ? AND user_id = ?", roomID, userID).First(&member).Error; err != nil {
		return false
	}

	// 获取权力等级
	var powerLevels MatrixRoomState
	if err := db.GetDB().Where("room_id = ? AND event_type = ?", roomID, EventTypeRoomPowerLevels).First(&powerLevels).Error; err != nil {
		// 如果没有权力等级事件，只有创建者可以升级
		room, _ := rus.hs.Room.GetRoom(roomID)
		if room != nil {
			return room.Creator == userID
		}
		return false
	}

	var plContent PowerLevelContent
	data, err := json.Marshal(powerLevels.Content)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(data, &plContent); err != nil {
		return false
	}

	userLevel := plContent.UsersDefault
	if level, ok := plContent.Users[userID]; ok {
		userLevel = level
	}

	// 升级房间需要至少 100 级（管理员）
	return userLevel >= 100
}

// getRoomState 获取房间的完整状态
func (rus *RoomUpgradeService) getRoomState(roomID string) (map[string]MatrixRoomState, error) {
	var states []MatrixRoomState
	if err := db.GetDB().Where("room_id = ?", roomID).Find(&states).Error; err != nil {
		return nil, err
	}

	stateMap := make(map[string]MatrixRoomState)
	for _, state := range states {
		key := string(state.EventType)
		if state.StateKey != "" {
			key = key + ":" + state.StateKey
		}
		stateMap[key] = state
	}

	return stateMap, nil
}

// buildInitialState 构建新房间的初始状态
func (rus *RoomUpgradeService) buildInitialState(oldState map[string]MatrixRoomState) []CreateRoomStateEvent {
	initialState := []CreateRoomStateEvent{}

	// 需要保留的状态事件类型
	stateTypes := []struct {
		eventType string
		stateKey  string
	}{
		{string(EventTypeRoomName), ""},
		{string(EventTypeRoomTopic), ""},
		{string(EventTypeRoomAvatar), ""},
		{string(EventTypeRoomPowerLevels), ""},
		{string(EventTypeRoomJoinRules), ""},
		{string(EventTypeRoomHistoryVisibility), ""},
		{string(EventTypeRoomGuestAccess), ""},
		{string(EventTypeRoomEncryption), ""},
	}

	for _, st := range stateTypes {
		key := st.eventType
		if st.stateKey != "" {
			key = key + ":" + st.stateKey
		}

		if state, ok := oldState[key]; ok {
			initialState = append(initialState, CreateRoomStateEvent{
				Type:     st.eventType,
				StateKey: st.stateKey,
				Content:  state.Content,
			})
		}
	}

	return initialState
}

// migrateMembers 迁移房间成员
func (rus *RoomUpgradeService) migrateMembers(oldRoomID, newRoomID, creatorID string) {
	// 获取旧房间的所有成员
	var members []MatrixRoomMember
	db.GetDB().Where("room_id = ? AND membership IN ?", oldRoomID, []string{"join", "invite"}).Find(&members)

	for _, member := range members {
		if member.UserID == creatorID {
			// 创建者已经在创建房间时加入
			continue
		}

		if member.Membership == MembershipJoin {
			// 自动加入成员到新房间
			// 使用 invite 然后他们可以选择加入
			rus.hs.Room.InviteUser(newRoomID, creatorID, &InviteUserRequest{
				UserID: member.UserID,
			})
		} else if member.Membership == MembershipInvite {
			// 邀请成员到新房间
			rus.hs.Room.InviteUser(newRoomID, creatorID, &InviteUserRequest{
				UserID: member.UserID,
			})
		}
	}
}

// migratePushRules copies room-specific push rules from old room to new room for all users.
// Uses FirstOrCreate for idempotency — safe to call multiple times (e.g. from both local and federated tombstone events).
func (rus *RoomUpgradeService) migratePushRules(oldRoomID, newRoomID string) {
	var rules []PushRule
	db.GetDB().Where("kind = ? AND rule_id = ?", "room", oldRoomID).Find(&rules)
	for _, rule := range rules {
		newRule := PushRule{
			UserID:        rule.UserID,
			Scope:         rule.Scope,
			Kind:          rule.Kind,
			RuleID:        newRoomID,
			Default:       rule.Default,
			Enabled:       rule.Enabled,
			Conditions:    rule.Conditions,
			Actions:       rule.Actions,
			Pattern:       rule.Pattern,
			PriorityClass: rule.PriorityClass,
		}
		db.GetDB().Where("user_id = ? AND scope = ? AND kind = ? AND rule_id = ?",
			rule.UserID, rule.Scope, rule.Kind, newRoomID).
			Assign(&newRule).FirstOrCreate(&newRule)
	}
}

// migrateRoomAlias 迁移房间别名
func (rus *RoomUpgradeService) migrateRoomAlias(oldRoomID, newRoomID string) {
	// 查找旧房间的别名
	var aliasStates []MatrixRoomState
	db.GetDB().Where("room_id = ? AND event_type = ?", oldRoomID, EventTypeRoomAliases).Find(&aliasStates)

	for _, state := range aliasStates {
		if aliases, ok := state.Content["aliases"].([]interface{}); ok {
			for _, alias := range aliases {
				if aliasStr, ok := alias.(string); ok {
					// 更新别名指向新房间
					rus.updateRoomAlias(aliasStr, newRoomID)
				}
			}
		}
	}

	// 更新 canonical_alias
	var canonicalAlias MatrixRoomState
	if err := db.GetDB().Where("room_id = ? AND event_type = ?", oldRoomID, EventTypeRoomCanonicalAlias).First(&canonicalAlias).Error; err == nil {
		if alias, ok := canonicalAlias.Content["alias"].(string); ok {
			rus.updateRoomAlias(alias, newRoomID)
		}
	}
}

// updateRoomAlias 更新房间别名
func (rus *RoomUpgradeService) updateRoomAlias(alias, newRoomID string) {
	// 删除旧别名映射
	db.GetDB().Where("alias = ?", alias).Delete(&MatrixRoomAlias{})

	// 创建新别名映射
	newAlias := &MatrixRoomAlias{
		RoomAlias: alias,
		RoomID:    newRoomID,
	}
	db.GetDB().Create(newAlias)
}

// recordRoomUpgrade 记录房间升级历史
func (rus *RoomUpgradeService) recordRoomUpgrade(oldRoomID, newRoomID, newVersion string) {
	upgrade := &MatrixRoomUpgrade{
		OldRoomID:  oldRoomID,
		NewRoomID:  newRoomID,
		NewVersion: newVersion,
		UpgradedAt: time.Now(),
	}
	db.GetDB().Create(upgrade)
}

// GetRoomUpgradeHistory 获取房间升级历史
func (rus *RoomUpgradeService) GetRoomUpgradeHistory(roomID string) ([]MatrixRoomUpgrade, error) {
	var history []MatrixRoomUpgrade
	if err := db.GetDB().Where("old_room_id = ? OR new_room_id = ?", roomID, roomID).
		Order("upgraded_at DESC").
		Find(&history).Error; err != nil {
		return nil, err
	}
	return history, nil
}

// IsRoomUpgraded 检查房间是否已被升级
func (rus *RoomUpgradeService) IsRoomUpgraded(roomID string) (string, bool) {
	var upgrade MatrixRoomUpgrade
	if err := db.GetDB().Where("old_room_id = ?", roomID).First(&upgrade).Error; err == nil {
		return upgrade.NewRoomID, true
	}
	return "", false
}

// ===== 数据库模型 =====

// MatrixRoomUpgrade 房间升级记录
type MatrixRoomUpgrade struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	OldRoomID  string    `gorm:"index;type:varchar(255);not null"`
	NewRoomID  string    `gorm:"index;type:varchar(255);not null"`
	NewVersion string    `gorm:"type:varchar(16);not null"`
	UpgradedAt time.Time `gorm:"index"`
}

func (MatrixRoomUpgrade) TableName() string {
	return "matrix_room_upgrades"
}

// ===== HTTP Handlers =====

// RegisterRoomUpgradeRoutes 注册房间升级路由
func RegisterRoomUpgradeRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// 房间升级端点
	rooms := r.Group("/_matrix/client/v3/rooms")
	rooms.Use(MatrixAuthMiddleware())
	{
		rooms.POST("/:roomId/upgrade", handleUpgradeRoom)
	}
}

// handleUpgradeRoom 处理房间升级请求
func handleUpgradeRoom(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	roomID := c.Param("roomId")

	var req UpgradeRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	resp, matrixErr := hs.RoomUpgrade.UpgradeRoom(userID, roomID, req.NewVersion)
	if matrixErr != nil {
		c.JSON(http.StatusBadRequest, ginErrFromMatrix(matrixErr))
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ginErrFromMatrix 转换 MatrixError 为 gin.H
func ginErrFromMatrix(err *MatrixError) gin.H {
	return gin.H{
		"errcode": err.ErrCode,
		"error":   err.Error,
	}
}

// isSupportedRoomVersion 检查是否支持该房间版本
func isSupportedRoomVersion(version string) bool {
	supported := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}
	for _, v := range supported {
		if v == version {
			return true
		}
	}
	return false
}

// updateJoinRule 更新房间加入规则
func (rus *RoomUpgradeService) updateJoinRule(roomID, userID, joinRule string) *MatrixError {
	content := map[string]interface{}{
		"join_rule": joinRule,
	}
	stateKey := ""
	_, err := rus.hs.Event.SendRoomEvent(roomID, userID, string(EventTypeRoomJoinRules), nil, content, &stateKey)
	return err
}
