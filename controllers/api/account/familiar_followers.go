package account

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

// FamiliarFollowersRequest represents the request for familiar followers
type FamiliarFollowersRequest struct {
	Id []string `form:"id" binding:"required"`
}

// FamiliarFollowers handles GET /api/v1/accounts/familiar_followers
func FamiliarFollowers(c *gin.Context) {
	// Get target account IDs
	ids := c.QueryArray("id")
	if len(ids) == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "id parameter required"})
		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account)

	// Parse target IDs
	targetIds := make([]uint64, 0, len(ids))
	for _, idStr := range ids {
		if id, err := strconv.ParseUint(idStr, 10, 64); err == nil {
			targetIds = append(targetIds, id)
		}
	}

	if len(targetIds) == 0 {
		c.JSON(200, []interface{}{})
		return
	}

	// Build result for each target account
	result := make([]gin.H, 0, len(targetIds))

	for _, targetId := range targetIds {
		// Find accounts that:
		// 1. The current user follows
		// 2. Also follow the target account
		followingSubquery := db.GetDB().Table("follows").
			Select("follow_id").
			Where("account_id = ? AND status = ?", account.Id, models.FollowStatusFollowed)

		followersSubquery := db.GetDB().Table("follows").
			Select("account_id").
			Where("follow_id = ? AND status = ?", targetId, models.FollowStatusFollowed)

		// Get familiar followers (intersection)
		var familiarIds []uint64
		db.GetDB().Table("accounts").
			Select("accounts.id").
			Where("accounts.id IN (?)", followingSubquery).
			Where("accounts.id IN (?)", followersSubquery).
			Where("accounts.id != ?", account.Id).
			Limit(10).
			Pluck("id", &familiarIds)

		// Load accounts
		accounts := make([]*models.Account, 0)
		if len(familiarIds) > 0 {
			if err := db.GetDB().Preload("Emojis").Preload("Extra").
				Where("id IN ?", familiarIds).
				Find(&accounts).Error; err != nil {
				_ = c.Error(errors.Wrap(err, "get familiar followers error"))
			}
		}

		// Build accounts response
		familiarFollowers := serializer.ToAccounts(accounts)

		result = append(result, gin.H{
			"id":       strconv.FormatUint(targetId, 10),
			"accounts": familiarFollowers,
		})
	}

	c.JSON(200, result)
}
