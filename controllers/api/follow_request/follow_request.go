package follow_request

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
)

// ListRequest represents the request for listing follow requests.
type ListRequest struct {
	Limit   int    `binding:"omitempty,min=1,max=80" form:"limit"`
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

	// Lock the pending row and update both counters in the same transaction,
	// so concurrent approvals cannot count the same request twice.
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		followRequest := &models.Follow{}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Scopes(models.FollowRequested).
			Where("account_id = ? AND follow_id = ?", targetId, currentAccount.Id).First(followRequest).Error; err != nil {
			return err
		}
		followRequest.Status = models.FollowStatusFollowed
		if err := tx.Save(followRequest).Error; err != nil {
			return err
		}
		if err := models.QueueFederationFollowDecision(tx, "Accept", followRequest); err != nil {
			return err
		}
		if err := tx.Model(&models.AccountExtra{}).Where("account_id = ?", currentAccount.Id).
			UpdateColumn("followers_count", gorm.Expr("followers_count + ?", 1)).Error; err != nil {
			return err
		}
		return tx.Model(&models.AccountExtra{}).Where("account_id = ?", targetId).
			UpdateColumn("following_count", gorm.Expr("following_count + ?", 1)).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "follow request not found"})
		} else {
			_ = c.Error(errors.Wrap(err, "authorize follow request error"))
		}
		return
	}

	// Report the relationship from the authenticated owner's perspective.
	outgoing := &models.Follow{}
	lookupErr := db.GetDB().Where("account_id = ? AND follow_id = ?", currentAccount.Id, targetId).First(outgoing).Error
	if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		_ = c.Error(errors.Wrap(lookupErr, "get outgoing relationship error"))
		return
	}
	following := lookupErr == nil && outgoing.Status == models.FollowStatusFollowed
	requested := lookupErr == nil && outgoing.Status == models.FollowStatusRequested

	// Return relationship
	c.JSON(200, gin.H{
		"id":                   targetAccountId,
		"following":            following,
		"followed_by":          true,
		"blocking":             false,
		"muting":               false,
		"muting_notifications": false,
		"requested":            requested,
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

	// Persist the rejection response before deleting its original Follow ID.
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		var request models.Follow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Scopes(models.FollowRequested).Where("account_id = ? AND follow_id = ?", targetId, currentAccount.Id).First(&request).Error; err != nil {
			return err
		}
		if err := models.QueueFederationFollowDecision(tx, "Reject", &request); err != nil {
			return err
		}
		return tx.Delete(&request).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "follow request not found"})
		} else {
			_ = c.Error(errors.Wrap(err, "delete follow request error"))
		}
		return
	}

	// Report the relationship from the authenticated owner's perspective.
	outgoing := &models.Follow{}
	lookupErr := db.GetDB().Where("account_id = ? AND follow_id = ?", currentAccount.Id, targetId).First(outgoing).Error
	if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		_ = c.Error(errors.Wrap(lookupErr, "get outgoing relationship error"))
		return
	}
	following := lookupErr == nil && outgoing.Status == models.FollowStatusFollowed
	requested := lookupErr == nil && outgoing.Status == models.FollowStatusRequested

	// Return relationship
	c.JSON(200, gin.H{
		"id":                   targetAccountId,
		"following":            following,
		"followed_by":          false,
		"blocking":             false,
		"muting":               false,
		"muting_notifications": false,
		"requested":            requested,
		"domain_blocking":      false,
		"showing_reblogs":      true,
		"endorsed":             false,
		"note":                 "",
	})
}
