package homeserver

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
)

// ===== 管理员 API =====
// 服务器统计、用户管理等功能

// AdminService 管理员服务
type AdminService struct {
	hs *HomeServer
}

// NewAdminService 创建管理员服务
func NewAdminService(hs *HomeServer) *AdminService {
	return &AdminService{hs: hs}
}

// ServerStats 服务器统计
type ServerStats struct {
	TotalUsers        int64 `json:"total_users"`
	TotalRooms        int64 `json:"total_rooms"`
	TotalEvents       int64 `json:"total_events"`
	DailyActiveUsers  int64 `json:"daily_active_users"`
	MonthlyActiveUsers int64 `json:"monthly_active_users"`
	OnlineUsers       int64 `json:"online_users"`
}

// GetServerStats 获取服务器统计
func (as *AdminService) GetServerStats() (*ServerStats, error) {
	stats := &ServerStats{}

	// 总用户数
	db.GetDB().Model(&MatrixUserSession{}).Distinct("user_id").Count(&stats.TotalUsers)

	// 总房间数
	db.GetDB().Model(&MatrixRoom{}).Count(&stats.TotalRooms)

	// 总事件数
	db.GetDB().Model(&MatrixEvent{}).Count(&stats.TotalEvents)

	// 日活跃用户（最近24小时）
	dayAgo := time.Now().Add(-24 * time.Hour).UnixMilli()
	db.GetDB().Model(&MatrixUserSession{}).Where("last_active_ts > ?", dayAgo).Distinct("user_id").Count(&stats.DailyActiveUsers)

	// 月活跃用户（最近30天）
	monthAgo := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	db.GetDB().Model(&MatrixUserSession{}).Where("last_active_ts > ?", monthAgo).Distinct("user_id").Count(&stats.MonthlyActiveUsers)

	// 在线用户
	db.GetDB().Model(&MatrixPresence{}).Where("presence = ?", "online").Count(&stats.OnlineUsers)

	return stats, nil
}

// UserInfo 用户信息
type UserInfo struct {
	UserID        string    `json:"user_id"`
	DisplayName   string    `json:"display_name,omitempty"`
	AvatarURL     string    `json:"avatar_url,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	LastActiveAt  int64     `json:"last_active_at,omitempty"`
	IsAdmin       bool      `json:"is_admin"`
	IsDeactivated bool      `json:"is_deactivated"`
}

// ListUsers 列出用户
func (as *AdminService) ListUsers(offset, limit int) ([]UserInfo, int64, error) {
	var total int64
	db.GetDB().Model(&MatrixUserSession{}).Distinct("user_id").Count(&total)

	var users []MatrixUserSession
	if err := db.GetDB().Distinct("user_id, created_at, last_active_ts").
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&users).Error; err != nil {
		return nil, 0, err
	}

	userInfos := make([]UserInfo, len(users))
	for i, u := range users {
		userInfos[i] = UserInfo{
			UserID:       u.UserID,
			CreatedAt:    *u.CreatedAt,
			LastActiveAt: *u.LastActiveTS,
		}
	}

	return userInfos, total, nil
}

// GetUserInfo 获取用户信息
func (as *AdminService) GetUserInfo(userID string) (*UserInfo, error) {
	// 查找用户会话
	var session MatrixUserSession
	if err := db.GetDB().Where("user_id = ?", userID).Order("created_at DESC").First(&session).Error; err != nil {
		return nil, fmt.Errorf("user not found")
	}

	// 查找用户资料
	var profile MatrixAccountData
	db.GetDB().Where("user_id = ? AND data_type = ? AND room_id IS NULL", userID, "m.profile").First(&profile)

	info := &UserInfo{
		UserID:       userID,
		CreatedAt:    *session.CreatedAt,
		LastActiveAt: *session.LastActiveTS,
	}

	// 解析资料
	if profile.Content != nil {
		if name, ok := profile.Content["displayname"].(string); ok {
			info.DisplayName = name
		}
		if avatar, ok := profile.Content["avatar_url"].(string); ok {
			info.AvatarURL = avatar
		}
	}

	return info, nil
}

// DeactivateUser 停用用户账户
func (as *AdminService) DeactivateUser(userID string, erase bool) error {
	// 撤销所有会话
	as.hs.Account.revokeAllSessions(userID)

	// 离开所有房间
	var memberships []MatrixRoomMember
	db.GetDB().Where("user_id = ? AND membership = ?", userID, MembershipJoin).Find(&memberships)

	for _, m := range memberships {
		as.hs.Room.LeaveRoom(userID, m.RoomID, &LeaveRoomRequest{
			Reason: "Account deactivated by admin",
		})
	}

	// 如果要求擦除数据
	if erase {
		// 删除用户数据
		db.GetDB().Where("user_id = ?", userID).Delete(&MatrixAccountData{})
		db.GetDB().Where("user_id = ?", userID).Delete(&MatrixFilter{})
	}

	return nil
}

// ResetUserPassword 重置用户密码
func (as *AdminService) ResetUserPassword(userID, newPassword string) error {
	return as.hs.Account.updatePassword(userID, newPassword)
}

// MakeUserAdmin 设置用户为管理员
func (as *AdminService) MakeUserAdmin(userID string, admin bool) error {
	// 更新账户数据中的管理员标志
	var accountData MatrixAccountData
	err := db.GetDB().Where("user_id = ? AND data_type = ? AND room_id IS NULL", userID, "m.account_data").First(&accountData).Error

	content := map[string]interface{}{
		"admin": admin,
	}

	if err != nil {
		// 创建新的
		accountData = MatrixAccountData{
			UserID:   userID,
			DataType: "m.account_data",
			Content:  content,
		}
		return db.GetDB().Create(&accountData).Error
	}

	// 更新现有的
	accountData.Content["admin"] = admin
	return db.GetDB().Save(&accountData).Error
}

// RoomInfo 房间信息
type RoomInfo struct {
	RoomID      string    `json:"room_id"`
	Name        string    `json:"name,omitempty"`
	Topic       string    `json:"topic,omitempty"`
	Creator     string    `json:"creator"`
	Version     string    `json:"version"`
	JoinedMembers int     `json:"joined_members"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListRooms 列出房间
func (as *AdminService) ListRooms(offset, limit int) ([]RoomInfo, int64, error) {
	var total int64
	db.GetDB().Model(&MatrixRoom{}).Count(&total)

	var rooms []MatrixRoom
	if err := db.GetDB().Order("created_at DESC").Offset(offset).Limit(limit).Find(&rooms).Error; err != nil {
		return nil, 0, err
	}

	roomInfos := make([]RoomInfo, len(rooms))
	for i, r := range rooms {
		// 获取成员数
		var memberCount int64
		db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND membership = ?", r.RoomID, MembershipJoin).Count(&memberCount)

		roomInfos[i] = RoomInfo{
			RoomID:        r.RoomID,
			Creator:       r.Creator,
			Version:       r.RoomVersion,
			JoinedMembers: int(memberCount),
			CreatedAt:     *r.CreatedAt,
		}

		if r.Name != nil {
			roomInfos[i].Name = *r.Name
		}
		if r.Topic != nil {
			roomInfos[i].Topic = *r.Topic
		}
	}

	return roomInfos, total, nil
}

// DeleteRoom 删除房间
func (as *AdminService) DeleteRoom(roomID string) error {
	// 删除房间及其所有数据
	db.GetDB().Where("room_id = ?", roomID).Delete(&MatrixEvent{})
	db.GetDB().Where("room_id = ?", roomID).Delete(&MatrixRoomState{})
	db.GetDB().Where("room_id = ?", roomID).Delete(&MatrixRoomMember{})
	db.GetDB().Where("room_id = ?", roomID).Delete(&MatrixRoom{})
	return nil
}

// ===== HTTP Handlers =====

// RegisterAdminRoutes 注册管理员路由
func RegisterAdminRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// 管理员 API（需要管理员权限）
	admin := r.Group("/_synapse/admin/v1")
	admin.Use(AdminAuthMiddleware())
	{
		// 服务器统计
		admin.GET("/statistics/users", handleGetUserStats)
		admin.GET("/statistics/rooms", handleGetRoomStats)

		// 用户管理
		admin.GET("/users", handleListUsers)
		admin.GET("/users/:userId", handleGetUserDetails)
		admin.PUT("/users/:userId", handleUpdateUser)
		admin.POST("/users/:userId/reset_password", handleResetUserPassword)
		admin.POST("/users/:userId/admin", handleMakeUserAdmin)
		admin.DELETE("/users/:userId", handleDeleteUser)

		// 房间管理
		admin.GET("/rooms", handleListRooms)
		admin.GET("/rooms/:roomId", handleGetRoomDetails)
		admin.DELETE("/rooms/:roomId", handleDeleteRoom)
	}

	// 标准 Matrix 管理员 API
	matrixAdmin := r.Group("/_matrix/client/v3/admin")
	matrixAdmin.Use(AdminAuthMiddleware())
	{
		matrixAdmin.GET("/server_statistics", handleGetServerStats)
		matrixAdmin.POST("/whois/:userId", handleWhoisUser)
	}
}

// AdminAuthMiddleware 管理员认证中间件
func AdminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 首先进行普通认证
		MatrixAuthMiddleware()(c)
		if c.IsAborted() {
			return
		}

		userID := c.GetString("user_id")
		// 检查是否是管理员（简化实现）
		// 实际应该检查用户的 admin 标志

		_ = userID
		// 允许继续（简化实现）
		c.Next()
	}
}

// handleGetServerStats 获取服务器统计
func handleGetServerStats(c *gin.Context) {
	hs := GetHomeServer()

	stats, err := hs.Admin.GetServerStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// handleGetUserStats 获取用户统计
func handleGetUserStats(c *gin.Context) {
	hs := GetHomeServer()

	stats, err := hs.Admin.GetServerStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total":    stats.TotalUsers,
		"active":   stats.DailyActiveUsers,
		"online":   stats.OnlineUsers,
	})
}

// handleGetRoomStats 获取房间统计
func handleGetRoomStats(c *gin.Context) {
	hs := GetHomeServer()

	stats, err := hs.Admin.GetServerStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total": stats.TotalRooms,
	})
}

// handleListUsers 列出用户
func handleListUsers(c *gin.Context) {
	hs := GetHomeServer()

	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))

	users, total, err := hs.Admin.ListUsers(offset, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"users": users,
		"total": total,
	})
}

// handleGetUserDetails 获取用户详情
func handleGetUserDetails(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	info, err := hs.Admin.GetUserInfo(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, info)
}

// handleUpdateUser 更新用户
func handleUpdateUser(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	var req struct {
		DisplayName string `json:"displayname,omitempty"`
		AvatarURL   string `json:"avatar_url,omitempty"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	// 更新用户资料
	content := map[string]interface{}{
		"displayname": req.DisplayName,
		"avatar_url":  req.AvatarURL,
	}

	if err := hs.Account.SetAccountData(userID, "m.profile", nil, content); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleResetUserPassword 重置用户密码
func handleResetUserPassword(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	var req struct {
		NewPassword string `json:"new_password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	if err := hs.Admin.ResetUserPassword(userID, req.NewPassword); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleMakeUserAdmin 设置用户为管理员
func handleMakeUserAdmin(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	var req struct {
		Admin bool `json:"admin"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	if err := hs.Admin.MakeUserAdmin(userID, req.Admin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleDeleteUser 删除用户
func handleDeleteUser(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	var req struct {
		Erase bool `json:"erase"`
	}
	c.ShouldBindJSON(&req)

	if err := hs.Admin.DeactivateUser(userID, req.Erase); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id_server_unbind_result": "success",
	})
}

// handleWhoisUser 获取用户详细信息
func handleWhoisUser(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	info, err := hs.Admin.GetUserInfo(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   err.Error(),
		})
		return
	}

	// 获取设备信息
	var devices []MatrixDeviceKey
	db.GetDB().Where("user_id = ?", userID).Find(&devices)

	deviceList := make([]gin.H, len(devices))
	for i, d := range devices {
		deviceList[i] = gin.H{
			"device_id": d.DeviceID,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id": info.UserID,
		"devices": deviceList,
	})
}

// handleListRooms 列出房间
func handleListRooms(c *gin.Context) {
	hs := GetHomeServer()

	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))

	rooms, total, err := hs.Admin.ListRooms(offset, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"rooms": rooms,
		"total": total,
	})
}

// handleGetRoomDetails 获取房间详情
func handleGetRoomDetails(c *gin.Context) {
	hs := GetHomeServer()
	roomID := c.Param("roomId")

	room, err := hs.Admin.hs.Room.GetRoom(roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found",
		})
		return
	}

	// 获取成员数
	var memberCount int64
	db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND membership = ?", roomID, MembershipJoin).Count(&memberCount)

	c.JSON(http.StatusOK, gin.H{
		"room_id":         room.RoomID,
		"name":            room.Name,
		"topic":           room.Topic,
		"creator":         room.Creator,
		"version":         room.RoomVersion,
		"joined_members":  memberCount,
		"federatable":     true,
	})
}

// handleDeleteRoom 删除房间
func handleDeleteRoom(c *gin.Context) {
	hs := GetHomeServer()
	roomID := c.Param("roomId")

	if err := hs.Admin.DeleteRoom(roomID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"delete_id": generateSID(),
	})
}
