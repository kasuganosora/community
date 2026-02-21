package homeserver

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
)

// ===== 账户管理实现 =====

// PasswordResetRequest 密码重置请求
type PasswordResetRequest struct {
	ClientSecret string `json:"client_secret" binding:"required"`
	Email        string `json:"email,omitempty"`
	Phone        string `json:"phone,omitempty"`
	SendAttempt  int    `json:"send_attempt"`
}

// PasswordResetToken 密码重置令牌
type PasswordResetToken struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement"`
	Token       string    `gorm:"uniqueIndex;type:varchar(128);not null"`
	UserID      string    `gorm:"index;type:varchar(255);not null"`
	Email       string    `gorm:"type:varchar(255)"`
	ExpiresAt   time.Time `gorm:"not null"`
	Used        bool      `gorm:"default:false"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
}

func (PasswordResetToken) TableName() string {
	return "matrix_password_reset_tokens"
}

// DeactivateRequest 账户停用请求
type DeactivateRequest struct {
	Auth *DeactivateAuth `json:"auth,omitempty"`
}

// DeactivateAuth 停用认证
type DeactivateAuth struct {
	Type     string `json:"type"`
	Password *struct {
		Password string `json:"password"`
	} `json:"password,omitempty"`
}

// PasswordAuthFlow 密码认证流程
type PasswordAuthFlow struct {
	Type     string `json:"type"`
	Password *struct {
		Password string `json:"password"`
	} `json:"password,omitempty"`
}

// AccountData 账户数据
type AccountData struct {
	Type    string                 `json:"type"`
	Content map[string]interface{} `json:"content"`
}

// AccountService 账户服务
type AccountService struct {
	hs *HomeServer
}

// NewAccountService 创建账户服务
func NewAccountService(hs *HomeServer) *AccountService {
	return &AccountService{hs: hs}
}

// RequestPasswordReset 请求密码重置
func (as *AccountService) RequestPasswordReset(req *PasswordResetRequest) (string, error) {
	// 查找用户
	var account models.Account
	if req.Email != "" {
		if err := db.GetDB().Where("email = ?", req.Email).First(&account).Error; err != nil {
			// 为了安全，不泄露邮箱是否存在
			return "", nil
		}
	} else {
		return "", fmt.Errorf("email required")
	}

	// 生成重置令牌
	token := generateResetToken()

	// 创建重置记录
	resetToken := &PasswordResetToken{
		Token:     token,
		UserID:    fmt.Sprintf("@%s:%s", account.Username, as.hs.Config.ServerName),
		Email:     req.Email,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	if err := db.GetDB().Create(resetToken).Error; err != nil {
		return "", err
	}

	// TODO: 发送邮件或短信
	userID := fmt.Sprintf("@%s:%s", account.Username, as.hs.Config.ServerName)
	log.Info("Password reset token generated for user %s", userID)

	// 返回 sid（简化实现）
	sid := generateSID()
	return sid, nil
}

// ResetPassword 重置密码
func (as *AccountService) ResetPassword(token, newPassword string) error {
	// 查找令牌
	var resetToken PasswordResetToken
	if err := db.GetDB().Where("token = ? AND used = ? AND expires_at > ?", 
		token, false, time.Now()).First(&resetToken).Error; err != nil {
		return fmt.Errorf("invalid or expired token")
	}

	// 更新密码
	if err := as.updatePassword(resetToken.UserID, newPassword); err != nil {
		return err
	}

	// 标记令牌为已使用
	resetToken.Used = true
	db.GetDB().Save(&resetToken)

	// 撤销所有现有会话
	as.revokeAllSessions(resetToken.UserID)

	return nil
}

// updatePassword 更新用户密码
func (as *AccountService) updatePassword(userID, newPassword string) error {
	// 查找本地账户
	var account models.Account
	if err := db.GetDB().Where("email = ? OR username = ?", userID, strings.TrimPrefix(userID, "@")).First(&account).Error; err != nil {
		return fmt.Errorf("user not found")
	}

	// 简化实现，实际应该哈希密码
	_ = newPassword
	// 更新密码 - 这里简化处理
	return db.GetDB().Model(&account).Update("encrypted_password", newPassword).Error
}

// revokeAllSessions 撤销用户的所有会话
func (as *AccountService) revokeAllSessions(userID string) {
	db.GetDB().Where("user_id = ?", userID).Delete(&MatrixUserSession{})
}

// DeactivateAccount 停用账户
func (as *AccountService) DeactivateAccount(userID string, req *DeactivateRequest) error {
	// 验证身份（简化实现）
	if req.Auth != nil {
		// 应该进行交互式认证
	}

	// 获取用户加入的所有房间
	var memberships []MatrixRoomMember
	db.GetDB().Where("user_id = ? AND membership = ?", userID, MembershipJoin).Find(&memberships)

	// 离开所有房间
	for _, m := range memberships {
		as.hs.Room.LeaveRoom(userID, m.RoomID, &LeaveRoomRequest{
			Reason: "User account deactivated",
		})
	}

	// 删除用户会话
	as.revokeAllSessions(userID)

	// 标记账户为已停用
	// 这里简化处理，实际应该有一个字段标记账户状态

	log.Info("Account deactivated: %s", userID)
	return nil
}

// GetAccountData 获取账户数据
func (as *AccountService) GetAccountData(userID, dataType string, roomID *string) (*AccountData, error) {
	query := db.GetDB().Where("user_id = ? AND data_type = ?", userID, dataType)
	if roomID != nil {
		query = query.Where("room_id = ?", *roomID)
	} else {
		query = query.Where("room_id IS NULL")
	}

	var data MatrixAccountData
	if err := query.First(&data).Error; err != nil {
		return nil, err
	}

	return &AccountData{
		Type:    data.DataType,
		Content: data.Content,
	}, nil
}

// SetAccountData 设置账户数据
func (as *AccountService) SetAccountData(userID, dataType string, roomID *string, content map[string]interface{}) error {
	// 查找现有数据
	var existing MatrixAccountData
	query := db.GetDB().Where("user_id = ? AND data_type = ?", userID, dataType)
	if roomID != nil {
		query = query.Where("room_id = ?", *roomID)
	} else {
		query = query.Where("room_id IS NULL")
	}

	result := query.First(&existing)

	if result.Error == nil {
		// 更新
		existing.Content = content
		return db.GetDB().Save(&existing).Error
	}

	// 创建新记录
	newData := &MatrixAccountData{
		UserID:   userID,
		DataType: dataType,
		Content:  content,
	}
	if roomID != nil {
		newData.RoomID = roomID
	}

	return db.GetDB().Create(newData).Error
}

// Whoami 获取当前用户信息
func (as *AccountService) Whoami(userID string) (*WhoamiResponse, error) {
	// 查找用户
	var account models.Account
	if err := db.GetDB().Where("matrix_user_id = ?", userID).First(&account).Error; err != nil {
		return nil, fmt.Errorf("user not found")
	}

	return &WhoamiResponse{
		UserID: userID,
	}, nil
}

// WhoamiResponse 用户信息响应
type WhoamiResponse struct {
	UserID string `json:"user_id"`
}

// ChangePassword 修改密码
func (as *AccountService) ChangePassword(userID, oldPassword, newPassword string) error {
	// 简化实现，实际应该验证旧密码
	_ = oldPassword
	// 更新密码
	return as.updatePassword(userID, newPassword)
}

// ===== HTTP Handlers =====

// RegisterAccountRoutes 注册账户路由
func RegisterAccountRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	account := r.Group("/_matrix/client/v3")
	{
		// 账户信息
		account.GET("/account/whoami", MatrixAuthMiddleware(), handleWhoami)
		
		// 密码管理
		account.POST("/account/password", MatrixAuthMiddleware(), handleChangePassword)
		account.POST("/account/password/email/requestToken", handleRequestPasswordReset)
		
		// 账户停用
		account.POST("/account/deactivate", MatrixAuthMiddleware(), handleDeactivate)
		
		// 账户数据
		account.GET("/user/:userId/account_data/:type", MatrixAuthMiddleware(), handleGetAccountData)
		account.PUT("/user/:userId/account_data/:type", MatrixAuthMiddleware(), handleSetAccountData)
		account.GET("/user/:userId/rooms/:roomId/account_data/:type", MatrixAuthMiddleware(), handleGetRoomAccountData)
		account.PUT("/user/:userId/rooms/:roomId/account_data/:type", MatrixAuthMiddleware(), handleSetRoomAccountData)
	}
}

// handleWhoami 获取当前用户信息
func handleWhoami(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	resp, err := hs.Account.Whoami(userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// handleChangePassword 修改密码
func handleChangePassword(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	var req struct {
		Auth        *PasswordAuthFlow `json:"auth,omitempty"`
		NewPassword string            `json:"new_password" binding:"required"`
		LogoutDevices bool            `json:"logout_devices,omitempty"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	// 如果没有 auth，启动交互式认证
	if req.Auth == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"session": generateSessionID(),
			"flows": []gin.H{
				{"stages": []string{"m.login.password"}},
			},
		})
		return
	}

	// 验证当前密码
	if req.Auth.Type == "m.login.password" && req.Auth.Password != nil {
		if err := hs.Account.ChangePassword(userID, req.Auth.Password.Password, req.NewPassword); err != nil {
			c.JSON(http.StatusForbidden, gin.H{
				"errcode": "M_FORBIDDEN",
				"error":   err.Error(),
			})
			return
		}
	}

	// 如果请求，登出所有设备
	if req.LogoutDevices {
		hs.Account.revokeAllSessions(userID)
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleRequestPasswordReset 请求密码重置
func handleRequestPasswordReset(c *gin.Context) {
	hs := GetHomeServer()

	var req PasswordResetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	sid, err := hs.Account.RequestPasswordReset(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"sid": sid,
	})
}

// handleDeactivate 停用账户
func handleDeactivate(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	var req DeactivateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	if err := hs.Account.DeactivateAccount(userID, &req); err != nil {
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

// handleGetAccountData 获取账户数据
func handleGetAccountData(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	dataType := c.Param("type")

	// 验证用户只能访问自己的数据
	paramUserID := c.Param("userId")
	if paramUserID != userID {
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN",
			"error":   "Cannot access another user's account data",
		})
		return
	}

	data, err := hs.Account.GetAccountData(userID, dataType, nil)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Account data not found",
		})
		return
	}

	c.JSON(http.StatusOK, data.Content)
}

// handleSetAccountData 设置账户数据
func handleSetAccountData(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	dataType := c.Param("type")

	// 验证用户只能设置自己的数据
	paramUserID := c.Param("userId")
	if paramUserID != userID {
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN",
			"error":   "Cannot set another user's account data",
		})
		return
	}

	var content map[string]interface{}
	if err := c.ShouldBindJSON(&content); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	if err := hs.Account.SetAccountData(userID, dataType, nil, content); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleGetRoomAccountData 获取房间账户数据
func handleGetRoomAccountData(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	roomID := c.Param("roomId")
	dataType := c.Param("type")

	paramUserID := c.Param("userId")
	if paramUserID != userID {
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN",
			"error":   "Cannot access another user's account data",
		})
		return
	}

	data, err := hs.Account.GetAccountData(userID, dataType, &roomID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Account data not found",
		})
		return
	}

	c.JSON(http.StatusOK, data.Content)
}

// handleSetRoomAccountData 设置房间账户数据
func handleSetRoomAccountData(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	roomID := c.Param("roomId")
	dataType := c.Param("type")

	paramUserID := c.Param("userId")
	if paramUserID != userID {
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN",
			"error":   "Cannot set another user's account data",
		})
		return
	}

	var content map[string]interface{}
	if err := c.ShouldBindJSON(&content); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	if err := hs.Account.SetAccountData(userID, dataType, &roomID, content); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// ===== 辅助函数 =====






func generateSID() string {
	return uuid.New().String()
}

func generateResetToken() string {
	return uuid.New().String() + uuid.New().String()
}
