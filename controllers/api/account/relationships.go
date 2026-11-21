package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"strconv"
)

// FollowersRequest represents the request for getting followers/following.
type FollowersRequest struct {
	Limit   int    `binding:"omitempty,min=1,max=80" form:"limit"`
	MaxId   string `form:"max_id"`
	SinceId string `form:"since_id"`
}

// Followers handles GET /api/v1/accounts/:id/followers.
func Followers(c *gin.Context) {
	var req FollowersRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	if req.Limit == 0 {
		req.Limit = 40
	}

	// Get account ID from URL
	accountId := c.Param("id")
	if accountId == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"}) //nolint:goconst

		return
	}

	// Check if account exists and hide_collections is false
	targetAccount := &models.Account{}

	err := db.GetDB().Preload("Extra").First(targetAccount, accountId).Error
	if err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "account not found"}) //nolint:goconst

		return
	}

	// Check if current account can view
	var currentAccount *models.Account
	if c.GetBool("authenticated") {
		currentAccount = c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	}

	// Followers-list visibility (public / followers / private); the owner
	// always sees it. Hidden lists answer an empty page, as Mastodon does.
	if !targetAccount.CanViewCollection(db.GetDB(), currentAccount, true) {
		c.JSON(200, []*serializer.Account{})

		return
	}

	// Query followers (accounts that follow this account)
	query := db.GetDB().Model(&models.Follow{}).
		Preload("Account").
		Preload("Account.Emojis").
		Preload("Account.Extra").
		Scopes(models.Followed).
		Where("follow_id = ?", accountId).
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

	follows := make([]*models.Follow, 0)
	if err := query.Limit(req.Limit + 1).Find(&follows).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get followers error"))

		return
	}

	hasMore := len(follows) > req.Limit
	if hasMore {
		follows = follows[:req.Limit]
	}

	if len(follows) > 0 {
		common.SetPageLinksUint(c, follows[0].Id, follows[len(follows)-1].Id, hasMore)
	}

	// Extract accounts
	accounts := make([]*models.Account, 0, len(follows))

	for _, f := range follows {
		if f.Account != nil {
			accounts = append(accounts, f.Account)
		}
	}

	c.JSON(200, serializer.ToAccounts(accounts))
}

// Following handles GET /api/v1/accounts/:id/following.
func Following(c *gin.Context) {
	var req FollowersRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})

		return
	}

	if req.Limit == 0 {
		req.Limit = 40
	}

	// Get account ID from URL
	accountId := c.Param("id")
	if accountId == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})

		return
	}

	// Check if account exists and hide_collections is false
	targetAccount := &models.Account{}

	err := db.GetDB().Preload("Extra").First(targetAccount, accountId).Error
	if err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "account not found"})

		return
	}

	// Check if current account can view
	var currentAccount *models.Account
	if c.GetBool("authenticated") {
		currentAccount = c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	}

	// Following-list visibility (public / followers / private).
	if !targetAccount.CanViewCollection(db.GetDB(), currentAccount, false) {
		c.JSON(200, []*serializer.Account{})

		return
	}

	// Query following (accounts that this account follows)
	query := db.GetDB().Model(&models.Follow{}).
		Scopes(models.Followed).
		Where("follows.account_id = ?", accountId).
		Order("follows.id DESC")

	// Pagination
	if req.MaxId != "" {
		if maxId, err := strconv.ParseUint(req.MaxId, 10, 64); err == nil {
			query = query.Where("follows.id < ?", maxId)
		}
	}

	if req.SinceId != "" {
		if sinceId, err := strconv.ParseUint(req.SinceId, 10, 64); err == nil {
			query = query.Where("follows.id > ?", sinceId)
		}
	}

	follows := make([]*models.Follow, 0)
	if err := query.Limit(req.Limit + 1).Find(&follows).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get following error"))

		return
	}

	hasMore := len(follows) > req.Limit
	if hasMore {
		follows = follows[:req.Limit]
	}

	if len(follows) > 0 {
		common.SetPageLinksUint(c, follows[0].Id, follows[len(follows)-1].Id, hasMore)
	}

	// Load follow accounts separately
	accountIds := make([]uint64, 0, len(follows))
	for _, f := range follows {
		accountIds = append(accountIds, f.FollowId)
	}

	accounts := make([]*models.Account, 0)
	if len(accountIds) > 0 {
		err := db.GetDB().Preload("Emojis").Preload("Extra").Where("id IN ?", accountIds).Find(&accounts).Error
		if err != nil {
			_ = c.Error(errors.Wrap(err, "get accounts error"))

			return
		}
	}

	byID := make(map[uint64]*models.Account, len(accounts))
	for _, account := range accounts {
		byID[account.Id] = account
	}
	accounts = make([]*models.Account, 0, len(accountIds))
	for _, id := range accountIds {
		if account := byID[id]; account != nil {
			accounts = append(accounts, account)
		}
	}

	c.JSON(200, serializer.ToAccounts(accounts))
}

// Relationships returns the signed-in account's relationship to each known ID.
func Relationships(c *gin.Context) {
	viewer := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	ids := append(c.QueryArray("id"), c.QueryArray("id[]")...)
	targetIDs := make([]uint64, 0, len(ids))
	for _, raw := range ids {
		if id, err := strconv.ParseUint(raw, 10, 64); err == nil && id != 0 {
			targetIDs = append(targetIDs, id)
		}
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	targets := make([]*models.Account, 0, len(targetIDs))
	if len(targetIDs) > 0 {
		withSuspended, _ := strconv.ParseBool(c.Query("with_suspended"))
		query := conn.Select("id", "domain", "followed_message").Where("id IN ? AND deletion_requested_at IS NULL", targetIDs)
		if !withSuspended {
			query = query.Where("is_ban = ?", false)
		}
		if err := query.Find(&targets).Error; err != nil {
			_ = c.Error(err)
			return
		}
	}
	relationships, err := serializer.LoadRelationships(conn, viewer.Id, targets)
	if err != nil {
		_ = c.Error(err)
		return
	}
	result := make([]*serializer.FollowStatus, 0, len(relationships))
	for _, id := range targetIDs {
		if row := relationships[id]; row != nil {
			result = append(result, row)
			delete(relationships, id)
		}
	}
	c.JSON(200, result)
}

func renderRelationship(c *gin.Context, viewer, target *models.Account) {
	result, err := serializer.LoadRelationships(db.GetDB().WithContext(c.Request.Context()), viewer.Id, []*models.Account{target})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(200, result[target.Id])
}
