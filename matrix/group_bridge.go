package matrix

import (
	"fmt"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

// SendGroupMessageToMatrix 将群组消息发送到 Matrix 房间.
func (b *Bridge) SendGroupMessageToMatrix(roomID string, senderAccountID uint64, content string) (string, error) {
	if !b.started.Load() {
		return "", nil
	}

	eventID, err := b.msgBridge.SendGroupMessageToMatrix(roomID, senderAccountID, content)
	if err != nil {
		return "", err
	}

	return eventID, nil
}

// SendGroupMessageToMatrix 将群组消息发送到 Matrix 房间.
func (m *MessageBridge) SendGroupMessageToMatrix(matrixRoomID string, senderAccountID uint64, content string) (string, error) {
	// 获取发送者对应的 Matrix 用户
	matrixUserID, err := m.userMapper.GetMatrixUserID(senderAccountID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get matrix user for sender")
	}

	// 确保 Bridge 用户在房间中（通过 bridge 用户发送邀请）
	if err := m.client.InviteUser(matrixRoomID, matrixUserID); err != nil {
		log.Warn("Failed to invite user to room: %v", err)
		// 继续尝试发送，可能已经在了
	}

	// 转换消息格式
	matrixContent := m.convertStatusToMatrixMessage(content)

	// 发送到 Matrix
	resp, err := m.client.SendRoomMessage(matrixRoomID, matrixUserID, matrixContent)
	if err != nil {
		return "", errors.Wrap(err, "failed to send group message to matrix")
	}

	// 创建消息映射（群组消息不关联到特定 Status，所以 StatusId 设为 0）
	if err := m.createGroupMessageMapping(matrixRoomID, resp.EventID, matrixUserID); err != nil {
		log.Error("Failed to create group message mapping: %v", err)
		// 非致命错误，继续
	}

	return resp.EventID, nil
}

// createGroupMessageMapping 创建群组消息映射记录.
func (m *MessageBridge) createGroupMessageMapping(roomID, eventID, senderID string) error {
	mapping := &models.MatrixMessage{
		StatusId:       0, // 群组消息不关联到特定 Status
		MatrixEventID:  eventID,
		MatrixRoomID:   roomID,
		MatrixSenderID: senderID,
		Direction:      "to_matrix_group",
	}

	return db.GetDB().Create(mapping).Error
}

// SyncGroupToMatrix 同步群组到 Matrix（创建或获取对应的 Matrix 房间）.
func (b *Bridge) SyncGroupToMatrix(groupID uint64) (string, error) {
	if !b.started.Load() {
		return "", nil
	}

	// 获取群组信息
	var group models.Group
	if err := db.GetDB().First(&group, groupID).Error; err != nil {
		return "", errors.Wrap(err, "group not found")
	}

	// 如果已经有 Matrix Room ID，直接返回
	if group.MatrixRoomId != nil && *group.MatrixRoomId != "" {
		return *group.MatrixRoomId, nil
	}

	// 获取群组成员
	var members []models.GroupMember
	if err := db.GetDB().Where("group_id = ?", groupID).Find(&members).Error; err != nil {
		return "", errors.Wrap(err, "failed to get group members")
	}

	// 获取成员对应的 Matrix 用户 ID
	var inviteUserIDs []string

	for _, member := range members {
		if member.AccountId == nil {
			continue
		}

		matrixUserID, err := b.userMapper.GetMatrixUserID(*member.AccountId)
		if err != nil {
			log.Warn("Failed to get matrix user for account %d: %v", *member.AccountId, err)

			continue
		}

		inviteUserIDs = append(inviteUserIDs, matrixUserID)
	}

	// 创建 Matrix 房间
	roomName := group.Name
	if roomName == "" {
		roomName = fmt.Sprintf("Group %d", groupID)
	}

	roomConfig := &CreateRoomConfig{
		Name:   roomName,
		Topic:  group.Description,
		Invite: inviteUserIDs,
	}

	roomID, err := b.client.CreateRoom(roomConfig)
	if err != nil {
		return "", errors.Wrap(err, "failed to create matrix room")
	}

	// 更新群组的 Matrix Room ID
	if err := db.GetDB().Model(&group).Update("matrix_room_id", roomID).Error; err != nil {
		log.Error("Failed to update group matrix_room_id: %v", err)
		// 不返回错误，因为房间已经创建成功了
	}

	return roomID, nil
}
