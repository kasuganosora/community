package homeserver

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
)

// ===== 第三方标识符 (3PID) 管理 =====
// 邮箱和手机号绑定

// ThreePID 第三方标识符
type ThreePID struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	UserID      string `gorm:"index;type:varchar(255);not null"`
	Medium      string `gorm:"index;type:varchar(16);not null"`  // email, msisdn
	Address     string `gorm:"index;type:varchar(255);not null"` // 邮箱或手机号
	ValidatedAt int64  `json:"validated_at"`
	AddedAt     int64  `json:"added_at"`
	Validated   bool   `gorm:"default:false"`
}

func (ThreePID) TableName() string {
	return "matrix_threepids"
}

// ThreePIDToken 3PID 验证令牌
type ThreePIDToken struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	Token     string    `gorm:"uniqueIndex;type:varchar(128);not null"`
	Medium    string    `gorm:"type:varchar(16);not null"`
	Address   string    `gorm:"type:varchar(255);not null"`
	UserID    string    `gorm:"type:varchar(255)"`
	ExpiresAt time.Time `gorm:"not null"`
	Used      bool      `gorm:"default:false"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

func (ThreePIDToken) TableName() string {
	return "matrix_threepid_tokens"
}

// ThreePIDService 3PID 服务
type ThreePIDService struct {
	hs *HomeServer
}

// NewThreePIDService 创建 3PID 服务
func NewThreePIDService(hs *HomeServer) *ThreePIDService {
	return &ThreePIDService{hs: hs}
}

// GetThreePIDs 获取用户的所有 3PID
func (ts *ThreePIDService) GetThreePIDs(userID string) ([]ThreePID, error) {
	var threepids []ThreePID
	if err := db.GetDB().Where("user_id = ? AND validated = ?", userID, true).Find(&threepids).Error; err != nil {
		return nil, err
	}
	return threepids, nil
}

// AddThreePID 添加 3PID
func (ts *ThreePIDService) AddThreePID(userID, medium, address string) error {
	// 检查是否已存在
	var existing ThreePID
	if err := db.GetDB().Where("medium = ? AND address = ? AND validated = ?", medium, address, true).First(&existing).Error; err == nil {
		if existing.UserID != userID {
			return fmt.Errorf("3pid already in use by another user")
		}
		return nil // 已存在
	}

	// 创建新的 3PID 记录
	threepid := &ThreePID{
		UserID:      userID,
		Medium:      medium,
		Address:     address,
		ValidatedAt: time.Now().UnixMilli(),
		AddedAt:     time.Now().UnixMilli(),
		Validated:   true, // 简化实现，实际应该验证
	}

	return db.GetDB().Create(threepid).Error
}

// DeleteThreePID 删除 3PID
func (ts *ThreePIDService) DeleteThreePID(userID, medium, address string) error {
	return db.GetDB().Where("user_id = ? AND medium = ? AND address = ?", userID, medium, address).Delete(&ThreePID{}).Error
}

// RequestValidationToken 请求验证令牌
func (ts *ThreePIDService) RequestValidationToken(medium, address, clientSecret string, sendAttempt int) (string, error) {
	// 生成验证令牌
	token := generateValidationToken()

	// 保存令牌
	tokenRecord := &ThreePIDToken{
		Token:     token,
		Medium:    medium,
		Address:   address,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	if err := db.GetDB().Create(tokenRecord).Error; err != nil {
		return "", err
	}

	// 发送验证邮件/短信
	switch medium {
	case "email":
		go ts.sendValidationEmail(address, token)
	case "msisdn":
		go ts.sendValidationSMS(address, token)
	}

	// 返回 sid
	sid := generateSID()
	return sid, nil
}

// ValidateThreePID 验证 3PID
func (ts *ThreePIDService) ValidateThreePID(token, medium, address string) (bool, error) {
	// 查找令牌
	var tokenRecord ThreePIDToken
	if err := db.GetDB().Where("token = ? AND medium = ? AND address = ? AND used = ? AND expires_at > ?",
		token, medium, address, false, time.Now()).First(&tokenRecord).Error; err != nil {
		return false, fmt.Errorf("invalid or expired token")
	}

	// 标记令牌为已使用
	tokenRecord.Used = true
	db.GetDB().Save(&tokenRecord)

	return true, nil
}

// LookupThreePID 查找 3PID 对应的用户
func (ts *ThreePIDService) LookupThreePID(medium, address string) (string, error) {
	var threepid ThreePID
	if err := db.GetDB().Where("medium = ? AND address = ? AND validated = ?", medium, address, true).First(&threepid).Error; err != nil {
		return "", fmt.Errorf("3pid not found")
	}
	return threepid.UserID, nil
}

// sendValidationEmail 发送验证邮件
func (ts *ThreePIDService) sendValidationEmail(email, token string) {
	// TODO: 实现邮件发送
	log.Info("Would send validation email to %s with token %s", email, token)
}

// sendValidationSMS 发送验证短信
func (ts *ThreePIDService) sendValidationSMS(phone, token string) {
	// TODO: 实现短信发送
	log.Info("Would send validation SMS to %s with token %s", phone, token)
}

// UnbindThreePID 解绑 3PID
func (ts *ThreePIDService) UnbindThreePID(userID, medium, address string) error {
	return db.GetDB().Where("user_id = ? AND medium = ? AND address = ?", userID, medium, address).Delete(&ThreePID{}).Error
}

// ===== HTTP Handlers =====

// RegisterThreePIDRoutes 注册 3PID 路由
func RegisterThreePIDRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// 3PID 管理（需要认证）
	api := r.Group("/_matrix/client/v3")
	api.Use(MatrixAuthMiddleware())
	{
		api.GET("/account/3pid", handleGetThreePIDs)
		api.POST("/account/3pid", handleAddThreePID)
		api.POST("/account/3pid/delete", handleDeleteThreePID)
	}

	// 3PID 验证（无需认证）
	r.POST("/_matrix/client/v3/register/email/requestToken", handleRequestEmailToken)
	r.POST("/_matrix/client/v3/register/msisdn/requestToken", handleRequestMSISDNToken)
	r.POST("/_matrix/client/v3/register/email/submitToken", handleSubmitEmailToken)

	// 查找 API
	r.GET("/_matrix/identity/v2/lookup", handleLookupThreePID)
}

// handleGetThreePIDs 获取用户的 3PID 列表
func handleGetThreePIDs(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	threepids, err := hs.ThreePID.GetThreePIDs(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	// 格式化响应
	type ThreePIDResponse struct {
		Medium      string `json:"medium"`
		Address     string `json:"address"`
		ValidatedAt int64  `json:"validated_at"`
		AddedAt     int64  `json:"added_at"`
	}

	response := make([]ThreePIDResponse, len(threepids))
	for i, tp := range threepids {
		response[i] = ThreePIDResponse{
			Medium:      tp.Medium,
			Address:     tp.Address,
			ValidatedAt: tp.ValidatedAt,
			AddedAt:     tp.AddedAt,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"threepids": response,
	})
}

// handleAddThreePID 添加 3PID
func handleAddThreePID(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	var req struct {
		Medium  string `json:"medium" binding:"required"`
		Address string `json:"address" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	// 验证 3PID
	if req.Medium != "email" && req.Medium != "msisdn" {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_INVALID_PARAM",
			"error":   "Invalid medium",
		})
		return
	}

	if err := hs.ThreePID.AddThreePID(userID, req.Medium, req.Address); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_THREEPID_IN_USE",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleDeleteThreePID 删除 3PID
func handleDeleteThreePID(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")

	var req struct {
		Medium  string `json:"medium" binding:"required"`
		Address string `json:"address" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	if err := hs.ThreePID.DeleteThreePID(userID, req.Medium, req.Address); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleRequestEmailToken 请求邮箱验证令牌
func handleRequestEmailToken(c *gin.Context) {
	hs := GetHomeServer()

	var req struct {
		ClientSecret string `json:"client_secret" binding:"required"`
		Email        string `json:"email" binding:"required"`
		SendAttempt  int    `json:"send_attempt"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	sid, err := hs.ThreePID.RequestValidationToken("email", req.Email, req.ClientSecret, req.SendAttempt)
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

// handleRequestMSISDNToken 请求手机号验证令牌
func handleRequestMSISDNToken(c *gin.Context) {
	hs := GetHomeServer()

	var req struct {
		ClientSecret string `json:"client_secret" binding:"required"`
		Phone        string `json:"phone_number" binding:"required"`
		Country      string `json:"country"`
		SendAttempt  int    `json:"send_attempt"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	// 规范化手机号
	phone := req.Phone
	if !strings.HasPrefix(phone, "+") && req.Country != "" {
		// 添加国家码
		phone = "+" + req.Country + phone
	}

	sid, err := hs.ThreePID.RequestValidationToken("msisdn", phone, req.ClientSecret, req.SendAttempt)
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

// handleSubmitEmailToken 提交邮箱验证令牌
func handleSubmitEmailToken(c *gin.Context) {
	hs := GetHomeServer()

	var req struct {
		Token string `json:"token" binding:"required"`
		Email string `json:"email" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   err.Error(),
		})
		return
	}

	valid, err := hs.ThreePID.ValidateThreePID(req.Token, "email", req.Email)
	if err != nil || !valid {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_INVALID_PARAM",
			"error":   "Invalid token",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleLookupThreePID 查找 3PID
func handleLookupThreePID(c *gin.Context) {
	hs := GetHomeServer()

	medium := c.Query("medium")
	address := c.Query("address")

	if medium == "" || address == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_MISSING_PARAMS",
			"error":   "Missing medium or address",
		})
		return
	}

	userID, err := hs.ThreePID.LookupThreePID(medium, address)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "3PID not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"mxid": userID,
	})
}

// 辅助函数
func generateValidationToken() string {
	return generateOpenIDToken()
}
