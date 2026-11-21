package account

import (
	"math"
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
		renderRelationship(c, account, targetAccount)

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

	renderRelationship(c, account, targetAccount)
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

	renderRelationship(c, account, targetAccount)
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
	var req struct {
		Notifications *bool `json:"notifications"`
		Duration      int64 `json:"duration"`
	}
	if err := bindOptionalBody(c, &req); err != nil || req.Duration < 0 || req.Duration > math.MaxInt64/int64(time.Second) {
		c.JSON(422, gin.H{"error": "Invalid mute parameters"})
		return
	}
	notify := req.Notifications == nil || *req.Notifications
	var expiresAt *time.Time
	if req.Duration > 0 {
		expiresAt = new(time.Now().Add(time.Duration(req.Duration) * time.Second))
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
		if err := db.GetDB().Model(mute).Updates(map[string]any{"expired_at": expiresAt, "notifications": notify}).Error; err != nil {
			_ = c.Error(err)
			return
		}
		renderRelationship(c, account, targetAccount)

		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		_ = c.Error(err)
		return
	}

	// Create mute
	now := time.Now()

	mute = &models.MuteAccount{
		AccountId:     account.Id,
		TargetId:      accid,
		CreatedAt:     &now,
		ExpiredAt:     expiresAt,
		Notifications: notify,
	}
	if err := db.GetDB().Create(mute).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create mute error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	renderRelationship(c, account, targetAccount)
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

	renderRelationship(c, account, targetAccount)
}

// BlocksList handles GET /api/v1/blocks.
func BlocksList(c *gin.Context) {
	page := readRelationPage(c, 40)
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	query := db.GetDB().Model(&models.Block{}).
		Where("account_id = ? AND type = ?", account.Id, models.BlockTypeAccount)

	blocks := make([]*models.Block, 0)
	if err := page.apply(c, query, "id").Find(&blocks).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get blocks error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	cursors := make([]uint64, 0, len(blocks))
	targets := make([]uint64, 0, len(blocks))
	for _, b := range blocks {
		cursors = append(cursors, b.Id)
		targets = append(targets, b.TargetAccountId)
	}

	result, err := accountsInOrder(targets)
	if err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	page.links(c, cursors)
	c.JSON(200, result)
}

// MutesList handles GET /api/v1/mutes.
func MutesList(c *gin.Context) {
	page := readRelationPage(c, 40)
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	query := db.GetDB().Model(&models.MuteAccount{}).
		Where("account_id = ?", account.Id).
		Where("expired_at IS NULL OR expired_at > ?", time.Now())

	mutes := make([]*models.MuteAccount, 0)
	if err := page.apply(c, query, "id").Find(&mutes).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get mutes error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	cursors := make([]uint64, 0, len(mutes))
	targets := make([]uint64, 0, len(mutes))
	expiresAt := make(map[string]*serializer.APITime, len(mutes))
	for _, m := range mutes {
		cursors = append(cursors, m.Id)
		targets = append(targets, m.TargetId)
		expiresAt[strconv.FormatUint(m.TargetId, 10)] = serializer.NewAPITime(m.ExpiredAt)
	}

	result, err := accountsInOrder(targets)
	if err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	page.links(c, cursors)
	type mutedAccount struct {
		*serializer.Account
		MuteExpiresAt *serializer.APITime `json:"mute_expires_at"`
	}
	out := make([]mutedAccount, 0, len(result))
	for _, account := range result {
		out = append(out, mutedAccount{Account: account, MuteExpiresAt: expiresAt[account.Id]})
	}
	c.JSON(200, out)
}
