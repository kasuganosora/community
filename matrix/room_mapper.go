package matrix

import (
	"fmt"
	"sync"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// RoomMapper 房间映射服务
// 负责 Matrix 房间与本地 Conversation 之间的双向映射
type RoomMapper struct {
	client     *ASClient
	userMapper *UserMapper
	cache      *roomCache
}

type roomCache struct {
	sync.RWMutex
	convToRoom map[uint64]*models.MatrixRoom // conversation_id -> MatrixRoom
	roomToConv map[string]*models.MatrixRoom // matrix_room_id -> MatrixRoom
}

// NewRoomMapper 创建房间映射服务
func NewRoomMapper(client *ASClient, userMapper *UserMapper) *RoomMapper {
	return &RoomMapper{
		client:     client,
		userMapper: userMapper,
		cache: &roomCache{
			convToRoom: make(map[uint64]*models.MatrixRoom),
			roomToConv: make(map[string]*models.MatrixRoom),
		},
	}
}

// GetOrCreateRoomByConversation 获取或创建本地会话对应的 Matrix 房间
// 方向：本地 Conversation -> Matrix Room
func (r *RoomMapper) GetOrCreateRoomByConversation(convID uint64, participants []string) (*models.MatrixRoom, error) {
	// 1. 先查缓存
	r.cache.RLock()
	if room, ok := r.cache.convToRoom[convID]; ok {
		r.cache.RUnlock()
		return room, nil
	}
	r.cache.RUnlock()

	// 2. 查数据库
	var matrixRoom models.MatrixRoom
	err := db.GetDB().Where("conversation_id = ?", convID).First(&matrixRoom).Error

	if err == nil {
		r.cacheRoom(&matrixRoom)
		return &matrixRoom, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.Wrap(err, "failed to query matrix room")
	}

	// 3. 不存在则创建新的 Matrix 房间
	return r.createRoomForConversation(convID, participants)
}

// createRoomForConversation 为本地会话创建 Matrix 房间
func (r *RoomMapper) createRoomForConversation(convID uint64, participants []string) (*models.MatrixRoom, error) {
	LoadConfig()

	// 检查 participants 是否为空
	if len(participants) == 0 {
		return nil, fmt.Errorf("no participants provided for conversation %d", convID)
	}

	// 获取本地会话
	var conv models.Conversation
	if err := db.GetDB().First(&conv, convID).Error; err != nil {
		return nil, errors.Wrap(err, "failed to find conversation")
	}

	// Bridge 机器人必须是创建者
	bridgeUserID := GlobalConfig.GetBridgeUserID()

	// 确保 Bridge 用户存在
	if err := r.client.EnsureVirtualUser(GlobalConfig.BridgeUsername); err != nil {
		return nil, errors.Wrap(err, "failed to ensure bridge user")
	}

	// 创建 Matrix 私信房间
	resp, err := r.client.CreateDirectRoom(bridgeUserID, participants[0], true)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create matrix room")
	}

	// 邀请其他参与者
	for i, participant := range participants {
		if i == 0 {
			continue // 第一个已经在创建时邀请
		}
		if err := r.client.InviteToRoom(resp.RoomID, bridgeUserID, participant); err != nil {
			log.Warn("failed to invite user %s to room %s: %v", participant, resp.RoomID, err)
		}
	}

	// 解析房间 ID
	localpart, server, ok := ParseMatrixRoomID(resp.RoomID)
	if !ok {
		return nil, fmt.Errorf("invalid room id: %s", resp.RoomID)
	}

	// 创建数据库记录
	matrixRoom := &models.MatrixRoom{
		ConversationId:      convID,
		MatrixRoomID:        resp.RoomID,
		MatrixRoomLocalpart: localpart,
		MatrixServer:        server,
		IsDirect:            true,
	}

	if err := db.GetDB().Create(matrixRoom).Error; err != nil {
		return nil, errors.Wrap(err, "failed to create matrix room mapping")
	}

	r.cacheRoom(matrixRoom)
	return matrixRoom, nil
}

// GetOrCreateConversationByRoom 获取或创建 Matrix 房间对应的本地会话
// 方向：Matrix Room -> 本地 Conversation
func (r *RoomMapper) GetOrCreateConversationByRoom(roomID string, participantIDs []string) (*models.MatrixRoom, error) {
	// 1. 先查缓存
	r.cache.RLock()
	if room, ok := r.cache.roomToConv[roomID]; ok {
		r.cache.RUnlock()
		return room, nil
	}
	r.cache.RUnlock()

	// 2. 查数据库
	var matrixRoom models.MatrixRoom
	err := db.GetDB().Where("matrix_room_id = ?", roomID).First(&matrixRoom).Error

	if err == nil {
		r.cacheRoom(&matrixRoom)
		return &matrixRoom, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.Wrap(err, "failed to query matrix room")
	}

	// 3. 不存在则创建新的本地会话
	return r.createConversationForRoom(roomID, participantIDs)
}

// createConversationForRoom 为 Matrix 房间创建本地会话
func (r *RoomMapper) createConversationForRoom(roomID string, participantIDs []string) (*models.MatrixRoom, error) {
	LoadConfig()

	// 获取房间成员信息，确定会话参与者
	accountIDs := make([]uint64, 0)

	// 将 Matrix 用户 ID 映射为本地账号 ID（使用共享的 UserMapper）
	for _, matrixUserID := range participantIDs {
		// 跳过 Bridge 用户
		if matrixUserID == GlobalConfig.GetBridgeUserID() {
			continue
		}

		localAccountID, err := r.userMapper.GetLocalAccountID(matrixUserID)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get local account for matrix user")
		}
		accountIDs = append(accountIDs, localAccountID)
	}

	if len(accountIDs) == 0 {
		return nil, fmt.Errorf("no valid participants for conversation")
	}

	// 创建本地会话
	conv := &models.Conversation{
		AccountsIds: accountIDs,
	}

	tx := db.GetDB().Begin()
	if err := tx.Create(conv).Error; err != nil {
		tx.Rollback()
		return nil, errors.Wrap(err, "failed to create conversation")
	}

	// 解析房间 ID
	localpart, server, ok := ParseMatrixRoomID(roomID)
	if !ok {
		tx.Rollback()
		return nil, fmt.Errorf("invalid room id: %s", roomID)
	}

	// 创建映射记录
	matrixRoom := &models.MatrixRoom{
		ConversationId:      conv.Id,
		MatrixRoomID:        roomID,
		MatrixRoomLocalpart: localpart,
		MatrixServer:        server,
		IsDirect:            true,
	}

	if err := tx.Create(matrixRoom).Error; err != nil {
		tx.Rollback()
		return nil, errors.Wrap(err, "failed to create matrix room mapping")
	}

	if err := tx.Commit().Error; err != nil {
		return nil, errors.Wrap(err, "failed to commit transaction")
	}

	matrixRoom.Conversation = conv
	r.cacheRoom(matrixRoom)
	return matrixRoom, nil
}

// getExistingRoom 仅查找已有的房间映射（不创建新会话）
func (r *RoomMapper) getExistingRoom(roomID string) (*models.MatrixRoom, error) {
	// 1. 先查缓存
	r.cache.RLock()
	if room, ok := r.cache.roomToConv[roomID]; ok {
		r.cache.RUnlock()
		return room, nil
	}
	r.cache.RUnlock()

	// 2. 查数据库
	var matrixRoom models.MatrixRoom
	err := db.GetDB().Where("matrix_room_id = ?", roomID).First(&matrixRoom).Error
	if err != nil {
		return nil, errors.Wrap(err, "room mapping not found")
	}

	r.cacheRoom(&matrixRoom)
	return &matrixRoom, nil
}

// GetConversationID 获取 Matrix 房间对应的本地会话 ID（仅查找已有映射）
func (r *RoomMapper) GetConversationID(roomID string) (uint64, error) {
	matrixRoom, err := r.getExistingRoom(roomID)
	if err != nil {
		return 0, err
	}
	return matrixRoom.ConversationId, nil
}

// GetMatrixRoomID 获取本地会话对应的 Matrix 房间 ID
func (r *RoomMapper) GetMatrixRoomID(convID uint64) (string, error) {
	// 先查缓存
	r.cache.RLock()
	if room, ok := r.cache.convToRoom[convID]; ok {
		r.cache.RUnlock()
		return room.MatrixRoomID, nil
	}
	r.cache.RUnlock()

	// 查数据库
	var matrixRoom models.MatrixRoom
	err := db.GetDB().Where("conversation_id = ?", convID).First(&matrixRoom).Error
	if err != nil {
		return "", errors.Wrap(err, "room mapping not found")
	}

	r.cacheRoom(&matrixRoom)
	return matrixRoom.MatrixRoomID, nil
}

// UpdateLastSync 更新房间的最后同步信息
func (r *RoomMapper) UpdateLastSync(roomID, eventID string) error {
	now := timeNow()
	err := db.GetDB().Model(&models.MatrixRoom{}).
		Where("matrix_room_id = ?", roomID).
		Updates(map[string]interface{}{
			"last_event_id": eventID,
			"last_sync_at":  &now,
		}).Error

	if err != nil {
		return errors.Wrap(err, "failed to update last sync")
	}

	// 更新缓存（需要写锁，因为修改了缓存对象的字段）
	r.cache.Lock()
	if room, ok := r.cache.roomToConv[roomID]; ok {
		room.LastEventID = eventID
		room.LastSyncAt = &now
	}
	r.cache.Unlock()

	return nil
}

// IsDirectRoom 检查是否为私信房间（仅查找已有映射）
func (r *RoomMapper) IsDirectRoom(roomID string) (bool, error) {
	matrixRoom, err := r.getExistingRoom(roomID)
	if err != nil {
		return false, err
	}
	return matrixRoom.IsDirect, nil
}

// GetRoomInfo 获取 Matrix 房间信息
func (r *RoomMapper) GetRoomInfo(roomID string) (*RoomInfo, error) {
	events, err := r.client.GetRoomMembers(roomID, "")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get room members")
	}

	info := &RoomInfo{
		RoomID:  roomID,
		Members: make(map[string]string),
	}

	for _, event := range events {
		if event.Type == EventTypeRoomMember {
			content := event.ToRoomMemberContent()
			if content != nil && event.StateKey != nil {
				info.Members[*event.StateKey] = content.Membership
			}
		}
	}

	return info, nil
}

// cacheRoom 缓存房间映射
func (r *RoomMapper) cacheRoom(room *models.MatrixRoom) {
	r.cache.Lock()
	defer r.cache.Unlock()

	r.cache.convToRoom[room.ConversationId] = room
	r.cache.roomToConv[room.MatrixRoomID] = room
}

// ClearCache 清除缓存
func (r *RoomMapper) ClearCache() {
	r.cache.Lock()
	defer r.cache.Unlock()

	r.cache.convToRoom = make(map[uint64]*models.MatrixRoom)
	r.cache.roomToConv = make(map[string]*models.MatrixRoom)
}
