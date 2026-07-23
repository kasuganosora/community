package matrix

import (
	"strconv"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// GroupMapper 群组与 Matrix 房间映射服务.
type GroupMapper struct {
	client     *ASClient
	userMapper *UserMapper
}

// NewGroupMapper 创建群组映射服务.
func NewGroupMapper(client *ASClient, userMapper *UserMapper) *GroupMapper {
	return &GroupMapper{client: client, userMapper: userMapper}
}

// CreateBridgedGroup 创建与 Matrix 桥接的群组.
func (gm *GroupMapper) CreateBridgedGroup(ownerID uint64, name, description string) (*models.Group, error) {
	// 1. 创建本地群组
	group := &models.Group{
		OwnerId:     ownerID,
		Name:        name,
		Description: description,
		Type:        models.GroupTypeBridged,
		Visibility:  "private",
	}

	if err := db.GetDB().Create(group).Error; err != nil {
		return nil, errors.Wrap(err, "failed to create group")
	}

	// 2. 在 Matrix 创建房间
	roomAlias := gm.generateRoomAlias(group.Id)

	roomID, err := gm.client.CreateRoom(&CreateRoomConfig{
		Name:       name,
		Topic:      description,
		Alias:      roomAlias,
		IsDirect:   false,
		Preset:     "private_chat", //nolint:goconst
		Visibility: "private",
	})
	if err != nil {
		// 回滚本地群组
		db.GetDB().Delete(group)

		return nil, errors.Wrap(err, "failed to create Matrix room")
	}

	// 3. 更新群组关联 Matrix 房间
	group.MatrixRoomId = &roomID
	if err := db.GetDB().Save(group).Error; err != nil {
		return nil, errors.Wrap(err, "failed to update group with Matrix room ID")
	}

	// 4. 添加创建者为成员
	if err := gm.addLocalUserToGroup(group.Id, ownerID, models.GroupMemberRoleOwner); err != nil {
		log.Error("Failed to add owner as group member: %v", err)
	}

	log.Info("Created bridged group %d with Matrix room %s", group.Id, roomID)

	return group, nil
}

// BridgeExistingGroup 将现有群组桥接到 Matrix.
func (gm *GroupMapper) BridgeExistingGroup(groupID uint64) error {
	group := &models.Group{}
	if err := db.GetDB().First(group, groupID).Error; err != nil {
		return errors.Wrap(err, "group not found")
	}

	if group.IsBridged() {
		return errors.New("group is already bridged")
	}

	// 创建 Matrix 房间
	roomAlias := gm.generateRoomAlias(group.Id)

	roomID, err := gm.client.CreateRoom(&CreateRoomConfig{
		Name:       group.Name,
		Topic:      group.Description,
		Alias:      roomAlias,
		IsDirect:   false,
		Preset:     "private_chat",
		Visibility: group.Visibility,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create Matrix room")
	}

	// 更新群组
	group.MatrixRoomId = &roomID

	group.Type = models.GroupTypeBridged

	if err := db.GetDB().Save(group).Error; err != nil {
		return errors.Wrap(err, "failed to update group")
	}

	// 邀请所有本地成员到 Matrix 房间（使用 UserMapper 确保虚拟用户已注册）
	members := make([]*models.GroupMember, 0)
	db.GetDB().Where("group_id = ?", groupID).Find(&members)

	for _, member := range members {
		if member.IsLocalUser() {
			matrixUser, err := gm.userMapper.GetOrCreateMatrixUserByLocal(*member.AccountId)
			if err != nil {
				log.Error("Failed to ensure Matrix user for account %d: %v", *member.AccountId, err)

				continue
			}

			if err := gm.client.InviteUser(roomID, matrixUser.MatrixUserID); err != nil {
				log.Error("Failed to invite Matrix user %s to room: %v", matrixUser.MatrixUserID, err)
			}
		}
	}

	log.Info("Bridged group %d to Matrix room %s", groupID, roomID)

	return nil
}

// AddLocalMemberToGroup 添加本地用户到群组.
func (gm *GroupMapper) AddLocalMemberToGroup(groupID, accountID uint64, role models.GroupMemberRole) error {
	err := gm.addLocalUserToGroup(groupID, accountID, role)
	if err != nil {
		return err
	}

	// 如果群组已桥接，邀请用户到 Matrix 房间
	group := &models.Group{}
	err = db.GetDB().First(group, groupID).Error
	if err != nil {
		return err
	}

	if group.IsBridged() {
		matrixUser, err := gm.userMapper.GetOrCreateMatrixUserByLocal(accountID)
		if err != nil {
			log.Error("Failed to ensure Matrix user for account %d: %v", accountID, err)
		} else {
			err := gm.client.InviteUser(*group.MatrixRoomId, matrixUser.MatrixUserID)
			if err != nil {
				log.Error("Failed to invite user to Matrix room: %v", err)
			}
		}
	}

	return nil
}

// AddMatrixMemberToGroup 添加 Matrix 用户到群组.
func (gm *GroupMapper) AddMatrixMemberToGroup(groupID uint64, matrixUserID, displayName string, role models.GroupMemberRole) error {
	member := &models.GroupMember{
		GroupId:      groupID,
		MatrixUserId: &matrixUserID,
		DisplayName:  displayName,
		Role:         role,
	}

	err := db.GetDB().Create(member).Error
	if err != nil {
		return errors.Wrap(err, "failed to add Matrix member")
	}

	return nil
}

// RemoveMemberFromGroup 从群组移除成员.
func (gm *GroupMapper) RemoveMemberFromGroup(groupID uint64, accountID *uint64, matrixUserID *string) error {
	query := db.GetDB().Where("group_id = ?", groupID)
	if accountID != nil {
		query = query.Where("account_id = ?", *accountID)
	}

	if matrixUserID != nil {
		query = query.Where("matrix_user_id = ?", *matrixUserID)
	}

	err := query.Delete(&models.GroupMember{}).Error
	if err != nil {
		return errors.Wrap(err, "failed to remove member")
	}

	return nil
}

// GetGroupByMatrixRoom 通过 Matrix 房间ID获取群组.
func (gm *GroupMapper) GetGroupByMatrixRoom(roomID string) (*models.Group, error) {
	group := &models.Group{}
	err := db.GetDB().Where("matrix_room_id = ?", roomID).First(group).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil //nolint:nilnil // not found is not an error
		}

		return nil, err
	}

	return group, nil
}

// GetGroupMembers 获取群组成员列表.
func (gm *GroupMapper) GetGroupMembers(groupID uint64) ([]*models.GroupMember, error) {
	members := make([]*models.GroupMember, 0)
	err := db.GetDB().Where("group_id = ?", groupID).Preload("Account").Find(&members).Error
	if err != nil {
		return nil, err
	}

	return members, nil
}

// IsGroupMember 检查用户是否为群组成员.
func (gm *GroupMapper) IsGroupMember(groupID uint64, accountID *uint64, matrixUserID *string) (bool, error) {
	query := db.GetDB().Model(&models.GroupMember{}).Where("group_id = ?", groupID)
	if accountID != nil {
		query = query.Where("account_id = ?", *accountID)
	}

	if matrixUserID != nil {
		query = query.Where("matrix_user_id = ?", *matrixUserID)
	}

	var count int64
	err := query.Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// addLocalUserToGroup 内部方法：添加本地用户到群组.
func (gm *GroupMapper) addLocalUserToGroup(groupID, accountID uint64, role models.GroupMemberRole) error {
	member := &models.GroupMember{
		GroupId:   groupID,
		AccountId: &accountID,
		Role:      role,
	}

	err := db.GetDB().Create(member).Error
	if err != nil {
		return errors.Wrap(err, "failed to add member")
	}

	return nil
}

// generateRoomAlias 生成 Matrix 房间别名.
func (gm *GroupMapper) generateRoomAlias(groupID uint64) string {
	LoadConfig()

	return GlobalConfig.RoomNamespacePrefix + "_group_" + strconv.FormatUint(groupID, 10)
}
