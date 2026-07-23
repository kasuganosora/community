package group

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/matrix"
	"github.com/kasuganosora/community/models"
)

// CreateGroupRequest 创建群组请求.
type CreateGroupRequest struct {
	Name           string `binding:"required,min=1,max=100"         json:"name"`
	Description    string `binding:"max=500"                        json:"description"`
	Visibility     string `binding:"omitempty,oneof=public private" json:"visibility"`
	BridgeToMatrix bool   `json:"bridge_to_matrix"` // 是否桥接到 Matrix
}

// CreateGroup 创建群组.
func CreateGroup(c *gin.Context) {
	var req CreateGroupRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	currentAccount := controllers.MustGetAccount(c)

	visibility := req.Visibility
	if visibility == "" {
		visibility = "private" //nolint:goconst
	}

	group := &models.Group{
		OwnerId:     currentAccount.Id,
		Name:        req.Name,
		Description: req.Description,
		Type:        models.GroupTypeLocal,
		Visibility:  visibility,
	}

	// 如果要求桥接到 Matrix 且 Matrix 已启用
	if req.BridgeToMatrix && matrix.IsEnabled() {
		bridge := matrix.GetBridge()
		if bridge != nil && bridge.IsStarted() {
			// 使用 GroupMapper 创建桥接群组
			groupMapper := matrix.NewGroupMapper(bridge.GetClient(), bridge.GetUserMapper())

			bridgedGroup, err := groupMapper.CreateBridgedGroup(currentAccount.Id, req.Name, req.Description)
			if err != nil {
				log.Error("Failed to create bridged group: %v", err)
				// 降级为本地群组
				group.Type = models.GroupTypeLocal
			} else {
				group = bridgedGroup
			}
		}
	}

	if group.Id == 0 {
		err := db.GetDB().Create(group).Error
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create group"})

			return
		}

		// 添加创建者为所有者
		member := &models.GroupMember{
			GroupId:   group.Id,
			AccountId: &currentAccount.Id,
			Role:      models.GroupMemberRoleOwner,
		}
		db.GetDB().Create(member)
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":             group.Id,
		"name":           group.Name,
		"description":    group.Description,
		"visibility":     group.Visibility,
		"type":           group.Type,
		"matrix_room_id": group.MatrixRoomId,
	})
}

// InviteMemberRequest 邀请成员请求.
type InviteMemberRequest struct {
	AccountId    *uint64 `json:"account_id"`     // 本地用户ID
	MatrixUserId *string `json:"matrix_user_id"` // Matrix 用户ID
	Role         int     `json:"role"`           // 角色
}

// InviteMember 邀请成员加入群组.
func InviteMember(c *gin.Context) {
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"}) //nolint:goconst

		return
	}

	var req InviteMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	if req.AccountId == nil && req.MatrixUserId == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Either account_id or matrix_user_id is required"})

		return
	}

	currentAccount := controllers.MustGetAccount(c)

	// 检查权限（只有所有者和管理员可以邀请）
	var memberCount int64
	if err := db.GetDB().Model(&models.GroupMember{}).
		Where("group_id = ? AND account_id = ? AND role IN ?",
			groupID, currentAccount.Id, []int{int(models.GroupMemberRoleOwner), int(models.GroupMemberRoleAdmin)}).
		Count(&memberCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check permissions"})

		return
	}

	if memberCount == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "No permission to invite members"})

		return
	}

	role := models.GroupMemberRole(req.Role)
	if role != models.GroupMemberRoleMember && role != models.GroupMemberRoleAdmin {
		role = models.GroupMemberRoleMember
	}

	// 添加成员
	newMember := &models.GroupMember{
		GroupId:      groupID,
		AccountId:    req.AccountId,
		MatrixUserId: req.MatrixUserId,
		Role:         role,
		InvitedBy:    &currentAccount.Id,
	}

	if err := db.GetDB().Create(newMember).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add member"})

		return
	}

	// 如果群组已桥接，邀请到 Matrix 房间
	group := &models.Group{}
	if err := db.GetDB().First(group, groupID).Error; err == nil && group.IsBridged() {
		if matrix.IsEnabled() {
			bridge := matrix.GetBridge()
			if bridge != nil {
				groupMapper := matrix.NewGroupMapper(bridge.GetClient(), bridge.GetUserMapper())
				if req.AccountId != nil {
					err := groupMapper.AddLocalMemberToGroup(groupID, *req.AccountId, role)
					if err != nil {
						log.Warn("Failed to add local member to group: %v", err)
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Member invited successfully"}) //nolint:goconst
}

// RemoveMember 移除群组成员.
func RemoveMember(c *gin.Context) {
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})

		return
	}

	memberID, err := strconv.ParseUint(c.Param("member_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid member ID"})

		return
	}

	currentAccount := controllers.MustGetAccount(c)

	// 检查权限
	var memberCount int64
	if err := db.GetDB().Model(&models.GroupMember{}).
		Where("group_id = ? AND account_id = ? AND role IN ?",
			groupID, currentAccount.Id, []int{int(models.GroupMemberRoleOwner), int(models.GroupMemberRoleAdmin)}).
		Count(&memberCount).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check permissions"})

		return
	}

	if memberCount == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "No permission to remove members"})

		return
	}

	if err := db.GetDB().Where("id = ? AND group_id = ?", memberID, groupID).
		Delete(&models.GroupMember{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove member"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Member removed successfully"})
}

// GetGroup 获取群组详情.
func GetGroup(c *gin.Context) {
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})

		return
	}

	group := &models.Group{}
	if err := db.GetDB().Preload("Owner").Preload("Members.Account").First(group, groupID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"}) //nolint:goconst

		return
	}

	c.JSON(http.StatusOK, group)
}

// GetMyGroups 获取当前用户的群组列表.
func GetMyGroups(c *gin.Context) {
	currentAccount := controllers.MustGetAccount(c)

	var memberships []*models.GroupMember

	db.GetDB().Where("account_id = ?", currentAccount.Id).Preload("Group").Find(&memberships)

	groups := make([]*models.Group, 0, len(memberships))

	for _, m := range memberships {
		if m.Group != nil {
			groups = append(groups, m.Group)
		}
	}

	c.JSON(http.StatusOK, groups)
}

// GetGroupMessages 获取群组消息.
func GetGroupMessages(c *gin.Context) {
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})

		return
	}

	currentAccount := controllers.MustGetAccount(c)

	// 检查是否为成员
	var memberCount int64

	db.GetDB().Model(&models.GroupMember{}).
		Where("group_id = ? AND account_id = ?", groupID, currentAccount.Id).
		Count(&memberCount)

	if memberCount == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "Not a member of this group"})

		return
	}

	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit == 0 || limit > 100 {
		limit = 50
	}

	var messages []*models.GroupMessage

	db.GetDB().Where("group_id = ?", groupID).
		Order("created_at DESC").
		Limit(limit).
		Preload("Sender").
		Find(&messages)

	c.JSON(http.StatusOK, messages)
}

// SendMessageRequest 发送消息请求.
type SendMessageRequest struct {
	Content string `binding:"required" json:"content"`
}

// SendGroupMessage 发送群组消息.
func SendGroupMessage(c *gin.Context) {
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})

		return
	}

	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

		return
	}

	currentAccount := controllers.MustGetAccount(c)

	// 检查是否为成员
	var memberCount int64

	db.GetDB().Model(&models.GroupMember{}).
		Where("group_id = ? AND account_id = ?", groupID, currentAccount.Id).
		Count(&memberCount)

	if memberCount == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "Not a member of this group"})

		return
	}

	// 创建消息
	message := &models.GroupMessage{
		GroupId:  groupID,
		SenderId: &currentAccount.Id,
		Content:  req.Content,
	}

	if err := db.GetDB().Create(message).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send message"})

		return
	}

	// 如果群组已桥接，同步到 Matrix
	group := &models.Group{}
	if err := db.GetDB().First(group, groupID).Error; err == nil && group.IsBridged() {
		if matrix.IsEnabled() {
			bridge := matrix.GetBridge()
			if bridge != nil {
				go func() {
					// 发送到 Matrix 房间
					if group.MatrixRoomId != nil && *group.MatrixRoomId != "" {
						_, err := bridge.SendGroupMessageToMatrix(*group.MatrixRoomId, currentAccount.Id, req.Content)
						if err != nil {
							log.Error("Failed to sync group message %d to Matrix: %v", message.Id, err)
						} else {
							log.Debug("Synced group message %d to Matrix room %s", message.Id, *group.MatrixRoomId)
						}
					}
				}()
			}
		}
	}

	c.JSON(http.StatusCreated, message)
}

// DeleteGroup 删除群组.
func DeleteGroup(c *gin.Context) {
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})

		return
	}

	currentAccount := controllers.MustGetAccount(c)

	// 检查是否为所有者
	group := &models.Group{}
	if err := db.GetDB().First(group, groupID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})

		return
	}

	if group.OwnerId != currentAccount.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only the owner can delete the group"})

		return
	}

	if err := db.GetDB().Delete(group).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete group"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Group deleted successfully"})
}

// BridgeGroup 将本地群组桥接到 Matrix.
func BridgeGroup(c *gin.Context) {
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group ID"})

		return
	}

	currentAccount := controllers.MustGetAccount(c)

	// 检查是否为所有者
	group := &models.Group{}
	if err := db.GetDB().First(group, groupID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})

		return
	}

	if group.OwnerId != currentAccount.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only the owner can bridge the group"})

		return
	}

	if group.IsBridged() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Group is already bridged"})

		return
	}

	if !matrix.IsEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Matrix bridge is not enabled"})

		return
	}

	bridge := matrix.GetBridge()
	if bridge == nil || !bridge.IsStarted() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Matrix bridge is not running"})

		return
	}

	groupMapper := matrix.NewGroupMapper(bridge.GetClient(), bridge.GetUserMapper())
	if err := groupMapper.BridgeExistingGroup(groupID); err != nil {
		log.Error("Failed to bridge group: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to bridge group to Matrix"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Group bridged to Matrix successfully"})
}
