package account

import (
	"github.com/kasuganosora/community/controllers"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// FeaturedTag represents a featured tag on a profile. The table is created
// by migrate/scripts.FeaturedTagsAndMissingTablesScript.
type FeaturedTag struct {
	Id        uint64     `gorm:"primary_key;autoIncrement"                                                json:"id"`
	AccountId uint64     `gorm:"not null;index;uniqueIndex:uidx_featured_tags_account_tag,priority:1" json:"account_id"`
	TagId     uint64     `gorm:"not null;index;uniqueIndex:uidx_featured_tags_account_tag,priority:2" json:"tag_id"`
	Name      string     `gorm:"type:varchar(64);not null"                                                 json:"name"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"                                                   json:"created_at"`
}

func (f *FeaturedTag) TableName() string {
	return "featured_tags"
}

// featuredTagStats is the number of the account's public/unlisted statuses
// using the tag and the date of the latest one.
type featuredTagStats struct {
	count  int64
	lastAt *time.Time
}

func loadFeaturedTagStats(conn *gorm.DB, accountID, tagID uint64) (featuredTagStats, error) {
	var stats featuredTagStats
	base := func() *gorm.DB {
		return conn.Table("statuses").
			Joins("JOIN status_tags ON status_tags.status_id = statuses.id").
			Where("statuses.account_id = ? AND status_tags.tag_id = ?", accountID, tagID).
			Where("statuses.deleted_at IS NULL").
			Where("statuses.visibility IN ?", []int{models.StatusVisibilityPublic, models.StatusVisibilityUnlisted})
	}
	if err := base().Count(&stats.count).Error; err != nil {
		return stats, err
	}
	if stats.count == 0 {
		return stats, nil
	}
	var latest struct{ CreatedAt *time.Time }
	if err := base().Select("statuses.created_at").Order("statuses.created_at DESC").Limit(1).Scan(&latest).Error; err != nil {
		return stats, err
	}
	stats.lastAt = latest.CreatedAt
	return stats, nil
}

func featuredTagJSON(c *gin.Context, account *models.Account, ft *FeaturedTag, stats featuredTagStats) gin.H {
	var lastStatusAt any
	if stats.lastAt != nil {
		lastStatusAt = stats.lastAt.UTC().Format("2006-01-02")
	}
	return gin.H{
		"id":             strconv.FormatUint(ft.Id, 10),
		"name":           ft.Name,                                                                      //nolint:goconst
		"url":            "https://" + c.Request.Host + "/@" + account.Username + "/tagged/" + ft.Name, //nolint:goconst
		"statuses_count": stats.count,                                                                  //nolint:goconst
		"last_status_at": lastStatusAt,                                                                 //nolint:goconst
	}
}

// renderFeaturedTags answers with account's featured tags, or 500 on a
// database error (a missing table used to be reported as an empty list).
func renderFeaturedTags(c *gin.Context, account *models.Account) {
	conn := db.GetDB().WithContext(c.Request.Context())
	featuredTags := make([]*FeaturedTag, 0)
	if err := conn.Where("account_id = ?", account.Id).Order("id DESC").Find(&featuredTags).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get featured tags error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	result := make([]gin.H, 0, len(featuredTags))
	for _, ft := range featuredTags {
		stats, err := loadFeaturedTagStats(conn, account.Id, ft.TagId)
		if err != nil {
			_ = c.Error(errors.Wrap(err, "count featured tag statuses error"))
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

			return
		}
		result = append(result, featuredTagJSON(c, account, ft, stats))
	}

	c.JSON(200, result)
}

// ListFeaturedTags handles GET /api/v1/featured_tags.
func ListFeaturedTags(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	renderFeaturedTags(c, account)
}

// AccountFeaturedTags handles GET /api/v1/accounts/:id/featured_tags.
func AccountFeaturedTags(c *gin.Context) {
	accId, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accId == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"}) //nolint:goconst

		return
	}

	account := &models.Account{}
	if err := db.GetDB().WithContext(c.Request.Context()).First(account, accId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "Record not found"})

			return
		}
		_ = c.Error(errors.Wrap(err, "get account error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	renderFeaturedTags(c, account)
}

// CreateFeaturedTagRequest represents the request for creating a featured tag.
type CreateFeaturedTagRequest struct {
	Name string `binding:"required" json:"name"`
}

// CreateFeaturedTag handles POST /api/v1/featured_tags.
func CreateFeaturedTag(c *gin.Context) {
	var req CreateFeaturedTagRequest
	if err := controllers.BindBody(c, &req); err != nil {
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

	// Find or create the tag case-insensitively; the tag keeps its
	// first-seen spelling.
	tag, err := models.FindOrCreateTag(db.GetDB(), name)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "create tag error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}
	name = tag.Name

	// Check if already featured (by tag, so case variants count as one)
	var count int64

	if err := db.GetDB().Model(&FeaturedTag{}).
		Where("account_id = ? AND tag_id = ?", account.Id, tag.Id).
		Count(&count).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "check featured tag error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	if count > 0 {
		c.AbortWithStatusJSON(422, gin.H{"error": "Tag is already featured"})

		return
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

	stats, err := loadFeaturedTagStats(db.GetDB(), account.Id, tag.Id)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "count featured tag statuses error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, featuredTagJSON(c, account, featuredTag, stats))
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

	featuredID, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "Featured tag not found"})

		return
	}

	// Delete featured tag
	result := db.GetDB().Where("id = ? AND account_id = ?", featuredID, account.Id).Delete(&FeaturedTag{})
	if result.Error != nil {
		_ = c.Error(errors.Wrap(result.Error, "delete featured tag error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}
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

	if err := db.GetDB().Table("status_contents").
		Joins("JOIN statuses ON statuses.id = status_contents.status_id").
		Where("statuses.account_id = ?", account.Id).
		Where("status_contents.content IS NOT NULL").
		Order("statuses.created_at DESC").
		Limit(100).
		Pluck("status_contents.content", &contentStrings).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get statuses for featured tag suggestions error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Count tag usage case-insensitively; show the first-seen spelling.
	tagCounts := make(map[string]int64)
	tagDisplay := make(map[string]string)

	for _, content := range contentStrings {
		tags := models.MatchTags(content)
		for _, tag := range tags {
			key := models.TagKey(tag)
			if _, ok := tagDisplay[key]; !ok {
				tagDisplay[key] = tag
			}
			tagCounts[key]++
		}
	}

	// Get already featured tags
	featuredTags := make([]FeaturedTag, 0)
	if err := db.GetDB().Where("account_id = ?", account.Id).Find(&featuredTags).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get featured tags error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	featuredSet := make(map[string]bool)
	for _, ft := range featuredTags {
		featuredSet[models.TagKey(ft.Name)] = true
	}

	// Build suggestions (exclude already featured)
	result := make([]gin.H, 0)

	for key, count := range tagCounts {
		if featuredSet[key] {
			continue
		}
		// Remove # prefix for display
		displayName := tagDisplay[key]
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
