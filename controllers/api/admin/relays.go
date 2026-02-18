package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// ListRelays handles GET /api/v1/admin/relays
// 列出所有中继
func ListRelays(c *gin.Context) {
	relayService := activitypub.NewRelayService()

	relays, err := relayService.GetAllRelays()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch relays"})
		return
	}

	c.JSON(http.StatusOK, activitypub.SerializeRelays(relays))
}

// CreateRelay handles POST /api/v1/admin/relays
// 创建并启用中继
func CreateRelay(c *gin.Context) {
	var params struct {
		InboxURL string `json:"inbox_url" binding:"required,url"`
	}

	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid inbox_url"})
		return
	}

	relayService := activitypub.NewRelayService()

	// 创建中继
	relay, err := relayService.CreateRelay(params.InboxURL)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	// 启用中继（发送 Follow 活动）
	if err := relayService.EnableRelay(relay.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to enable relay"})
		return
	}

	// 重新获取更新后的中继
	relay, _ = relayService.GetRelayByID(relay.ID)

	c.JSON(http.StatusOK, activitypub.SerializeRelay(relay))
}

// EnableRelay handles POST /api/v1/admin/relays/:id/enable
// 启用中继
func EnableRelay(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid relay ID"})
		return
	}

	relayService := activitypub.NewRelayService()

	// 检查中继是否存在
	relay, err := relayService.GetRelayByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Relay not found"})
		return
	}

	// 启用中继
	if err := relayService.EnableRelay(relay.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 重新获取更新后的中继
	relay, _ = relayService.GetRelayByID(id)

	c.JSON(http.StatusOK, activitypub.SerializeRelay(relay))
}

// DisableRelay handles POST /api/v1/admin/relays/:id/disable
// 禁用中继
func DisableRelay(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid relay ID"})
		return
	}

	relayService := activitypub.NewRelayService()

	// 检查中继是否存在
	relay, err := relayService.GetRelayByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Relay not found"})
		return
	}

	// 禁用中继
	if err := relayService.DisableRelay(relay.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 重新获取更新后的中继
	relay, _ = relayService.GetRelayByID(id)

	c.JSON(http.StatusOK, activitypub.SerializeRelay(relay))
}

// DeleteRelay handles DELETE /api/v1/admin/relays/:id
// 删除中继
func DeleteRelay(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid relay ID"})
		return
	}

	relayService := activitypub.NewRelayService()

	// 检查中继是否存在
	_, err = relayService.GetRelayByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Relay not found"})
		return
	}

	// 删除中继
	if err := relayService.DeleteRelay(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete relay"})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// GetRelay handles GET /api/v1/admin/relays/:id
// 获取单个中继
func GetRelay(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid relay ID"})
		return
	}

	relayService := activitypub.NewRelayService()

	relay, err := relayService.GetRelayByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Relay not found"})
		return
	}

	c.JSON(http.StatusOK, activitypub.SerializeRelay(relay))
}

// GetRelayInboxes handles GET /api/v1/admin/relays/inboxes
// 获取所有已启用中继的 inbox URL（用于调试）
func GetRelayInboxes(c *gin.Context) {
	relayService := activitypub.NewRelayService()

	inboxes, err := relayService.GetRelayInboxes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch relay inboxes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"inboxes": inboxes,
		"count":   len(inboxes),
	})
}

// Helper function to get enabled relays count
func getEnabledRelaysCount() int64 {
	var count int64
	db.GetDB().Model(&models.Relay{}).Where("state = ?", models.RelayStateAccepted).Count(&count)
	return count
}
