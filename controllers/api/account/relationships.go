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

	// If hide_collections is true, only allow self to view
	if targetAccount.HideCollections {
		if currentAccount == nil || currentAccount.Id != targetAccount.Id {
			c.JSON(200, []*serializer.Account{})

			return
		}
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

	// If hide_collections is true, only allow self to view
	if targetAccount.HideCollections {
		if currentAccount == nil || currentAccount.Id != targetAccount.Id {
			c.JSON(200, []*serializer.Account{})

			return
		}
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

// RelationshipsRequest represents the request for relationships.
type RelationshipsRequest struct {
	Id []string `binding:"required" form:"id"`
}

// Relationships handles GET /api/v1/accounts/relationships.
func Relationships(c *gin.Context) {
	// Get current account
	currentAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Get target account IDs
	// Mastodon clients send id[]=1&id[]=2; also accept plain id=1&id=2.
	ids := append(c.QueryArray("id"), c.QueryArray("id[]")...)
	if len(ids) == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "id parameter required"})

		return
	}

	// Parse IDs
	targetIds := make([]uint64, 0, len(ids))

	for _, idStr := range ids {
		if id, err := strconv.ParseUint(idStr, 10, 64); err == nil {
			targetIds = append(targetIds, id)
		}
	}

	if len(targetIds) == 0 {
		c.JSON(200, []gin.H{})

		return
	}

	// Query relationships
	// Following status
	followingMap := make(map[uint64]bool)

	followingList := make([]*models.Follow, 0)
	err := db.GetDB().Scopes(models.Followed).
		Where("account_id = ? AND follow_id IN ?", currentAccount.Id, targetIds).
		Find(&followingList).Error
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	showingReblogsMap := make(map[uint64]bool)
	for _, f := range followingList {
		followingMap[f.FollowId] = true
		showingReblogsMap[f.FollowId] = f.ShowsReblogs()
	}

	// Follow requested status
	requestedMap := make(map[uint64]bool)

	requestedList := make([]*models.Follow, 0)
	err = db.GetDB().Scopes(models.FollowRequested).
		Where("account_id = ? AND follow_id IN ?", currentAccount.Id, targetIds).
		Find(&requestedList).Error
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	for _, f := range requestedList {
		requestedMap[f.FollowId] = true
	}

	// Followed by status
	followedByMap := make(map[uint64]bool)

	followedList := make([]*models.Follow, 0)
	err = db.GetDB().Scopes(models.Followed).
		Where("follow_id = ? AND account_id IN ?", currentAccount.Id, targetIds).
		Find(&followedList).Error
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	for _, f := range followedList {
		followedByMap[f.AccountId] = true
	}

	// Blocking status
	blockingMap := make(map[uint64]bool)

	blocks := make([]*models.Block, 0)
	err = db.GetDB().Where("account_id = ? AND target_account_id IN ?", currentAccount.Id, targetIds).
		Find(&blocks).Error
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	for _, b := range blocks {
		blockingMap[b.TargetAccountId] = true
	}

	// Muting status
	mutingMap := make(map[uint64]bool)

	mutes := make([]*models.MuteAccount, 0)
	err = db.GetDB().Where("account_id = ? AND target_id IN ?", currentAccount.Id, targetIds).
		Find(&mutes).Error
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	for _, m := range mutes {
		mutingMap[m.TargetId] = true
	}

	// Build response
	relationships := make([]gin.H, 0, len(targetIds))
	for _, targetId := range targetIds {
		relationships = append(relationships, gin.H{
			"id":                   strconv.FormatUint(targetId, 10),
			"following":            followingMap[targetId], //nolint:goconst
			"followed_by":          followedByMap[targetId],
			"blocking":             blockingMap[targetId],
			"muting":               mutingMap[targetId],
			"muting_notifications": false,
			"requested":            requestedMap[targetId],
			"domain_blocking":      false,
			"showing_reblogs":      showingReblogs(followingMap[targetId], showingReblogsMap[targetId]),
			"endorsed":             false,
			"note":                 "",
		})
	}

	c.JSON(200, relationships)
}

// showingReblogs mirrors Mastodon: false when not following, otherwise the
// follow's reblogs preference.
func showingReblogs(following, shows bool) bool { return following && shows }
