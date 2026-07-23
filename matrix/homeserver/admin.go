package homeserver

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // G505: reviewed and safe
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// ===== 管理员 API =====
// 服务器统计、用户管理等功能

// AdminService 管理员服务.
type AdminService struct {
	hs *HomeServer
}

// NewAdminService 创建管理员服务.
func NewAdminService(hs *HomeServer) *AdminService {
	return &AdminService{hs: hs}
}

// ServerStats 服务器统计.
type ServerStats struct {
	TotalUsers         int64 `json:"total_users"`
	TotalRooms         int64 `json:"total_rooms"`
	TotalEvents        int64 `json:"total_events"`
	DailyActiveUsers   int64 `json:"daily_active_users"`
	MonthlyActiveUsers int64 `json:"monthly_active_users"`
	OnlineUsers        int64 `json:"online_users"`
}

// GetServerStats 获取服务器统计.
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

// UserInfo 用户信息.
type UserInfo struct {
	UserID        string    `json:"user_id"`
	DisplayName   string    `json:"display_name,omitempty"`
	AvatarURL     string    `json:"avatar_url,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	LastActiveAt  int64     `json:"last_active_at,omitempty"`
	IsAdmin       bool      `json:"is_admin"`
	IsDeactivated bool      `json:"is_deactivated"`
}

// ListUsers 列出用户.
func (as *AdminService) ListUsers(offset, limit int) ([]UserInfo, int64, error) {
	var total int64

	db.GetDB().Model(&MatrixUserSession{}).Distinct("user_id").Count(&total)

	var users []MatrixUserSession
	err := db.GetDB().Distinct("user_id, created_at, last_active_ts").
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&users).Error
	if err != nil {
		return nil, 0, err
	}

	userInfos := make([]UserInfo, len(users))

	for i, u := range users {
		userInfo := UserInfo{
			UserID: u.UserID,
		}
		if u.CreatedAt != nil {
			userInfo.CreatedAt = *u.CreatedAt
		}

		if u.LastActiveTS != nil {
			userInfo.LastActiveAt = *u.LastActiveTS
		}

		userInfos[i] = userInfo
	}

	return userInfos, total, nil
}

// GetUserInfo 获取用户信息.
func (as *AdminService) GetUserInfo(userID string) (*UserInfo, error) {
	// 查找用户会话
	var session MatrixUserSession
	err := db.GetDB().Where("user_id = ?", userID).Order("created_at DESC").First(&session).Error
	if err != nil {
		return nil, errors.New("user not found")
	}

	// 查找用户资料
	var profile MatrixAccountData

	db.GetDB().Where("user_id = ? AND data_type = ? AND room_id IS NULL", userID, "m.profile").First(&profile)

	info := &UserInfo{
		UserID: userID,
	}
	if session.CreatedAt != nil {
		info.CreatedAt = *session.CreatedAt
	}

	if session.LastActiveTS != nil {
		info.LastActiveAt = *session.LastActiveTS
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

// DeactivateUser 停用用户账户.
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

// ResetUserPassword 重置用户密码.
func (as *AdminService) ResetUserPassword(userID, newPassword string) error {
	return as.hs.Account.updatePassword(userID, newPassword)
}

// MakeUserAdmin 设置用户为管理员.
func (as *AdminService) MakeUserAdmin(userID string, admin bool) error {
	// 更新账户数据中的管理员标志
	var accountData MatrixAccountData

	err := db.GetDB().Where("user_id = ? AND data_type = ? AND room_id IS NULL", userID, "m.account_data").First(&accountData).Error

	content := map[string]any{
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

// RoomInfo 房间信息.
type RoomInfo struct {
	RoomID        string    `json:"room_id"`
	Name          string    `json:"name,omitempty"`
	Topic         string    `json:"topic,omitempty"`
	Creator       string    `json:"creator"`
	Version       string    `json:"version"`
	JoinedMembers int       `json:"joined_members"`
	CreatedAt     time.Time `json:"created_at"`
}

// ListRooms 列出房间.
func (as *AdminService) ListRooms(offset, limit int) ([]RoomInfo, int64, error) {
	var total int64

	db.GetDB().Model(&MatrixRoom{}).Count(&total)

	var rooms []MatrixRoom
	err := db.GetDB().Order("created_at DESC").Offset(offset).Limit(limit).Find(&rooms).Error
	if err != nil {
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

// DeleteRoom 删除房间.
func (as *AdminService) DeleteRoom(roomID string) error {
	// 删除房间及其所有数据
	db.GetDB().Where("room_id = ?", roomID).Delete(&MatrixEvent{})
	db.GetDB().Where("room_id = ?", roomID).Delete(&MatrixRoomState{})
	db.GetDB().Where("room_id = ?", roomID).Delete(&MatrixRoomMember{})
	db.GetDB().Where("room_id = ?", roomID).Delete(&MatrixRoom{})

	return nil
}

// ===== HTTP Handlers =====

// RegisterAdminRoutes 注册管理员路由.
func RegisterAdminRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// 管理员 API（需要管理员权限）
	admin := r.Group("/_synapse/admin/v1")
	admin.Use(AdminAuthMiddleware())
	{
		// Server notices (Synapse-compatible admin API)
		admin.POST("/send_server_notice", handleSendServerNotice)
		admin.PUT("/send_server_notice/:txnId", handleSendServerNotice)

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

	// Shared-secret registration (unauthenticated — uses HMAC)
	// Compatible with Complement test suite
	r.GET("/_synapse/admin/v1/register", handleSharedSecretRegisterNonce)
	r.POST("/_synapse/admin/v1/register", handleSharedSecretRegister)
}

// isUserAdmin checks if a Matrix user ID belongs to an admin account.
func isUserAdmin(userID string) bool {
	localpart, _, ok := ParseMatrixUserID(userID)
	if !ok {
		return false
	}

	var user models.User
	err := db.GetDB().Where("username = ?", localpart).First(&user).Error
	if err != nil {
		return false
	}

	return user.IsAdmin
}

// AdminAuthMiddleware 管理员认证中间件.
func AdminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Inline auth logic (do NOT call MatrixAuthMiddleware()(c) because
		// its c.Next() would advance the handler chain prematurely)
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			authHeader = "Bearer " + c.Query("access_token")
		}

		if authHeader == "" || len(authHeader) < 8 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"errcode": "M_MISSING_TOKEN",      //nolint:goconst
				"error":   "Missing access token", //nolint:goconst
			})

			return
		}

		token := authHeader[7:]

		hs := GetHomeServer()
		if hs == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"errcode": "M_UNKNOWN",                //nolint:goconst
				"error":   "Homeserver not available", //nolint:goconst
			})

			return
		}

		session, matrixErr := hs.Auth.GetDeviceByToken(token)
		if matrixErr != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"errcode": "M_UNKNOWN_TOKEN", //nolint:goconst
				"error":   "Invalid access token",
			})

			return
		}

		c.Set("user_id", session.UserID)
		c.Set("device_id", session.DeviceID)

		if !isUserAdmin(session.UserID) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"errcode": "M_FORBIDDEN", //nolint:goconst
				"error":   "You are not a server administrator",
			})

			return
		}

		c.Next()
	}
}

// handleGetServerStats 获取服务器统计.
func handleGetServerStats(c *gin.Context) {
	hs := GetHomeServer()

	stats, err := hs.Admin.GetServerStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR", //nolint:goconst
			"error":   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, stats)
}

// handleGetUserStats 获取用户统计.
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
		"total":  stats.TotalUsers,
		"active": stats.DailyActiveUsers,
		"online": stats.OnlineUsers,
	})
}

// handleGetRoomStats 获取房间统计.
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

// handleListUsers 列出用户.
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
		"users": users, //nolint:goconst
		"total": total,
	})
}

// handleGetUserDetails 获取用户详情.
func handleGetUserDetails(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	info, err := hs.Admin.GetUserInfo(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND", //nolint:goconst
			"error":   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, info)
}

// handleUpdateUser 更新用户.
func handleUpdateUser(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	var req struct {
		DisplayName string `json:"displayname,omitempty"`
		AvatarURL   string `json:"avatar_url,omitempty"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON", //nolint:goconst
			"error":   err.Error(),
		})

		return
	}

	// 更新用户资料
	content := map[string]any{
		"displayname": req.DisplayName,
		"avatar_url":  req.AvatarURL,
	}

	err = hs.Account.SetAccountData(userID, "m.profile", nil, content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleResetUserPassword 重置用户密码.
func handleResetUserPassword(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	var req struct {
		NewPassword string `binding:"required" json:"new_password"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})

		return
	}

	err = hs.Admin.ResetUserPassword(userID, req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleMakeUserAdmin 设置用户为管理员.
func handleMakeUserAdmin(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	var req struct {
		Admin bool `json:"admin"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})

		return
	}

	err = hs.Admin.MakeUserAdmin(userID, req.Admin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleDeleteUser 删除用户.
func handleDeleteUser(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.Param("userId")

	var req struct {
		Erase bool `json:"erase"`
	}

	_ = c.ShouldBindJSON(&req)

	err := hs.Admin.DeactivateUser(userID, req.Erase)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   "internal server error",
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id_server_unbind_result": "success",
	})
}

// handleWhoisUser 获取用户详细信息.
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

// handleListRooms 列出房间.
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

// handleGetRoomDetails 获取房间详情.
func handleGetRoomDetails(c *gin.Context) {
	hs := GetHomeServer()
	roomID := c.Param("roomId")

	room, err := hs.Admin.hs.Room.GetRoom(roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room not found", //nolint:goconst
		})

		return
	}

	// 获取成员数
	var memberCount int64

	db.GetDB().Model(&MatrixRoomMember{}).Where("room_id = ? AND membership = ?", roomID, MembershipJoin).Count(&memberCount)

	c.JSON(http.StatusOK, gin.H{
		"room_id":        room.RoomID, //nolint:goconst
		"name":           room.Name,   //nolint:goconst
		"topic":          room.Topic,
		"creator":        room.Creator,
		"version":        room.RoomVersion,
		"joined_members": memberCount,
		"federatable":    true,
	})
}

// handleDeleteRoom 删除房间.
func handleDeleteRoom(c *gin.Context) {
	hs := GetHomeServer()
	roomID := c.Param("roomId")

	err := hs.Admin.DeleteRoom(roomID)
	if err != nil {
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

// ===== Server Notices (Synapse-compatible admin API) =====

// serverNoticeTxnCache stores txnId → event_id for idempotency.
var serverNoticeTxnCache = struct {
	sync.Mutex

	m map[string]string
}{m: make(map[string]string)}

// handleSendServerNotice implements POST /_synapse/admin/v1/send_server_notice
// It creates a DM room between the server notice bot and the target user, then sends a message.
func handleSendServerNotice(c *gin.Context) {
	// Check txnId idempotency (PUT requests with :txnId)
	txnID := c.Param("txnId")
	if txnID != "" {
		serverNoticeTxnCache.Lock()
		if eventID, ok := serverNoticeTxnCache.m[txnID]; ok {
			serverNoticeTxnCache.Unlock()
			c.JSON(http.StatusOK, gin.H{"event_id": eventID}) //nolint:goconst

			return
		}
		serverNoticeTxnCache.Unlock()
	}

	hs := GetHomeServer()

	var req struct {
		UserID  string         `json:"user_id"`
		Content map[string]any `json:"content"`
	}
	err := c.ShouldBindJSON(&req)

	if err != nil || req.UserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": "Bad JSON or missing user_id"})

		return
	}

	// Server notice bot user ID (Synapse uses @_server:<domain>)
	botUserID := "@_server:" + hs.Config.ServerName

	// Ensure bot user exists (create silently if not)
	botLocalpart := "_server"

	var botSession MatrixUserSession

	if db.GetDB().Where("user_id = ?", botUserID).First(&botSession).Error != nil {
		// Create the bot account via the internal Register method
		_, _ = hs.Auth.Register(&RegisterRequest{
			Username: botLocalpart,
			Password: "notices_bot_" + generateSID(),
		}, false)
	}

	// Find or create a DM room between bot and target user
	roomID := ""
	needsReinvite := false

	var existingMembers []MatrixRoomMember

	db.GetDB().Where("user_id = ? AND membership = ?", botUserID, MembershipJoin).Find(&existingMembers)

	for _, m := range existingMembers {
		var targetMember MatrixRoomMember
		// Check if target has any membership in this room (join, invite, or leave)
		if db.GetDB().Where("room_id = ? AND user_id = ?", m.RoomID, req.UserID).First(&targetMember).Error == nil {
			roomID = m.RoomID

			if targetMember.Membership == MembershipLeave || targetMember.Membership == MembershipBan {
				needsReinvite = true
			}

			break
		}
	}

	if roomID == "" {
		// Create a new DM room
		inviteList := []string{req.UserID}
		isDirect := true

		createResp, err := hs.Room.CreateRoom(botUserID, &CreateRoomRequest{
			Preset:   "private_chat",
			Invite:   inviteList,
			IsDirect: isDirect,
			CreationContent: map[string]any{
				"m.federate": false,
			},
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"errcode": "M_INTERNAL", "error": "Failed to create notice room"}) //nolint:goconst

			return
		}

		roomID = createResp.RoomID
	}

	// Re-invite if the target user previously left
	if needsReinvite {
		_ = hs.Room.InviteUser(roomID, botUserID, &InviteUserRequest{UserID: req.UserID})
	}

	// Send the notice message
	content := req.Content
	if content == nil {
		content = map[string]any{"msgtype": "m.text", "body": "Server notice"} //nolint:goconst
	}

	resp, matErr := hs.Event.SendRoomEvent(roomID, botUserID, "m.room.message", nil, content, nil)
	if matErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"errcode": "M_INTERNAL", "error": "Failed to send notice"})

		return
	}

	// Cache txnId → event_id for idempotency
	if txnID != "" {
		serverNoticeTxnCache.Lock()
		serverNoticeTxnCache.m[txnID] = resp.EventID
		serverNoticeTxnCache.Unlock()
	}

	c.JSON(http.StatusOK, gin.H{"event_id": resp.EventID})
}

// ===== Shared-Secret Registration (Complement compatible) =====

const sharedRegistrationSecret = "complement"

var (
	nonceStore = make(map[string]time.Time)
	nonceMu    sync.Mutex
)

// handleSharedSecretRegisterNonce returns a nonce for shared-secret registration.
func handleSharedSecretRegisterNonce(c *gin.Context) {
	nonce := generateSID() + generateSID()

	nonceMu.Lock()
	nonceStore[nonce] = time.Now()
	// Clean up old nonces (> 5 minutes)
	for k, v := range nonceStore {
		if time.Since(v) > 5*time.Minute {
			delete(nonceStore, k)
		}
	}
	nonceMu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"nonce": nonce,
	})
}

// handleSharedSecretRegister handles shared-secret admin registration.
func handleSharedSecretRegister(c *gin.Context) {
	var req struct {
		Nonce    string `json:"nonce"`
		Username string `json:"username"`
		Password string `json:"password"`
		Admin    bool   `json:"admin"`
		Mac      string `json:"mac"`
	}
	err := c.ShouldBindJSON(&req)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_BAD_JSON", "error": "Bad JSON"}) //nolint:goconst

		return
	}

	// Validate nonce
	nonceMu.Lock()

	ts, ok := nonceStore[req.Nonce]
	if ok {
		delete(nonceStore, req.Nonce)
	}
	nonceMu.Unlock()

	if !ok || time.Since(ts) > 5*time.Minute {
		c.JSON(http.StatusBadRequest, gin.H{"errcode": "M_UNKNOWN", "error": "Invalid or expired nonce"})

		return
	}

	// Verify HMAC: HMAC-SHA1(key=secret, message=nonce\x00username\x00password\x00admin|notadmin)
	adminStr := "notadmin"
	if req.Admin {
		adminStr = "admin"
	}

	mac := hmac.New(sha1.New, []byte(sharedRegistrationSecret))
	mac.Write([]byte(req.Nonce))
	mac.Write([]byte{0})
	mac.Write([]byte(req.Username))
	mac.Write([]byte{0})
	mac.Write([]byte(req.Password))
	mac.Write([]byte{0})
	mac.Write([]byte(adminStr))
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(req.Mac), []byte(expectedMAC)) {
		c.JSON(http.StatusForbidden, gin.H{"errcode": "M_FORBIDDEN", "error": "HMAC mismatch"})

		return
	}

	hs := GetHomeServer()

	resp, regErr := hs.Auth.Register(&RegisterRequest{
		Username: req.Username,
		Password: req.Password,
		Auth:     &UIAuthRequest{Type: "m.login.dummy"},
	}, true)
	if regErr != nil {
		c.JSON(http.StatusBadRequest, regErr)

		return
	}

	c.JSON(http.StatusOK, resp)
}
