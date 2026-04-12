package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"strconv"
)

// ListSuggestions handles GET /api/v2/suggestions
func ListSuggestions(c *gin.Context) {
	limit := 20
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 40 {
			limit = parsed
		}
	}

	// Get current account
	account := c.MustGet("account").(*models.Account)

	// Get accounts that are followed by accounts the user follows
	// (friends of friends) but not followed by the user
	subquery := db.GetDB().Table("follows").
		Select("DISTINCT follow_id").
		Where("account_id = ?", account.Id)

	// Find accounts that are followed by people the user follows
	// but not by the user themselves
	query := db.GetDB().Model(&models.Account{}).
		Where("id IN (SELECT follow_id FROM follows WHERE account_id IN (?))", subquery).
		Where("id NOT IN (SELECT follow_id FROM follows WHERE account_id = ?)", account.Id).
		Where("id != ?", account.Id).
		Where("locked = ?", false). // Only suggest public accounts
		Preload("Emojis").
		Preload("Extra").
		Order("followers_count DESC").
		Limit(limit)

	accounts := make([]*models.Account, 0)
	if err := query.Find(&accounts).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get suggestions error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	// Build response with source info
	result := make([]gin.H, 0, len(accounts))
	for _, acc := range accounts {
		result = append(result, gin.H{
			"source":  "past_interactions",
			"account": serializer.ToAccount(acc),
		})
	}

	// If no suggestions from social graph, return popular accounts
	if len(result) == 0 {
		popularQuery := db.GetDB().Model(&models.Account{}).
			Where("id != ?", account.Id).
			Where("locked = ?", false).
			Where("followers_count > 0").
			Preload("Emojis").
			Preload("Extra").
			Order("followers_count DESC").
			Limit(limit)

		popularAccounts := make([]*models.Account, 0)
		if err := popularQuery.Find(&popularAccounts).Error; err != nil {
			c.JSON(200, []interface{}{})
			return
		}

		for _, acc := range popularAccounts {
			result = append(result, gin.H{
				"source":  "global",
				"account": serializer.ToAccount(acc),
			})
		}
	}

	c.JSON(200, result)
}

// RemoveSuggestion handles DELETE /api/v1/suggestions/:account_id
func RemoveSuggestion(c *gin.Context) {
	accid := c.Param("account_id")
	if accid == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "account id required"})
		return
	}

	// In Mastodon, this removes the suggestion from the user's view
	// We could store dismissed suggestions, but for simplicity we just return success
	c.JSON(200, gin.H{})
}
