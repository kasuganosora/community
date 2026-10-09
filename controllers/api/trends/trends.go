package trends

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

// ListTags handles GET /api/v1/trends/tags.
func ListTags(c *gin.Context) {
	limit := 10

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 20 {
			limit = parsed
		}
	}

	offset := 0

	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// 尝试从缓存读取热门标签（TTL 2 分钟）
	tagsCacheKey := fmt.Sprintf("%s:tags:%d:%d", cache.KeyTrendingTags, limit, offset)
	var cachedResult []gin.H
	if err := cache.GetCache().Get(c.Request.Context(), tagsCacheKey, &cachedResult); err == nil {
		c.JSON(200, cachedResult)

		return
	}

	// Query trending tags (based on recent usage)
	// Use last_status_used_at to determine trending
	query := db.GetDB().Model(&models.Tag{}).
		Where("status_count > 0").
		Where("last_status_used_at > ?", time.Now().AddDate(0, 0, -7)). // Last 7 days
		Order("status_count DESC, last_status_used_at DESC")

	tags := make([]*models.Tag, 0)
	err := query.Limit(limit).Offset(offset).Find(&tags).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "get trending tags error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Build response
	result := make([]gin.H, 0, len(tags))
	for _, tag := range tags {
		result = append(result, gin.H{
			"name": tag.Name,
			"url":  "https://" + c.Request.Host + "/tags/" + tag.Name,
			"history": []gin.H{
				{
					"day":        strconv.FormatInt(tag.LastStatusUsedAt.Unix(), 10),
					"uses":       strconv.FormatUint(tag.StatusCount, 10),
					"accounts":   strconv.FormatUint(tag.AccountCount, 10),
					"_version":   "1",
					"_createdAt": tag.CreatedAt.Format(time.RFC3339),
					"_updatedAt": tag.UpdatedAt.Format(time.RFC3339),
				},
			},
		})
	}

	// 写入缓存
	_ = cache.GetCache().Set(c.Request.Context(), tagsCacheKey, result, cache.ShortExpiration)

	c.JSON(200, result)
}
func ListStatuses(c *gin.Context) {
	limit := 10

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 20 {
			limit = parsed
		}
	}

	offset := 0

	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Query trending statuses (based on reblogs and favourites count)
	query := db.GetDB().Model(&models.Status{}).
		Where("visibility = ?", models.StatusVisibilityPublic).
		Where("in_reply_to_id IS NULL OR in_reply_to_id = 0").
		Where("created_at > ?", time.Now().AddDate(0, 0, -7)). // Last 7 days
		Preload("Account").
		Preload("Account.Emojis").
		Preload("Account.Extra").
		Preload("Emojis").
		Order("(reblogs_count + favourites_count) DESC, created_at DESC")

	statuses := make([]*models.Status, 0)
	err := query.Limit(limit).Offset(offset).Find(&statuses).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "get trending statuses error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Serialize statuses
	serializedStatuses, _ := serializer.ToStatuses(models.Statuses(statuses), nil)

	c.JSON(200, serializedStatuses)
}

// ListLinks handles GET /api/v1/trends/links.
func ListLinks(c *gin.Context) {
	limit := 10

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 20 {
			limit = parsed
		}
	}

	offset := 0

	if o := c.Query("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Link trends come from a status_cards table that nothing in this
	// server writes yet, so it is not created by the migrations: without
	// it there are no trending links. Any other database error is a 500.
	if !db.GetDB().Migrator().HasTable("status_cards") {
		c.JSON(200, []any{})

		return
	}

	// Aggregate every non-grouped column so the query is valid on
	// PostgreSQL and MySQL ONLY_FULL_GROUP_BY as well as SQLite.
	query := db.GetDB().Table("status_cards").
		Select("url, MAX(title) AS title, MAX(description) AS description, MAX(image_url) AS image_url, MAX(provider_name) AS provider_name, COUNT(*) AS shares_count").
		Where("created_at > ?", time.Now().AddDate(0, 0, -7)).
		Group("url").
		Order("shares_count DESC").
		Limit(limit).
		Offset(offset)

	type LinkResult struct {
		URL          string `gorm:"column:url"`
		Title        string `gorm:"column:title"`
		Description  string `gorm:"column:description"`
		ImageURL     string `gorm:"column:image_url"`
		ProviderName string `gorm:"column:provider_name"`
		SharesCount  int    `gorm:"column:shares_count"`
	}

	links := make([]LinkResult, 0)
	err := query.Find(&links).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "get trending links error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	// Build response
	result := make([]gin.H, 0, len(links))
	for _, link := range links {
		result = append(result, gin.H{
			"url":           link.URL,
			"title":         link.Title,
			"description":   link.Description,
			"image":         link.ImageURL,
			"provider_name": link.ProviderName,
			"published_at":  nil,
			"history": []gin.H{
				{
					"day":      strconv.FormatInt(time.Now().Unix(), 10),
					"uses":     strconv.Itoa(link.SharesCount),
					"accounts": strconv.Itoa(link.SharesCount),
				},
			},
		})
	}

	c.JSON(200, result)
}
