package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"strconv"
)

// GetDirectory handles GET /api/v1/directory
func GetDirectory(c *gin.Context) {
	limit := 40
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}

	offset := 0
	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Get order (active or new)
	order := c.Query("order")
	if order == "" {
		order = "active"
	}

	// Query accounts
	query := db.GetDB().Model(&models.Account{}).
		Where("locked = ?", false).
		Where("suspended = ?", false).
		Where("silenced = ?", false).
		Preload("Emojis").
		Preload("Extra")

	// Order by
	if order == "new" {
		query = query.Order("created_at DESC")
	} else {
		// active - order by last status at or followers count
		query = query.Order("last_status_at DESC, followers_count DESC")
	}

	accounts := make([]*models.Account, 0)
	if err := query.Limit(limit).Offset(offset).Find(&accounts).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get directory error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, serializer.ToAccounts(accounts))
}
