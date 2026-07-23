package account

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

// Endorsement represents an account endorsement.
type Endorsement struct {
	Id        uint64 `gorm:"primary_key;auto_increment" json:"id"`
	AccountId uint64 `gorm:"not null;index"             json:"account_id"`
	TargetId  uint64 `gorm:"not null;index"             json:"target_id"`
}

func (e *Endorsement) TableName() string {
	return "endorsements"
}

// ListEndorsements handles GET /api/v1/endorsements.
func ListEndorsements(c *gin.Context) {
	limit := 40

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Query endorsements
	query := db.GetDB().Table("endorsements").
		Select("endorsements.*, accounts.*").
		Joins("LEFT JOIN accounts ON accounts.id = endorsements.target_id").
		Where("endorsements.account_id = ?", account.Id).
		Preload("Emojis", "account_id = endorsements.target_id").
		Preload("Extra", "account_id = endorsements.target_id").
		Order("endorsements.id DESC")

	// Pagination
	if maxId := c.Query("max_id"); maxId != "" {
		if id, err := strconv.ParseUint(maxId, 10, 64); err == nil {
			query = query.Where("endorsements.id < ?", id)
		}
	}

	type EndorsementResult struct {
		EndorsementId uint64 `json:"endorsement_id"`
		AccountId     uint64 `json:"account_id"`
		TargetId      uint64 `json:"target_id"`
		models.Account
	}

	results := make([]EndorsementResult, 0)
	err := query.Limit(limit).Find(&results).Error
	if err != nil {
		// If table doesn't exist, return empty array
		c.JSON(200, []any{})

		return
	}

	// Load accounts separately
	accountIds := make([]uint64, 0, len(results))
	for _, r := range results {
		accountIds = append(accountIds, r.TargetId)
	}

	accounts := make([]*models.Account, 0)
	if len(accountIds) > 0 {
		err := db.GetDB().Preload("Emojis").Preload("Extra").Where("id IN ?", accountIds).Find(&accounts).Error
		if err != nil {
			_ = c.Error(errors.Wrap(err, "get accounts error"))
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst

			return
		}
	}

	c.JSON(200, serializer.ToAccounts(accounts))
}

// PinAccount handles POST /api/v1/accounts/:id/pin.
func PinAccount(c *gin.Context) {
	accid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accid == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"}) //nolint:goconst

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Cannot pin self
	if account.Id == accid {
		c.AbortWithStatusJSON(400, gin.H{"error": "cannot pin yourself"})

		return
	}

	// Check target account exists and is followed
	follow := &models.Follow{}

	err = db.GetDB().Scopes(models.Followed).
		Where("account_id = ? AND follow_id = ?", account.Id, accid).
		First(follow).Error
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "can only endorse accounts you follow"})

		return
	}

	// Check if already endorsed
	var count int64
	if err := db.GetDB().Table("endorsements").
		Where("account_id = ? AND target_id = ?", account.Id, accid).
		Count(&count).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "check endorsement error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	if count > 0 {
		// Already endorsed, return relationship
		targetAccount := &models.Account{}
		err := db.GetDB().Preload("Extra").First(targetAccount, accid).Error
		if err != nil {
			_ = c.Error(errors.Wrap(err, "load target account error"))
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

			return
		}

		c.JSON(200, buildRelationship(account, targetAccount))

		return
	}

	// Create endorsement
	endorsement := &Endorsement{
		AccountId: account.Id,
		TargetId:  accid,
	}
	if err := db.GetDB().Create(endorsement).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create endorsement error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Load target account
	targetAccount := &models.Account{}
	if err := db.GetDB().Preload("Extra").First(targetAccount, accid).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "load target account error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, buildRelationship(account, targetAccount))
}

// UnpinAccount handles POST /api/v1/accounts/:id/unpin.
func UnpinAccount(c *gin.Context) {
	accid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accid == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Delete endorsement
	if err := db.GetDB().Table("endorsements").
		Where("account_id = ? AND target_id = ?", account.Id, accid).
		Delete(&Endorsement{}).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "delete endorsement error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Load target account
	targetAccount := &models.Account{}
	if err := db.GetDB().Preload("Extra").First(targetAccount, accid).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "load target account error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, buildRelationship(account, targetAccount))
}
