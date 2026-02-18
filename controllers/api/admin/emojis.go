package admin

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

// ListCustomEmojis handles GET /api/v1/admin/custom_emojis
func ListCustomEmojis(c *gin.Context) {
	limit := 40
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	query := db.GetDB().Model(&models.Emoji{})

	if shortcode := c.Query("shortcode"); shortcode != "" {
		query = query.Where("shortcode LIKE ?", "%"+shortcode+"%")
	}

	if category := c.Query("category"); category != "" {
		query = query.Where("category = ?", category)
	}

	if visible := c.Query("visible_in_picker"); visible != "" {
		if visible == "true" {
			query = query.Where("visible_in_picker = ?", true)
		} else {
			query = query.Where("visible_in_picker = ?", false)
		}
	}

	if origin := c.Query("origin"); origin != "" {
		if origin == "local" {
			query = query.Where("domain = ''")
		} else if origin == "remote" {
			query = query.Where("domain != ''")
		}
	}

	emojis := make([]*models.Emoji, 0)
	if err := query.Order("shortcode ASC").Limit(limit).Find(&emojis).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "list emojis error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	result := make([]gin.H, 0, len(emojis))
	for _, emoji := range emojis {
		result = append(result, serializeAdminEmoji(emoji))
	}

	c.JSON(200, result)
}

// GetCustomEmoji handles GET /api/v1/admin/custom_emojis/:id
func GetCustomEmoji(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "emoji id required"})
		return
	}

	emoji := &models.Emoji{}
	if err := db.GetDB().First(emoji, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "emoji not found"})
		return
	}

	c.JSON(200, serializeAdminEmoji(emoji))
}

// CreateEmojiRequest represents a create emoji request
type CreateEmojiRequest struct {
	Shortcode       string `json:"shortcode" binding:"required"`
	Category        string `json:"category"`
	Image           string `json:"image"`
	VisibleInPicker bool   `json:"visible_in_picker"`
}

// CreateCustomEmoji handles POST /api/v1/admin/custom_emojis
func CreateCustomEmoji(c *gin.Context) {
	var req CreateEmojiRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid request"})
		return
	}

	var count int64
	db.GetDB().Model(&models.Emoji{}).Where("shortcode = ? AND domain = ''", req.Shortcode).Count(&count)
	if count > 0 {
		c.AbortWithStatusJSON(422, gin.H{"error": "shortcode already taken"})
		return
	}

	emoji := &models.Emoji{
		Shortcode:       req.Shortcode,
		Url:             req.Image,
		StaticUrl:       req.Image,
		VisibleInPicker: req.VisibleInPicker,
		Category:        req.Category,
		Domain:          "",
	}

	if err := db.GetDB().Create(emoji).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create emoji error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, serializeAdminEmoji(emoji))
}

// UpdateEmojiRequest represents an update emoji request
type UpdateEmojiRequest struct {
	Shortcode       string `json:"shortcode"`
	Category        string `json:"category"`
	Image           string `json:"image"`
	VisibleInPicker *bool  `json:"visible_in_picker"`
}

// UpdateCustomEmoji handles PUT /api/v1/admin/custom_emojis/:id
func UpdateCustomEmoji(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "emoji id required"})
		return
	}

	emoji := &models.Emoji{}
	if err := db.GetDB().First(emoji, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "emoji not found"})
		return
	}

	var req UpdateEmojiRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid request"})
		return
	}

	if req.Shortcode != "" {
		emoji.Shortcode = req.Shortcode
	}
	if req.Category != "" {
		emoji.Category = req.Category
	}
	if req.Image != "" {
		emoji.Url = req.Image
		emoji.StaticUrl = req.Image
	}
	if req.VisibleInPicker != nil {
		emoji.VisibleInPicker = *req.VisibleInPicker
	}

	if err := db.GetDB().Save(emoji).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "update emoji error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, serializeAdminEmoji(emoji))
}

// DeleteCustomEmoji handles DELETE /api/v1/admin/custom_emojis/:id
func DeleteCustomEmoji(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "emoji id required"})
		return
	}

	emoji := &models.Emoji{}
	if err := db.GetDB().First(emoji, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "emoji not found"})
		return
	}

	if err := db.GetDB().Delete(emoji).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "delete emoji error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, gin.H{})
}

// CopyEmojiRequest represents a copy emoji request
type CopyEmojiRequest struct {
	Shortcode string `json:"shortcode" binding:"required"`
}

// CopyCustomEmoji handles POST /api/v1/admin/custom_emojis/:id/copy
func CopyCustomEmoji(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "emoji id required"})
		return
	}

	emoji := &models.Emoji{}
	if err := db.GetDB().First(emoji, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "emoji not found"})
		return
	}

	var req CopyEmojiRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid request"})
		return
	}

	var count int64
	db.GetDB().Model(&models.Emoji{}).Where("shortcode = ? AND domain = ''", req.Shortcode).Count(&count)
	if count > 0 {
		c.AbortWithStatusJSON(422, gin.H{"error": "shortcode already taken"})
		return
	}

	newEmoji := &models.Emoji{
		Shortcode:       req.Shortcode,
		Url:             emoji.Url,
		StaticUrl:       emoji.StaticUrl,
		VisibleInPicker: emoji.VisibleInPicker,
		Category:        emoji.Category,
		Domain:          "",
	}

	if err := db.GetDB().Create(newEmoji).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "copy emoji error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, serializeAdminEmoji(newEmoji))
}

// EnableCustomEmoji handles POST /api/v1/admin/custom_emojis/:id/enable
func EnableCustomEmoji(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "emoji id required"})
		return
	}

	emoji := &models.Emoji{}
	if err := db.GetDB().First(emoji, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "emoji not found"})
		return
	}

	emoji.VisibleInPicker = true
	db.GetDB().Save(emoji)

	c.JSON(200, serializeAdminEmoji(emoji))
}

// DisableCustomEmoji handles POST /api/v1/admin/custom_emojis/:id/disable
func DisableCustomEmoji(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "emoji id required"})
		return
	}

	emoji := &models.Emoji{}
	if err := db.GetDB().First(emoji, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "emoji not found"})
		return
	}

	emoji.VisibleInPicker = false
	db.GetDB().Save(emoji)

	c.JSON(200, serializeAdminEmoji(emoji))
}

func serializeAdminEmoji(emoji *models.Emoji) gin.H {
	var domain interface{}
	if emoji.Domain != "" {
		domain = emoji.Domain
	}

	createdAt := time.Unix(emoji.CreatedAt, 0).Format("2006-01-02T15:04:05.000Z")
	updatedAt := time.Unix(emoji.UpdatedAt, 0).Format("2006-01-02T15:04:05.000Z")

	return gin.H{
		"id":                strconv.FormatUint(emoji.Id, 10),
		"shortcode":         emoji.Shortcode,
		"url":               emoji.Url,
		"static_url":        emoji.StaticUrl,
		"visible_in_picker": emoji.VisibleInPicker,
		"category":          emoji.Category,
		"domain":            domain,
		"disabled":          !emoji.VisibleInPicker,
		"updated_at":        updatedAt,
		"created_at":        createdAt,
		"total_file_size":   0,
		"content_type":      "image/png",
		"uri":               config.C.AppBaseURL + "/emojis/" + emoji.Shortcode,
	}
}

// Ensure config is loaded
var _ = config.C
