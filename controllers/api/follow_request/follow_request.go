package follow_request

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

// ListRequest represents the request for listing follow requests.
type ListRequest struct {
	Limit   int    `binding:"min=1,max=80" form:"limit"`
	MaxId   string `form:"max_id"`
	SinceId string `form:"since_id"`
}

// FollowRequestResponse represents the response for follow requests (Account)
// In Mastodon API, follow requests are returned as accounts

// List handles GET /api/v1/follow_requests.
func List(c *gin.Context) {
	var req ListRequest
	err := c.ShouldBindQuery(&req)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	if req.Limit == 0 {
		req.Limit = 40
	}

	// Get current account
	currentAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Query follow requests (accounts that want to follow me)
	query := db.GetDB().Model(&models.Follow{}).
		Preload("Account").
		Preload("Account.Emojis").
		Preload("Account.Extra").
		Scopes(models.FollowRequested).
		Where("follow_id = ?", currentAccount.Id).
		Order("id DESC")

	// Pagination
	if req.MaxId != "" {
		if maxId, err := strconv.ParseUint(req.MaxId, 10, 64); err == nil {
			query = query.Where("id < ?", maxId)
		}
	}

	if req.SinceId != "" {
		if sinceId, err := strconv.ParseUint(req.SinceId, 10, 64); err == nil {
			query = query.Where("id > ?", sinceId)
		}
	}

	followRequests := make([]*models.Follow, 0)
	err = query.Limit(req.Limit).Find(&followRequests).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "get follow requests error"))

		return
	}

	// Extract accounts from follow requests
	accounts := make([]*models.Account, 0, len(followRequests))

	for _, fr := range followRequests {
		if fr.Account != nil {
			accounts = append(accounts, fr.Account)
		}
	}

	c.JSON(200, serializer.ToAccounts(accounts))
}

// Authorize handles POST /api/v1/follow_requests/:id/authorize.
func Authorize(c *gin.Context) {
	// Get the account ID from URL
	targetAccountId := c.Param("id")
	if targetAccountId == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"}) //nolint:goconst

		return
	}

	targetId, err := strconv.ParseUint(targetAccountId, 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})

		return
	}

	// Get current account
	currentAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Find the follow request
	followRequest := &models.Follow{}

	err = db.GetDB().Scopes(models.FollowRequested).
		Where("account_id = ? AND follow_id = ?", targetId, currentAccount.Id).
		First(followRequest).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "follow request not found"})

			return
		}

		_ = c.Error(errors.Wrap(err, "get follow request error"))

		return
	}

	// Update status to followed
	followRequest.Status = models.FollowStatusFollowed
	if err := db.GetDB().Save(followRequest).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "update follow request error"))

		return
	}

	// Update follower count for current account
	if err := db.GetDB().Model(&models.AccountExtra{}).
		Where("account_id = ?", currentAccount.Id).
		UpdateColumn("followers_count", gorm.Expr("followers_count + ?", 1)).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "update followers count error"))
	}

	// Update following count for target account
	if err := db.GetDB().Model(&models.AccountExtra{}).
		Where("account_id = ?", targetId).
		UpdateColumn("following_count", gorm.Expr("following_count + ?", 1)).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "update following count error"))
	}

	// Return relationship
	c.JSON(200, gin.H{
		"id":                   targetAccountId,
		"following":            true,
		"followed_by":          false,
		"blocking":             false,
		"muting":               false,
		"muting_notifications": false,
		"requested":            false,
		"domain_blocking":      false,
		"showing_reblogs":      true,
		"endorsed":             false,
		"note":                 "",
	})
}

// Reject handles POST /api/v1/follow_requests/:id/reject.
func Reject(c *gin.Context) {
	// Get the account ID from URL
	targetAccountId := c.Param("id")
	if targetAccountId == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})

		return
	}

	targetId, err := strconv.ParseUint(targetAccountId, 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})

		return
	}

	// Get current account
	currentAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Find and delete the follow request
	result := db.GetDB().Scopes(models.FollowRequested).
		Where("account_id = ? AND follow_id = ?", targetId, currentAccount.Id).
		Delete(&models.Follow{})

	if result.Error != nil {
		_ = c.Error(errors.Wrap(result.Error, "delete follow request error"))

		return
	}

	if result.RowsAffected == 0 {
		c.AbortWithStatusJSON(404, gin.H{"error": "follow request not found"})

		return
	}

	// Return relationship
	c.JSON(200, gin.H{
		"id":                   targetAccountId,
		"following":            false,
		"followed_by":          false,
		"blocking":             false,
		"muting":               false,
		"muting_notifications": false,
		"requested":            false,
		"domain_blocking":      false,
		"showing_reblogs":      true,
		"endorsed":             false,
		"note":                 "",
	})
}
