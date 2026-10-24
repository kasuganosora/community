package account

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

// emojiCatalogueRev is the public local set (domain empty, not disabled).
// Adding, deleting, disabling, or editing a row changes the ETag; an
// unchanged set answers 304 without loading every emoji.
type emojiCatalogueRev struct {
	Count      int64
	MaxID      int64
	MaxUpdated int64
	Checksum   int64
}

// GetCustomEmojis handles GET /api/v1/custom_emojis.
func GetCustomEmojis(c *gin.Context) {
	var rev emojiCatalogueRev
	err := db.GetDB().Model(&models.Emoji{}).
		Where("domain = ? AND disabled = ?", "", false).
		Select("COUNT(*) AS count, COALESCE(MAX(id), 0) AS max_id, COALESCE(MAX(updated_at), 0) AS max_updated, COALESCE(SUM(updated_at), 0) AS checksum").
		Scan(&rev).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load custom emojis"})
		return
	}
	etag := fmt.Sprintf(`W/"%d.%d.%d.%d"`, rev.Count, rev.MaxID, rev.MaxUpdated, rev.Checksum)
	c.Header("ETag", etag)
	c.Header("Cache-Control", "public, no-cache")
	if customEmojiNoneMatch(c.GetHeader("If-None-Match"), etag) {
		// Status alone stays inside Gin; WriteHeaderNow is what the client sees.
		c.AbortWithStatus(http.StatusNotModified)
		return
	}

	emojis := make([]*models.Emoji, 0)
	if err := db.GetDB().Where("domain = '' AND disabled = ?", false).Order("shortcode ASC").Find(&emojis).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load custom emojis"})
		return
	}

	// Same entity as nested status emojis: absolute URLs, null category.
	c.JSON(http.StatusOK, serializer.ToEmojis(emojis))
}

// customEmojiNoneMatch reports whether If-None-Match names the current tag.
// Comparison ignores a weak prefix so a compressed response still matches.
func customEmojiNoneMatch(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	want := etagToken(etag)
	if want == "" {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		if etagToken(part) == want {
			return true
		}
	}
	return false
}

func etagToken(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "W/")
	value = strings.TrimPrefix(value, "w/")
	return strings.TrimSpace(value)
}

// Emoji represents a custom emoji for lookup.
type Emoji struct {
	Shortcode       string `gorm:"type:varchar(64);not null;index" json:"shortcode"`
	Url             string `gorm:"type:varchar(255)"               json:"url"`
	StaticUrl       string `gorm:"type:varchar(255)"               json:"static_url"`
	VisibleInPicker bool   `gorm:"not null;default:true"           json:"visible_in_picker"`
	Category        string `gorm:"type:varchar(64)"                json:"category"`
}

// RenderEmojis replaces emoji shortcodes with image tags.
func RenderEmojis(content string, emojis []*models.Emoji) string {
	for _, emoji := range emojis {
		re := regexp.MustCompile(":" + regexp.QuoteMeta(emoji.Shortcode) + ":")
		content = re.ReplaceAllString(content, `<img src="`+emoji.Url+`" alt=":`+emoji.Shortcode+`:" title=":`+emoji.Shortcode+`:" class="emoji">`)
	}

	return content
}

// ParseEmojiFromText extracts emoji shortcodes from text.
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
