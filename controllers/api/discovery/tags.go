package discovery

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	service "github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
)

// ListTags is deliberately separate from Mastodon's calendar-day history DTO.
func ListTags(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	window, scope := c.DefaultQuery("window", "24h"), c.DefaultQuery("scope", "federated")
	if (window != "24h" && window != "7d") || (scope != "local" && scope != "federated") {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid window or scope", "code": "invalid_request"})
		return
	}
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 20 {
			c.AbortWithStatusJSON(400, gin.H{"error": "limit must be between 1 and 20", "code": "invalid_request"})
			return
		}
		limit = value
	}
	var viewer *models.Account
	if raw, ok := c.Get("account"); ok {
		viewer, _ = raw.(*models.Account)
	}
	page, err := service.ReadTagTrends(c.Request.Context(), viewer, window, scope, limit, c.Query("cursor"))
	if err != nil {
		if errors.Is(err, service.ErrTagCursor) {
			c.AbortWithStatusJSON(410, gin.H{"code": "reset_required", "error": "Trending tags have been updated; please refresh"})
			return
		}
		_ = c.Error(err)
		c.Header("Retry-After", "5")
		c.AbortWithStatusJSON(503, gin.H{"code": "unavailable", "error": "Trending tags are temporarily unavailable, please try again later", "retryable": true})
		return
	}
	if !page.CoverageComplete && !page.Disabled {
		c.Header("X-Trends-Partial", "true")
	}
	c.JSON(200, page)
}
