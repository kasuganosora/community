package homeserver

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
)

// ===== MSC1960: OpenID Connect 集成 =====
// 允许用户使用 Matrix 身份登录第三方服务

// OpenIDToken OpenID 令牌
type OpenIDToken struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	Token     string    `gorm:"uniqueIndex;type:varchar(128);not null"`
	UserID    string    `gorm:"index;type:varchar(255);not null"`
	ExpiresAt time.Time `gorm:"not null"`
	Used      bool      `gorm:"default:false"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

func (OpenIDToken) TableName() string {
	return "matrix_openid_tokens"
}

// OpenIDTokenRequest OpenID 令牌请求
type OpenIDTokenRequest struct {
	// 可选的过期时间（毫秒）
	ExpiresIn int64 `json:"expires_in,omitempty"`
}

// OpenIDTokenResponse OpenID 令牌响应
type OpenIDTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	MatrixServerName string `json:"matrix_server_name"`
	ExpiresIn        int64  `json:"expires_in,omitempty"`
}

// OpenIDUserInfo OpenID 用户信息
type OpenIDUserInfo struct {
	Sub string `json:"sub"` // Matrix 用户 ID
}

// OpenIDService OpenID 服务
type OpenIDService struct {
	hs *HomeServer
}

// NewOpenIDService 创建 OpenID 服务
func NewOpenIDService(hs *HomeServer) *OpenIDService {
	s := &OpenIDService{hs: hs}
	go s.cleanupLoop()
	return s
}

// cleanupLoop 定期清理过期的令牌
func (os *OpenIDService) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		os.CleanupExpiredTokens()
	}
}

// RequestToken 请求 OpenID 令牌
func (os *OpenIDService) RequestToken(userID string, req *OpenIDTokenRequest) (*OpenIDTokenResponse, error) {
	// 生成令牌
	token := generateOpenIDToken()

	// 计算过期时间
	expiresIn := int64(3600) // 默认 1 小时
	if req.ExpiresIn > 0 && req.ExpiresIn <= 86400 {
		expiresIn = req.ExpiresIn
	}

	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)

	// 保存令牌
	openIDToken := &OpenIDToken{
		Token:     token,
		UserID:    userID,
		ExpiresAt: expiresAt,
	}

	if err := db.GetDB().Create(openIDToken).Error; err != nil {
		return nil, err
	}

	return &OpenIDTokenResponse{
		AccessToken:      token,
		TokenType:        "Bearer",
		MatrixServerName: os.hs.Config.ServerName,
		ExpiresIn:        expiresIn,
	}, nil
}

// GetUserInfo 获取 OpenID 用户信息
func (os *OpenIDService) GetUserInfo(token string) (*OpenIDUserInfo, error) {
	// 查找令牌
	var openIDToken OpenIDToken
	if token == "" {
		return nil, fmt.Errorf("empty token")
	}
	if err := db.GetDB().Where("token = ? AND used = ? AND expires_at > ?",
		token, false, time.Now()).First(&openIDToken).Error; err != nil {
		return nil, fmt.Errorf("invalid or expired token")
	}

	// 可选：标记令牌为已使用（一次性令牌）
	// openIDToken.Used = true
	// db.GetDB().Save(&openIDToken)

	return &OpenIDUserInfo{
		Sub: openIDToken.UserID,
	}, nil
}

// CleanupExpiredTokens 清理过期的令牌
func (os *OpenIDService) CleanupExpiredTokens() {
	db.GetDB().Where("expires_at < ? OR used = ?", time.Now(), true).Delete(&OpenIDToken{})
}

// ===== HTTP Handlers =====

// RegisterOpenIDRoutes 注册 OpenID 路由
func RegisterOpenIDRoutes(r gin.IRouter) {
	hs := GetHomeServer()
	if hs == nil || !hs.Config.Enabled {
		return
	}

	// OpenID 令牌请求（需要认证）
	api := r.Group("/_matrix/client/v3")
	api.Use(MatrixAuthMiddleware())
	{
		api.POST("/user/:userId/openid/request_token", handleRequestOpenIDToken)
	}

	// OpenID 用户信息（使用 OpenID 令牌）
	r.GET("/_matrix/federation/v1/openid/userinfo", handleGetOpenIDUserInfo)
	r.GET("/_matrix/client/v3/openid/userinfo", handleGetOpenIDUserInfo)
}

// handleRequestOpenIDToken 处理 OpenID 令牌请求
func handleRequestOpenIDToken(c *gin.Context) {
	hs := GetHomeServer()
	userID := c.GetString("user_id")
	paramUserID := c.Param("userId")

	// 验证只能请求自己的令牌
	if paramUserID != userID {
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN",
			"error":   "Cannot request token for another user",
		})
		return
	}

	var req OpenIDTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 使用默认值
		req.ExpiresIn = 3600
	}

	resp, err := hs.OpenID.RequestToken(userID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL_ERROR",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// handleGetOpenIDUserInfo 处理 OpenID 用户信息请求
func handleGetOpenIDUserInfo(c *gin.Context) {
	hs := GetHomeServer()

	// 从 Authorization 头获取令牌
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" || len(authHeader) < 8 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"errcode": "M_MISSING_TOKEN",
			"error":   "Missing access token",
		})
		return
	}

	token := authHeader[7:] // 去掉 "Bearer "

	userInfo, err := hs.OpenID.GetUserInfo(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"errcode": "M_UNKNOWN_TOKEN",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, userInfo)
}

// generateOpenIDToken 生成 OpenID 令牌
func generateOpenIDToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 回退到基于时间的令牌
		return fmt.Sprintf("openid_%x", time.Now().UnixNano())
	}
	return "openid_" + hex.EncodeToString(b)
}
