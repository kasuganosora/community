package account

import (
	"github.com/kasuganosora/community/controllers/api/common"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

// Block handles POST /api/v1/accounts/:id/block.
func Block(c *gin.Context) {
	accid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accid == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"}) //nolint:goconst

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Cannot block self
	if account.Id == accid {
		c.AbortWithStatusJSON(400, gin.H{"error": "cannot block yourself"})

		return
	}

	// Check target account exists
	targetAccount := &models.Account{}

	err = db.GetDB().Preload("Extra").First(targetAccount, accid).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "account not found"}) //nolint:goconst

			return
		}

		_ = c.Error(errors.Wrap(err, "get target account error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	// Check if already blocked
	block := &models.Block{}

	err = db.GetDB().Where("account_id = ? AND target_account_id = ? AND type = ?", account.Id, accid, models.BlockTypeAccount).First(block).Error
	if err == nil {
		// Already blocked, return relationship
		c.JSON(200, buildRelationship(account, targetAccount))

		return
	}

	// Create block
	now := time.Now()

	block = &models.Block{
		AccountId:       account.Id,
		Type:            models.BlockTypeAccount,
		TargetAccountId: accid,
		CreatedAt:       &now,
	}
	if err := db.GetDB().Create(block).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create block error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Remove both follow directions through the model so counters decrement.
	if err := models.DeleteFollowEdges(db.GetDB(), account.Id, accid); err != nil {
		_ = c.Error(errors.Wrap(err, "unfollow error"))
	}

	c.JSON(200, buildRelationship(account, targetAccount))
}

// Unblock handles POST /api/v1/accounts/:id/unblock.
func Unblock(c *gin.Context) {
	accid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accid == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Check target account exists
	targetAccount := &models.Account{}

	err = db.GetDB().Preload("Extra").First(targetAccount, accid).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "account not found"})

			return
		}

		_ = c.Error(errors.Wrap(err, "get target account error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Delete block
	if err := db.GetDB().Where("account_id = ? AND target_account_id = ? AND type = ?", account.Id, accid, models.BlockTypeAccount).Delete(&models.Block{}).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "delete block error"))
	}

	c.JSON(200, buildRelationship(account, targetAccount))
}

// Mute handles POST /api/v1/accounts/:id/mute.
func Mute(c *gin.Context) {
	accid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accid == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Cannot mute self
	if account.Id == accid {
		c.AbortWithStatusJSON(400, gin.H{"error": "cannot mute yourself"})

		return
	}

	// Check target account exists
	targetAccount := &models.Account{}

	err = db.GetDB().Preload("Extra").First(targetAccount, accid).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "account not found"})

			return
		}

		_ = c.Error(errors.Wrap(err, "get target account error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Check if already muted
	mute := &models.MuteAccount{}

	err = db.GetDB().Where("account_id = ? AND target_id = ?", account.Id, accid).First(mute).Error
	if err == nil {
		// Already muted, return relationship
		c.JSON(200, buildRelationship(account, targetAccount))

		return
	}

	// Create mute
	now := time.Now()

	mute = &models.MuteAccount{
		AccountId: account.Id,
		TargetId:  accid,
		CreatedAt: &now,
	}
	if err := db.GetDB().Create(mute).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create mute error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, buildRelationship(account, targetAccount))
}

// Unmute handles POST /api/v1/accounts/:id/unmute.
func Unmute(c *gin.Context) {
	accid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accid == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Check target account exists
	targetAccount := &models.Account{}

	err = db.GetDB().Preload("Extra").First(targetAccount, accid).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "account not found"})

			return
		}

		_ = c.Error(errors.Wrap(err, "get target account error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Delete mute
	if err := db.GetDB().Where("account_id = ? AND target_id = ?", account.Id, accid).Delete(&models.MuteAccount{}).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "delete mute error"))
	}

	c.JSON(200, buildRelationship(account, targetAccount))
}

// buildRelationship builds a relationship response for an account.
func buildRelationship(currentAccount *models.Account, targetAccount *models.Account) gin.H {
	// Check following status
	following := false
	follow := &models.Follow{}
	db.GetDB().Scopes(models.Followed).
		Where("account_id = ? AND follow_id = ?", currentAccount.Id, targetAccount.Id).
		First(follow)

	if follow.Id > 0 {
		following = true
	}

	// Check followed by status
	followedBy := false
	followedByFollow := &models.Follow{}
	db.GetDB().Scopes(models.Followed).
		Where("account_id = ? AND follow_id = ?", targetAccount.Id, currentAccount.Id).
		First(followedByFollow)

	if followedByFollow.Id > 0 {
		followedBy = true
	}

	// Check blocking status
	blocking := false
	block := &models.Block{}
	db.GetDB().Where("account_id = ? AND target_account_id = ? AND type = ?", currentAccount.Id, targetAccount.Id, models.BlockTypeAccount).First(block)

	if block.Id > 0 {
		blocking = true
	}

	// Check muting status
	muting := false
	mute := &models.MuteAccount{}
	db.GetDB().Where("account_id = ? AND target_id = ?", currentAccount.Id, targetAccount.Id).First(mute)

	if mute.Id > 0 {
		muting = true
	}

	// Check requested status
	requested := false
	requestedFollow := &models.Follow{}
	db.GetDB().Scopes(models.FollowRequested).
		Where("account_id = ? AND follow_id = ?", currentAccount.Id, targetAccount.Id).
		First(requestedFollow)

	if requestedFollow.Id > 0 {
		requested = true
	}

	return gin.H{
		"id":                   strconv.FormatUint(targetAccount.Id, 10),
		"following":            following, //nolint:goconst
		"followed_by":          followedBy,
		"blocking":             blocking,
		"muting":               muting,
		"muting_notifications": false,
		"requested":            requested,
		"domain_blocking":      false,
		"showing_reblogs":      true,
		"endorsed":             false,
		"note":                 "",
	}
}

// BlocksList handles GET /api/v1/blocks.
func BlocksList(c *gin.Context) {
	limit := 40

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Query blocks
	query := db.GetDB().Model(&models.Block{}).
		Where("account_id = ? AND type = ?", account.Id, models.BlockTypeAccount).
		Order("id DESC")

	// Pagination
	if maxId := c.Query("max_id"); maxId != "" {
		if id, err := strconv.ParseUint(maxId, 10, 64); err == nil {
			query = query.Where("id < ?", id)
		}
	}

	blocks := make([]*models.Block, 0)
	err := query.Limit(limit).Find(&blocks).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "get blocks error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Get blocked accounts
	targetIds := make([]uint64, 0, len(blocks))
	for _, b := range blocks {
		targetIds = append(targetIds, b.TargetAccountId)
	}

	accounts := make([]*models.Account, 0)
	if len(targetIds) > 0 {
		err := db.GetDB().Preload("Emojis").Preload("Extra").Where("id IN ?", targetIds).Find(&accounts).Error
		if err != nil {
			_ = c.Error(errors.Wrap(err, "get accounts error"))
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

			return
		}
	}

	// Build account map for ordering
	accountMap := make(map[uint64]*models.Account)
	for _, acc := range accounts {
		accountMap[acc.Id] = acc
	}

	// Build result in block order
	result := make([]*serializer.Account, 0)

	for _, b := range blocks {
		if acc, ok := accountMap[b.TargetAccountId]; ok {
			result = append(result, serializer.ToAccount(acc))
		}
	}

	if len(blocks) > 0 {
		common.SetPageLinksUint(c, blocks[0].Id, blocks[len(blocks)-1].Id, true)
	}

	c.JSON(200, result)
}

// MutesList handles GET /api/v1/mutes.
func MutesList(c *gin.Context) {
	limit := 40

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Query mutes
	query := db.GetDB().Model(&models.MuteAccount{}).
		Where("account_id = ?", account.Id).
		Where("expired_at IS NULL OR expired_at > ?", time.Now()).
		Order("id DESC")

	// Pagination
	if maxId := c.Query("max_id"); maxId != "" {
		if id, err := strconv.ParseUint(maxId, 10, 64); err == nil {
			query = query.Where("id < ?", id)
		}
	}

	mutes := make([]*models.MuteAccount, 0)
	err := query.Limit(limit).Find(&mutes).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "get mutes error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Get muted accounts
	targetIds := make([]uint64, 0, len(mutes))
	for _, m := range mutes {
		targetIds = append(targetIds, m.TargetId)
	}

	accounts := make([]*models.Account, 0)
	if len(targetIds) > 0 {
		err := db.GetDB().Preload("Emojis").Preload("Extra").Where("id IN ?", targetIds).Find(&accounts).Error
		if err != nil {
			_ = c.Error(errors.Wrap(err, "get accounts error"))
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

			return
		}
	}

	// Build account map for ordering
	accountMap := make(map[uint64]*models.Account)
	for _, acc := range accounts {
		accountMap[acc.Id] = acc
	}

	// Build result in mute order
	result := make([]*serializer.Account, 0)

	for _, m := range mutes {
		if acc, ok := accountMap[m.TargetId]; ok {
			result = append(result, serializer.ToAccount(acc))
		}
	}

	if len(mutes) > 0 {
		common.SetPageLinksUint(c, mutes[0].Id, mutes[len(mutes)-1].Id, true)
	}

	c.JSON(200, result)
}
