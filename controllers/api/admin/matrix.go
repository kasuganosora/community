package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/matrix"
	"github.com/kasuganosora/community/migrate/scripts"
)

// MatrixBridgeStatus Matrix Bridge 状态响应.
type MatrixBridgeStatus struct {
	Enabled    bool   `json:"enabled"`
	Running    bool   `json:"running"`
	Homeserver string `json:"homeserver,omitempty"`
	ServerName string `json:"server_name,omitempty"`
	Connected  bool   `json:"connected"`
	Message    string `json:"message"`
}

// GetMatrixStatus 获取 Matrix Bridge 状态.
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
	err := client.PingHS()
	if err != nil {
		log.Error("failed to connect to Homeserver: %v", err)

		status.Connected = false
		status.Message = "Failed to connect to Homeserver"
	} else {
		status.Connected = true
		status.Message = "Connected to Homeserver"
	}

	c.JSON(http.StatusOK, status)
}

// StartMatrixBridge 启动 Matrix Bridge.
func StartMatrixBridge(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "bridge lifecycle is managed by the server process"})
}
func StopMatrixBridge(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "bridge lifecycle is managed by the server process"})
}

// RunMatrixMigration 运行 Matrix 数据库迁移.
func RunMatrixMigration(c *gin.Context) {
	err := scripts.MigrateMatrixTables()
	if err != nil {
		log.Error("failed to run Matrix migration: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "operation failed",
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Matrix tables migration completed successfully",
	})
}

// GetMatrixConfig 获取 Matrix 配置（不含敏感信息）.
func GetMatrixConfig(c *gin.Context) {
	matrix.LoadConfig()

	cfg := &matrix.GlobalConfig

	c.JSON(http.StatusOK, gin.H{
		"enabled":         cfg.Enabled,
		"homeserver_url":  cfg.HomeserverURL,
		"server_name":     cfg.ServerName,
		"bridge_username": cfg.BridgeUsername,
		"user_namespace":  cfg.UserNamespacePrefix,
		"room_namespace":  cfg.RoomNamespacePrefix,
		// 不返回敏感的 token
	})
}

// MatrixTestRequest 测试请求.
type MatrixTestRequest struct {
	MatrixUserID string `binding:"required" json:"matrix_user_id"`
}

// TestMatrixConnection 测试与 Matrix 用户的连接.
func TestMatrixConnection(c *gin.Context) {
	var req MatrixTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request", //nolint:goconst
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
		log.Error("failed to get user profile: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"success": false, //nolint:goconst
			"error":   "operation failed",
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"display_name": profile.DisplayName, //nolint:goconst
		"avatar_url":   profile.AvatarURL,
	})
}
