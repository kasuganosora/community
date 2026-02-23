package matrix

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/log"
)

// WebhookHandler Matrix AS Webhook 处理器
type WebhookHandler struct {
	bridge   *Bridge
	asClient *ASClient
}

// 事务ID幂等性缓存
var (
	processedTxns   = make(map[string]time.Time)
	processedTxnsMu sync.Mutex
)

// isProcessedTxn 检查事务是否已处理（并标记为已处理）
func isProcessedTxn(txnID string) bool {
	processedTxnsMu.Lock()
	defer processedTxnsMu.Unlock()

	// 清理超过1小时的旧条目
	now := time.Now()
	for k, v := range processedTxns {
		if now.Sub(v) > time.Hour {
			delete(processedTxns, k)
		}
	}

	if _, ok := processedTxns[txnID]; ok {
		return true
	}
	processedTxns[txnID] = now
	return false
}

// NewWebhookHandler 创建 Webhook 处理器
func NewWebhookHandler(bridge *Bridge) *WebhookHandler {
	return &WebhookHandler{
		bridge:   bridge,
		asClient: NewASClient(),
	}
}

// HandleTransaction 处理 AS Transaction
// Matrix Homeserver 通过 PUT /_matrix/app/v1/transactions/{txnId} 推送事件
func (h *WebhookHandler) HandleTransaction(c *gin.Context) {
	// 验证 HS Token
	token := c.GetHeader("Authorization")
	if token == "" {
		token = c.Query("access_token")
	}

	if !h.asClient.ValidateHSToken(extractBearerToken(token)) {
		log.Warn("Invalid HS token in Matrix webhook request")
		c.JSON(http.StatusUnauthorized, gin.H{
			"errcode": "M_UNAUTHORIZED",
			"error":   "Invalid or missing access token",
		})
		return
	}

	txnID := c.Param("txnId")
	if txnID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Missing transaction ID",
		})
		return
	}

	// 事务幂等性检查：防止重复处理
	if isProcessedTxn(txnID) {
		log.Info("Skipping already processed transaction %s", txnID)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// 解析请求体
	var txn Transaction
	if err := c.ShouldBindJSON(&txn); err != nil {
		log.Error("Failed to parse Matrix transaction: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Failed to parse request body",
		})
		return
	}

	// 处理事件
	if h.bridge != nil {
		if err := h.bridge.HandleTransaction(txnID, txn.Events); err != nil {
			log.Error("Failed to handle Matrix transaction %s: %v", txnID, err)
			// 仍然返回 200，避免 HS 重试
		}
	}

	// Matrix 规范要求返回空对象表示成功
	c.JSON(http.StatusOK, gin.H{})
}

// HandleUserQuery 处理用户查询
// GET /_matrix/app/v1/users/{userId}
func (h *WebhookHandler) HandleUserQuery(c *gin.Context) {
	// 验证 HS Token
	token := c.GetHeader("Authorization")
	if token == "" {
		token = c.Query("access_token")
	}

	if !h.asClient.ValidateHSToken(extractBearerToken(token)) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"errcode": "M_UNAUTHORIZED",
			"error":   "Invalid or missing access token",
		})
		return
	}

	userID := c.Param("userId")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Missing user ID",
		})
		return
	}

	// 检查是否是本系统映射的用户
	LoadConfig()
	localpart, _, ok := ParseMatrixUserID(userID)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_INVALID_PARAM",
			"error":   "Invalid user ID format",
		})
		return
	}

	// 检查是否在命名空间内
	if GlobalConfig.UserNamespacePrefix != "" &&
		len(localpart) > len(GlobalConfig.UserNamespacePrefix) &&
		localpart[:len(GlobalConfig.UserNamespacePrefix)] == GlobalConfig.UserNamespacePrefix {
		// 是本系统映射的用户，返回用户信息
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// 不是本系统的用户
	c.JSON(http.StatusNotFound, gin.H{
		"errcode": "M_NOT_FOUND",
		"error":   "User not found",
	})
}

// HandleRoomQuery 处理房间查询
// GET /_matrix/app/v1/rooms/{roomAlias}
func (h *WebhookHandler) HandleRoomQuery(c *gin.Context) {
	// 验证 HS Token
	token := c.GetHeader("Authorization")
	if token == "" {
		token = c.Query("access_token")
	}

	if !h.asClient.ValidateHSToken(extractBearerToken(token)) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"errcode": "M_UNAUTHORIZED",
			"error":   "Invalid or missing access token",
		})
		return
	}

	roomAlias := c.Param("roomAlias")
	if roomAlias == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Missing room alias",
		})
		return
	}

	// 检查是否是本系统映射的房间
	LoadConfig()
	if GlobalConfig.RoomNamespacePrefix != "" &&
		len(roomAlias) > len(GlobalConfig.RoomNamespacePrefix) &&
		roomAlias[:len(GlobalConfig.RoomNamespacePrefix)] == GlobalConfig.RoomNamespacePrefix {
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	// 不是本系统的房间
	c.JSON(http.StatusNotFound, gin.H{
		"errcode": "M_NOT_FOUND",
		"error":   "Room not found",
	})
}

// HandleHealthCheck 健康检查端点
func (h *WebhookHandler) HandleHealthCheck(c *gin.Context) {
	LoadConfig()
	if !GlobalConfig.IsEnabled() {
		c.JSON(http.StatusOK, gin.H{
			"status":  "disabled",
			"message": "Matrix Bridge is not enabled",
		})
		return
	}

	if h.bridge != nil && h.bridge.IsStarted() {
		c.JSON(http.StatusOK, gin.H{
			"status":  "running",
			"message": "Matrix Bridge is running",
		})
		return
	}

	c.JSON(http.StatusServiceUnavailable, gin.H{
		"status":  "stopped",
		"message": "Matrix Bridge is not running",
	})
}

// extractBearerToken 从 Authorization header 中提取 Bearer token
func extractBearerToken(authHeader string) string {
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		return authHeader[7:]
	}
	return authHeader
}
