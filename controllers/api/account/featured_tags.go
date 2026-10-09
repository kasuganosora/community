package account

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

// FeaturedTag represents a featured tag on a profile.
type FeaturedTag struct {
	Id        uint64     `gorm:"primary_key;autoIncrement" json:"id"`
	AccountId uint64     `gorm:"not null;index"             json:"account_id"`
	TagId     uint64     `gorm:"not null;index"             json:"tag_id"`
	Name      string     `gorm:"type:varchar(64);not null"  json:"name"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"    json:"created_at"`
}

func (f *FeaturedTag) TableName() string {
	return "featured_tags"
}

// ListFeaturedTags handles GET /api/v1/featured_tags.
func ListFeaturedTags(c *gin.Context) {
	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Query featured tags
	featuredTags := make([]FeaturedTag, 0)
	err := db.GetDB().Where("account_id = ?", account.Id).Order("id DESC").Find(&featuredTags).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "get featured tags error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	// Build response
	result := make([]gin.H, 0, len(featuredTags))

	for _, ft := range featuredTags {
		// Get tag stats
		tag := &models.Tag{}
		db.GetDB().Where("id = ?", ft.TagId).First(tag)

		var statusesCount int64

		conn := db.GetDB()
		conn.Table("status_contents").
			Joins("JOIN statuses ON statuses.id = status_contents.status_id").
			Where("statuses.account_id = ? AND status_contents.content "+dialect.ILike(conn)+" ?", account.Id, "%#"+ft.Name+"%").
			Count(&statusesCount)

		result = append(result, gin.H{
			"id":             strconv.FormatUint(ft.Id, 10),
			"name":           ft.Name,                                                                      //nolint:goconst
			"url":            "https://" + c.Request.Host + "/@" + account.Username + "/tagged/" + ft.Name, //nolint:goconst
			"statuses_count": statusesCount,                                                                //nolint:goconst
			"last_status_at": nil,                                                                          //nolint:goconst
		})
	}

	c.JSON(200, result)
}

// AccountFeaturedTags handles GET /api/v1/accounts/:id/featured_tags.
func AccountFeaturedTags(c *gin.Context) {
	accountId := c.Param("id")
	if accountId == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "account id required"})

		return
	}

	// Parse account ID
	accId, err := strconv.ParseUint(accountId, 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"}) //nolint:goconst

		return
	}

	// Query featured tags
	featuredTags := make([]FeaturedTag, 0)
	if err := db.GetDB().Where("account_id = ?", accId).Order("id DESC").Find(&featuredTags).Error; err != nil {
		c.JSON(200, []any{})

		return
	}

	// Get account for URL
	account := &models.Account{}
	db.GetDB().First(account, accId)

	// Build response
	result := make([]gin.H, 0, len(featuredTags))
	for _, ft := range featuredTags {
		result = append(result, gin.H{
			"id":             strconv.FormatUint(ft.Id, 10),
			"name":           ft.Name,
			"url":            "https://" + c.Request.Host + "/@" + account.Username + "/tagged/" + ft.Name,
			"statuses_count": 0,
			"last_status_at": nil,
		})
	}

	c.JSON(200, result)
}

// CreateFeaturedTagRequest represents the request for creating a featured tag.
type CreateFeaturedTagRequest struct {
	Name string `binding:"required" json:"name"`
}

// CreateFeaturedTag handles POST /api/v1/featured_tags.
func CreateFeaturedTag(c *gin.Context) {
	var req CreateFeaturedTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "name parameter required"})

		return
	}

	// Normalize tag name (remove # prefix if present)
	name := models.NormalizeTagName(req.Name)
	if name == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "name cannot be empty"})

		return
	}
	if !models.ValidTagName(name) {
		c.AbortWithStatusJSON(422, gin.H{"error": "invalid tag name"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Check if already featured
	var count int64

	db.GetDB().Table("featured_tags").
		Where("account_id = ? AND name = ?", account.Id, name).
		Count(&count)

	if count > 0 {
		c.AbortWithStatusJSON(422, gin.H{"error": "Tag is already featured"})

		return
	}

	// Find or create tag
	tag := &models.Tag{}

	err := db.GetDB().Where("name = ?", name).First(tag).Error
	if err != nil {
		// Create tag if not exists
		now := time.Now()

		tag = &models.Tag{
			Name:      name,
			CreatedAt: &now,
			UpdatedAt: &now,
		}
		err := db.GetDB().Create(tag).Error
		if err != nil {
			_ = c.Error(errors.Wrap(err, "create tag error"))
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

			return
		}
	}

	// Create featured tag
	now := time.Now()

	featuredTag := &FeaturedTag{
		AccountId: account.Id,
		TagId:     tag.Id,
		Name:      name,
		CreatedAt: &now,
	}
	if err := db.GetDB().Create(featuredTag).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create featured tag error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, gin.H{
		"id":             strconv.FormatUint(featuredTag.Id, 10),
		"name":           name,
		"url":            "https://" + c.Request.Host + "/@" + account.Username + "/tagged/" + name,
		"statuses_count": 0,
		"last_status_at": nil,
	})
}

// DeleteFeaturedTag handles DELETE /api/v1/featured_tags/:id.
func DeleteFeaturedTag(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "featured tag id required"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Delete featured tag
	result := db.GetDB().Where("id = ? AND account_id = ?", id, account.Id).Delete(&FeaturedTag{})
	if result.RowsAffected == 0 {
		c.AbortWithStatusJSON(404, gin.H{"error": "Featured tag not found"})

		return
	}

	c.JSON(200, gin.H{})
}

// SuggestFeaturedTags handles GET /api/v1/featured_tags/suggestions.
func SuggestFeaturedTags(c *gin.Context) {
	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Query status content for hashtag patterns
	var contentStrings []string

	db.GetDB().Table("status_contents").
		Joins("JOIN statuses ON statuses.id = status_contents.status_id").
		Where("statuses.account_id = ?", account.Id).
		Where("status_contents.content IS NOT NULL").
		Order("statuses.created_at DESC").
		Limit(100).
		Pluck("status_contents.content", &contentStrings)

	// Count tag usage
	tagCounts := make(map[string]int64)

	for _, content := range contentStrings {
		tags := models.MatchTags(content)
		for _, tag := range tags {
			tagCounts[tag]++
		}
	}

	// Get already featured tags
	featuredTags := make([]FeaturedTag, 0)
	db.GetDB().Where("account_id = ?", account.Id).Find(&featuredTags)

	featuredSet := make(map[string]bool)
	for _, ft := range featuredTags {
		featuredSet["#"+ft.Name] = true
	}

	// Build suggestions (exclude already featured)
	result := make([]gin.H, 0)

	for name, count := range tagCounts {
		if featuredSet[name] {
			continue
		}
		// Remove # prefix for display
		displayName := name
		if len(displayName) > 0 && displayName[0] == '#' {
			displayName = displayName[1:]
		}

		result = append(result, gin.H{
			"name":  displayName,
			"count": count,
		})
		if len(result) >= 10 {
			break
		}
	}

	c.JSON(200, result)
}
