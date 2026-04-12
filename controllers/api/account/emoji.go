package account

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// GetCustomEmojis handles GET /api/v1/custom_emojis
func GetCustomEmojis(c *gin.Context) {
	// Query custom emojis
	emojis := make([]*models.Emoji, 0)
	db.GetDB().Where("visible_in_picker = ?", true).Order("shortcode ASC").Find(&emojis)

	// Build response
	result := make([]gin.H, 0, len(emojis))
	for _, emoji := range emojis {
		result = append(result, gin.H{
			"shortcode":         emoji.Shortcode,
			"url":               emoji.Url,
			"static_url":        emoji.StaticUrl,
			"visible_in_picker": emoji.VisibleInPicker,
			"category":          emoji.Category,
		})
	}

	c.JSON(200, result)
}

// Emoji represents a custom emoji for lookup
type Emoji struct {
	Shortcode       string `gorm:"type:varchar(64);not null;index" json:"shortcode"`
	Url             string `gorm:"type:varchar(255)" json:"url"`
	StaticUrl       string `gorm:"type:varchar(255)" json:"static_url"`
	VisibleInPicker bool   `gorm:"not null;default:true" json:"visible_in_picker"`
	Category        string `gorm:"type:varchar(64)" json:"category"`
}

// RenderEmojis replaces emoji shortcodes with image tags
func RenderEmojis(content string, emojis []*models.Emoji) string {
	for _, emoji := range emojis {
		re := regexp.MustCompile(":" + regexp.QuoteMeta(emoji.Shortcode) + ":")
		content = re.ReplaceAllString(content, `<img src="`+emoji.Url+`" alt=":`+emoji.Shortcode+`:" title=":`+emoji.Shortcode+`:" class="emoji">`)
	}
	return content
}

// ParseEmojiFromText extracts emoji shortcodes from text
func ParseEmojiFromText(text string) []string {
	re := regexp.MustCompile(`:([a-zA-Z0-9_]+):`)
	matches := re.FindAllStringSubmatch(text, -1)
	emojis := make([]string, 0)
	for _, match := range matches {
		if len(match) > 1 {
			emojis = append(emojis, strings.ToLower(match[1]))
		}
	}
	return emojis
}
