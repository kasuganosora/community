package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/matrix"
	"github.com/kasuganosora/community/migrate/scripts"
)

// MatrixBridgeStatus Matrix Bridge 状态响应
type MatrixBridgeStatus struct {
	Enabled     bool   `json:"enabled"`
	Running     bool   `json:"running"`
	Homeserver  string `json:"homeserver,omitempty"`
	ServerName  string `json:"server_name,omitempty"`
	Connected   bool   `json:"connected"`
	Message     string `json:"message"`
}

// GetMatrixStatus 获取 Matrix Bridge 状态
func GetMatrixStatus(c *gin.Context) {
	matrix.LoadConfig()
	cfg := &matrix.GlobalConfig

	status := MatrixBridgeStatus{
		Enabled:    cfg.IsEnabled(),
		Homeserver: cfg.HomeserverURL,
		ServerName: cfg.ServerName,
	}

	if !cfg.IsEnabled() {
		status.Message = "Matrix Bridge is not configured or disabled"
		c.JSON(http.StatusOK, status)
		return
	}

	// 检查连接状态
	client := matrix.NewASClient()
	if err := client.PingHS(); err != nil {
		status.Connected = false
		status.Message = "Failed to connect to Homeserver: " + err.Error()
	} else {
		status.Connected = true
		status.Message = "Connected to Homeserver"
	}

	c.JSON(http.StatusOK, status)
}

// StartMatrixBridge 启动 Matrix Bridge
func StartMatrixBridge(c *gin.Context) {
	matrix.LoadConfig()
	cfg := &matrix.GlobalConfig

	if !cfg.IsEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Matrix Bridge is not configured or disabled",
		})
		return
	}

	// 这里可以集成全局 Bridge 实例的启动逻辑
	// 实际实现中，Bridge 应该在应用启动时初始化

	c.JSON(http.StatusOK, gin.H{
		"message": "Matrix Bridge start request received",
	})
}

// StopMatrixBridge 停止 Matrix Bridge
func StopMatrixBridge(c *gin.Context) {
	// 停止 Bridge 的逻辑
	c.JSON(http.StatusOK, gin.H{
		"message": "Matrix Bridge stop request received",
	})
}

// RunMatrixMigration 运行 Matrix 数据库迁移
func RunMatrixMigration(c *gin.Context) {
	if err := scripts.MigrateMatrixTables(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to run Matrix migration: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Matrix tables migration completed successfully",
	})
}

// GetMatrixConfig 获取 Matrix 配置（不含敏感信息）
func GetMatrixConfig(c *gin.Context) {
	matrix.LoadConfig()
	cfg := &matrix.GlobalConfig

	c.JSON(http.StatusOK, gin.H{
		"enabled":              cfg.Enabled,
		"homeserver_url":       cfg.HomeserverURL,
		"server_name":          cfg.ServerName,
		"bridge_username":      cfg.BridgeUsername,
		"user_namespace":       cfg.UserNamespacePrefix,
		"room_namespace":       cfg.RoomNamespacePrefix,
		// 不返回敏感的 token
	})
}

// MatrixTestRequest 测试请求
type MatrixTestRequest struct {
	MatrixUserID string `json:"matrix_user_id" binding:"required"`
}

// TestMatrixConnection 测试与 Matrix 用户的连接
func TestMatrixConnection(c *gin.Context) {
	var req MatrixTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	matrix.LoadConfig()
	cfg := &matrix.GlobalConfig

	if !cfg.IsEnabled() {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Matrix Bridge is not enabled",
		})
		return
	}

	// 测试获取用户信息
	client := matrix.NewClient()
	profile, err := client.GetUserID(req.MatrixUserID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   "Failed to get user profile: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"display_name": profile.DisplayName,
		"avatar_url":  profile.AvatarURL,
	})
}
