package instance

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// Peers handles GET /api/v1/instance/peers
// Returns all known domains this instance is aware of.
func Peers(c *gin.Context) {
	// 尝试从缓存读取（TTL 10 分钟，peers 列表变化不频繁）
	var cached []string
	err := cache.GetCache().Get(c.Request.Context(), cache.KeyInstanceStats+":peers", &cached)
	if err == nil {
		c.JSON(200, cached)

		return
	}

	// 缓存未命中，查询数据库
	var domains []string
	err = db.GetDB().Model(&models.Account{}).
		Where("domain IS NOT NULL AND domain != ''").
		Distinct("domain").
		Pluck("domain", &domains).Error
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// 写入缓存
	_ = cache.GetCache().Set(c.Request.Context(), fmt.Sprintf("%s:peers", cache.KeyInstanceStats), domains, cache.LongExpiration)

	c.JSON(200, domains)
}
