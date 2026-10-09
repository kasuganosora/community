package account

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"strconv"
)

// FollowedTag represents a tag follow relationship.
type FollowedTag struct {
	Id        uint64     `gorm:"primary_key;autoIncrement" json:"id"`
	AccountId uint64     `gorm:"not null;index"             json:"account_id"`
	TagId     uint64     `gorm:"not null;index"             json:"tag_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"    json:"created_at"`
}

func (f *FollowedTag) TableName() string {
	return "followed_tags"
}

// ListFollowedTags handles GET /api/v1/followed_tags.
func ListFollowedTags(c *gin.Context) {
	limit := 100

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Query followed tags
	query := db.GetDB().Table("followed_tags").
		Select("followed_tags.*, tags.name, tags.status_count, tags.account_count").
		Joins("LEFT JOIN tags ON tags.id = followed_tags.tag_id").
		Where("followed_tags.account_id = ?", account.Id).
		Order("followed_tags.id DESC")

	// Pagination
	if maxId := c.Query("max_id"); maxId != "" {
		if id, err := strconv.ParseUint(maxId, 10, 64); err == nil {
			query = query.Where("followed_tags.id < ?", id)
		}
	}

	if sinceId := c.Query("since_id"); sinceId != "" {
		if id, err := strconv.ParseUint(sinceId, 10, 64); err == nil {
			query = query.Where("followed_tags.id > ?", id)
		}
	}

	type FollowedTagResult struct {
		Id           uint64
		AccountId    uint64
		TagId        uint64
		CreatedAt    *time.Time
		Name         string
		StatusCount  uint64
		AccountCount uint64
	}

	followedTags := make([]FollowedTagResult, 0)
	err := query.Limit(limit).Find(&followedTags).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "get followed tags error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	// Build response
	result := make([]gin.H, 0, len(followedTags))

	for _, ft := range followedTags {
		var history []gin.H
		if ft.CreatedAt != nil {
			history = []gin.H{
				{
					"day":      strconv.FormatInt(ft.CreatedAt.Unix(), 10),
					"uses":     strconv.FormatUint(ft.StatusCount, 10),
					"accounts": strconv.FormatUint(ft.AccountCount, 10), //nolint:goconst
				},
			}
		}

		result = append(result, gin.H{
			"name":      ft.Name,                                          //nolint:goconst
			"url":       "https://" + c.Request.Host + "/tags/" + ft.Name, //nolint:goconst
			"history":   history,
			"following": true, //nolint:goconst
		})
	}

	c.JSON(200, result)
}

// GetTag handles GET /api/v1/tags/:id.
func GetTag(c *gin.Context) {
	tagName := c.Param("id")
	if tagName == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "tag name required"})

		return
	}

	// Find tag
	tag := &models.Tag{}

	err := db.GetDB().Where("name = ?", tagName).First(tag).Error
	if err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "tag not found"})

		return
	}

	// Check if current account follows this tag
	following := false

	if c.GetBool("authenticated") {
		account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

		var count int64

		db.GetDB().Table("followed_tags").
			Where("account_id = ? AND tag_id = ?", account.Id, tag.Id).
			Count(&count)

		following = count > 0
	}

	// Build response
	var history []gin.H
	if tag.LastStatusUsedAt != nil {
		history = []gin.H{
			{
				"day":      strconv.FormatInt(tag.LastStatusUsedAt.Unix(), 10),
				"uses":     strconv.FormatUint(tag.StatusCount, 10),
				"accounts": strconv.FormatUint(tag.AccountCount, 10),
			},
		}
	}

	c.JSON(200, gin.H{
		"name":      tag.Name,
		"url":       "https://" + c.Request.Host + "/tags/" + tag.Name,
		"history":   history,
		"following": following,
	})
}

// FollowTag handles POST /api/v1/tags/:id/follow.
func FollowTag(c *gin.Context) {
	tagName := models.NormalizeTagName(c.Param("id"))
	if tagName == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "tag name required"})

		return
	}
	if !models.ValidTagName(tagName) {
		c.AbortWithStatusJSON(422, gin.H{"error": "invalid tag name"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Find or create tag
	tag := &models.Tag{}

	err := db.GetDB().Where("name = ?", tagName).First(tag).Error
	if err != nil {
		// Create tag if not exists
		now := time.Now()

		tag = &models.Tag{
			Name:         tagName,
			StatusCount:  0,
			AccountCount: 0,
			CreatedAt:    &now,
			UpdatedAt:    &now,
		}
		err := db.GetDB().Create(tag).Error
		if err != nil {
			_ = c.Error(errors.Wrap(err, "create tag error"))
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

			return
		}
	}

	// Check if already following
	var count int64

	db.GetDB().Table("followed_tags").
		Where("account_id = ? AND tag_id = ?", account.Id, tag.Id).
		Count(&count)

	if count > 0 {
		// Already following, return tag info
		c.JSON(200, gin.H{
			"name":      tag.Name,
			"url":       "https://" + c.Request.Host + "/tags/" + tag.Name,
			"following": true,
		})

		return
	}

	// Create follow
	now := time.Now()

	followedTag := &FollowedTag{
		AccountId: account.Id,
		TagId:     tag.Id,
		CreatedAt: &now,
	}
	if err := db.GetDB().Create(followedTag).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "follow tag error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, gin.H{
		"name":      tag.Name,
		"url":       "https://" + c.Request.Host + "/tags/" + tag.Name,
		"following": true,
	})
}

// UnfollowTag handles POST /api/v1/tags/:id/unfollow.
func UnfollowTag(c *gin.Context) {
	tagName := c.Param("id")
	if tagName == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "tag name required"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Find tag
	tag := &models.Tag{}

	err := db.GetDB().Where("name = ?", tagName).First(tag).Error
	if err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "tag not found"})

		return
	}

	// Delete follow
	db.GetDB().Table("followed_tags").
		Where("account_id = ? AND tag_id = ?", account.Id, tag.Id).
		Delete(&FollowedTag{})

	c.JSON(200, gin.H{
		"name":      tag.Name,
		"url":       "https://" + c.Request.Host + "/tags/" + tag.Name,
		"following": false,
	})
}
