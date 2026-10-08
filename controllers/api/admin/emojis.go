package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var emojiShortcode = regexp.MustCompile("^[a-zA-Z0-9_]{1,36}$")

// Pointer fields preserve the difference between omission and explicitly clearing
// metadata. Arbitrary URLs are never accepted as validated image assets.
type CreateEmojiRequest struct {
	ReactionRoleIDs []string `json:"reaction_role_ids"`
	Shortcode       string   `json:"shortcode"`
	ImageID         string   `json:"image_id"`
	Aliases         []string `json:"aliases"`
	Category        string   `json:"category"`
	License         string   `json:"license"`
	LocalOnly       bool     `json:"local_only"`
	IsSensitive     bool     `json:"is_sensitive"`
	VisibleInPicker *bool    `json:"visible_in_picker"`
	Disabled        bool     `json:"disabled"`
}
type UpdateEmojiRequest struct {
	ReactionRoleIDs *[]string `json:"reaction_role_ids"`
	Shortcode       *string   `json:"shortcode"`
	ImageID         *string   `json:"image_id"`
	Aliases         *[]string `json:"aliases"`
	Category        *string   `json:"category"`
	License         *string   `json:"license"`
	LocalOnly       *bool     `json:"local_only"`
	IsSensitive     *bool     `json:"is_sensitive"`
	VisibleInPicker *bool     `json:"visible_in_picker"`
	Disabled        *bool     `json:"disabled"`
}
type CopyEmojiRequest struct {
	Shortcode string `json:"shortcode"`
}
type emojiAPIError struct {
	code    int
	message string
}

func (e emojiAPIError) Error() string { return e.message }
func emojiError(c *gin.Context, err error) {
	var rejected emojiAPIError
	switch {
	case errors.As(err, &rejected):
		c.AbortWithStatusJSON(rejected.code, gin.H{"error": rejected.message})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.AbortWithStatusJSON(404, gin.H{"error": "emoji or upload not found"})
	default:
		// Drivers expose different duplicate-key errors, but all must become a
		// validation conflict, including concurrent requests racing the precheck.
		message := strings.ToLower(err.Error())
		if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(message, "duplicate") || strings.Contains(message, "unique constraint") {
			c.AbortWithStatusJSON(422, gin.H{"error": "shortcode already taken"})
			return
		}
		_ = c.Error(err)
		c.AbortWithStatusJSON(500, gin.H{"error": "emoji operation failed"})
	}
}
func decodeEmojiJSON(c *gin.Context, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid request: " + err.Error()})
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		c.AbortWithStatusJSON(400, gin.H{"error": "one JSON object required"})
		return false
	}
	return true
}
func emojiID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid emoji id"})
		return 0, false
	}
	return id, true
}
func normalizeEmoji(e *models.Emoji) error {
	if !emojiShortcode.MatchString(e.Shortcode) {
		return emojiAPIError{422, "shortcode must contain 1–36 ASCII letters, digits or underscores"}
	}
	if utf8.RuneCountInString(e.Category) > 128 || utf8.RuneCountInString(e.License) > 2048 {
		return emojiAPIError{422, "category or license is too long"}
	}
	if len(e.Aliases) > 50 {
		return emojiAPIError{422, "at most 50 aliases are allowed"}
	}
	aliases := make([]string, 0, len(e.Aliases))
	seen := map[string]bool{}
	for _, alias := range e.Aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || utf8.RuneCountInString(alias) > 100 {
			return emojiAPIError{422, "aliases must contain 1–100 characters"}
		}
		if !seen[alias] {
			aliases = append(aliases, alias)
			seen[alias] = true
		}
	}
	e.Aliases = aliases
	return nil
}
func ListCustomEmojis(c *gin.Context) {
	page, limit := 1, 40
	for key, target := range map[string]*int{"page": &page, "limit": &limit} {
		if value := c.Query(key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || (key == "limit" && n > 100) || (key == "page" && n > 100000) {
				c.JSON(422, gin.H{"error": "invalid pagination"})
				return
			}
			*target = n
		}
	}
	query := db.GetDB().Model(&models.Emoji{})
	if search := c.Query("q"); search != "" {
		like := dialect.ILike(query)
		query = query.Where("shortcode "+like+" ? OR aliases "+like+" ?", "%"+search+"%", "%"+search+"%")
	} else if shortcode := c.Query("shortcode"); shortcode != "" {
		query = query.Where("shortcode "+dialect.ILike(query)+" ?", "%"+shortcode+"%")
	}

	for _, field := range []string{"category", "domain"} {
		if value, exists := c.GetQuery(field); exists {
			query = query.Where(field+" = ?", value)
		}
	}
	for _, field := range []string{"visible_in_picker", "disabled"} {
		if value, exists := c.GetQuery(field); exists {
			b, err := strconv.ParseBool(value)
			if err != nil {
				c.JSON(422, gin.H{"error": "invalid boolean filter"})
				return
			}
			query = query.Where(field+" = ?", b)
		}
	}
	switch c.Query("origin") {
	case "local":
		query = query.Where("domain = ''")
	case "remote":
		query = query.Where("domain <> ''")
	case "", "all":
	default:
		c.JSON(422, gin.H{"error": "invalid origin"})
		return
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		emojiError(c, err)
		return
	}
	var emojis []*models.Emoji
	if err := query.Order("shortcode ASC").Order("id ASC").Limit(limit).Offset((page - 1) * limit).Find(&emojis).Error; err != nil {
		emojiError(c, err)
		return
	}
	result := make([]gin.H, 0, len(emojis))
	for _, e := range emojis {
		result = append(result, serializeAdminEmoji(e))
	}
	c.Header("X-Total-Count", strconv.FormatInt(count, 10))
	c.JSON(200, result)
}
func GetCustomEmoji(c *gin.Context) {
	id, ok := emojiID(c)
	if !ok {
		return
	}
	var e models.Emoji
	if err := db.GetDB().First(&e, id).Error; err != nil {
		emojiError(c, err)
		return
	}
	c.JSON(200, serializeAdminEmoji(&e))
}
func UploadCustomEmoji(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (2<<20)+(64<<10))
	if err := c.Request.ParseMultipartForm(2 << 20); err != nil {
		c.JSON(422, gin.H{"error": "invalid or oversized image upload"})
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	header, err := c.FormFile("file")
	if err != nil {
		c.JSON(422, gin.H{"error": "file is required"})
		return
	}
	file, err := header.Open()
	if err != nil {
		emojiError(c, err)
		return
	}
	defer file.Close()
	asset, err := emojiasset.StoreEmojiImage(c.Request.Context(), file, header.Header.Get("Content-Type"))
	if err != nil {
		c.JSON(422, gin.H{"error": err.Error()})
		return
	}
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		_ = emojiasset.DeleteAsset(asset)
		emojiError(c, err)
		return
	}
	upload := &models.EmojiUpload{Id: hex.EncodeToString(random), AccountId: controllers.MustGetAccount(c).Id, Url: asset.URL, StaticUrl: asset.StaticURL, ContentType: asset.ContentType, FileSize: asset.Size, Width: asset.Width, Height: asset.Height}
	if err := db.GetDB().Create(upload).Error; err != nil {
		_ = emojiasset.DeleteAsset(asset)
		emojiError(c, err)
		return
	}
	c.JSON(200, upload)
}
func applyEmojiUpload(tx *gorm.DB, e *models.Emoji, id string, owner uint64) error {
	if id == "" {
		return emojiAPIError{422, "image_id from a validated upload is required"}
	}
	var upload models.EmojiUpload
	if err := tx.Where("id = ? AND account_id = ?", id, owner).First(&upload).Error; err != nil {
		return err
	}
	e.Url, e.StaticUrl, e.ContentType, e.FileSize, e.Width, e.Height = upload.Url, upload.StaticUrl, upload.ContentType, upload.FileSize, upload.Width, upload.Height
	return nil
}
func CreateCustomEmoji(c *gin.Context) {
	var req CreateEmojiRequest
	if !decodeEmojiJSON(c, &req) {
		return
	}
	visible := true
	if req.VisibleInPicker != nil {
		visible = *req.VisibleInPicker
	}
	e := models.Emoji{Shortcode: req.Shortcode, Aliases: req.Aliases, Category: req.Category, License: req.License, LocalOnly: req.LocalOnly, IsSensitive: req.IsSensitive, VisibleInPicker: visible, Disabled: req.Disabled}
	if err := normalizeEmoji(&e); err != nil {
		emojiError(c, err)
		return
	}
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := applyReactionRoles(tx, &e, req.ReactionRoleIDs); err != nil {
			return err
		}
		if err := applyEmojiUpload(tx, &e, req.ImageID, controllers.MustGetAccount(c).Id); err != nil {
			return err
		}
		return tx.Create(&e).Error
	})
	if err != nil {
		emojiError(c, err)
		return
	}
	c.JSON(200, serializeAdminEmoji(&e))
}
func UpdateCustomEmoji(c *gin.Context) {
	id, ok := emojiID(c)
	if !ok {
		return
	}
	var req UpdateEmojiRequest
	if !decodeEmojiJSON(c, &req) {
		return
	}
	var e models.Emoji
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&e, id).Error; err != nil {
			return err
		}
		if e.Domain != "" && (req.Shortcode != nil || req.ImageID != nil || req.Aliases != nil || req.Category != nil || req.License != nil || req.LocalOnly != nil || req.IsSensitive != nil || req.ReactionRoleIDs != nil) {
			return emojiAPIError{422, "copy a remote emoji locally before changing its metadata"}
		}
		if req.ReactionRoleIDs != nil {
			if err := applyReactionRoles(tx, &e, *req.ReactionRoleIDs); err != nil {
				return err
			}
		}
		if req.Shortcode != nil {
			e.Shortcode = *req.Shortcode
		}
		if req.Category != nil {
			e.Category = *req.Category
		}
		if req.License != nil {
			e.License = *req.License
		}
		if req.Aliases != nil {
			e.Aliases = *req.Aliases
		}
		if req.LocalOnly != nil {
			e.LocalOnly = *req.LocalOnly
		}
		if req.IsSensitive != nil {
			e.IsSensitive = *req.IsSensitive
		}
		if req.VisibleInPicker != nil {
			e.VisibleInPicker = *req.VisibleInPicker
		}
		if req.Disabled != nil {
			e.Disabled = *req.Disabled
		}
		if req.ImageID != nil {
			if err := applyEmojiUpload(tx, &e, *req.ImageID, controllers.MustGetAccount(c).Id); err != nil {
				return err
			}
		}
		if err := normalizeEmoji(&e); err != nil {
			return err
		}
		return tx.Save(&e).Error
	})
	if err != nil {
		emojiError(c, err)
		return
	}
	c.JSON(200, serializeAdminEmoji(&e))
}
func deleteEmoji(tx *gorm.DB, e *models.Emoji) error {
	// Preserve the tombstone and old associations while freeing the active
	// shortcode/domain identity for a genuinely new emoji.
	if err := tx.Model(e).UpdateColumn("domain", fmt.Sprintf("deleted:%d", e.Id)).Error; err != nil {
		return err
	}
	return tx.Delete(e).Error
}
func DeleteCustomEmoji(c *gin.Context) {
	id, ok := emojiID(c)
	if !ok {
		return
	}
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		var e models.Emoji
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&e, id).Error; err != nil {
			return err
		}
		return deleteEmoji(tx, &e)
	})
	if err != nil {
		emojiError(c, err)
		return
	}
	c.JSON(200, gin.H{})
}
func CopyCustomEmoji(c *gin.Context) {
	id, ok := emojiID(c)
	if !ok {
		return
	}
	var req CopyEmojiRequest
	if !decodeEmojiJSON(c, &req) {
		return
	}
	var original models.Emoji
	if err := db.GetDB().First(&original, id).Error; err != nil {
		emojiError(c, err)
		return
	}
	if original.Domain == "" {
		c.JSON(422, gin.H{"error": "only remote emojis can be copied"})
		return
	}
	e := original
	e.Id = 0
	e.Domain = ""
	e.CreatedAt = 0
	e.UpdatedAt = 0
	e.Disabled = false
	e.ReactionRoleIDs = nil
	e.VisibleInPicker = true
	if req.Shortcode != "" {
		e.Shortcode = req.Shortcode
	}
	if err := normalizeEmoji(&e); err != nil {
		emojiError(c, err)
		return
	}
	var count int64
	if err := db.GetDB().Model(&models.Emoji{}).Where("shortcode = ? AND domain = ''", e.Shortcode).Count(&count).Error; err != nil {
		emojiError(c, err)
		return
	}
	if count > 0 {
		emojiError(c, emojiAPIError{422, "shortcode already taken"})
		return
	}
	asset, err := emojiasset.ImportEmojiImage(c.Request.Context(), original.Url)
	if err != nil {
		c.JSON(422, gin.H{"error": err.Error()})
		return
	}
	e.Url, e.StaticUrl, e.ContentType, e.FileSize, e.Width, e.Height = asset.URL, asset.StaticURL, asset.ContentType, asset.Size, asset.Width, asset.Height
	if err := db.GetDB().Create(&e).Error; err != nil {
		_ = emojiasset.DeleteAsset(asset)
		emojiError(c, err)
		return
	}
	c.JSON(200, serializeAdminEmoji(&e))
}
func setEmojiEnabled(c *gin.Context, enabled bool) {
	id, ok := emojiID(c)
	if !ok {
		return
	}
	var e models.Emoji
	if err := db.GetDB().First(&e, id).Error; err != nil {
		emojiError(c, err)
		return
	}
	if err := db.GetDB().Model(&e).Update("disabled", !enabled).Error; err != nil {
		emojiError(c, err)
		return
	}
	e.Disabled = !enabled
	c.JSON(200, serializeAdminEmoji(&e))
}
func EnableCustomEmoji(c *gin.Context)  { setEmojiEnabled(c, true) }
func DisableCustomEmoji(c *gin.Context) { setEmojiEnabled(c, false) }

func serializeAdminEmoji(e *models.Emoji) gin.H {
	var domain any
	if e.Domain != "" {
		domain = e.Domain
	}
	aliases := e.Aliases
	if aliases == nil {
		aliases = []string{}
	}
	roles := make([]string, 0, len(e.ReactionRoleIDs))
	for _, id := range e.ReactionRoleIDs {
		roles = append(roles, strconv.FormatUint(id, 10))
	}
	return gin.H{"reaction_role_ids": roles, "id": strconv.FormatUint(e.Id, 10), "shortcode": e.Shortcode, "url": e.Url, "static_url": e.StaticUrl, "domain": domain, "category": e.Category, "aliases": aliases, "license": e.License, "local_only": e.LocalOnly, "is_sensitive": e.IsSensitive, "visible_in_picker": e.VisibleInPicker, "disabled": e.Disabled, "content_type": e.ContentType, "total_file_size": e.FileSize, "width": e.Width, "height": e.Height, "created_at": time.Unix(e.CreatedAt, 0).UTC().Format(time.RFC3339Nano), "updated_at": time.Unix(e.UpdatedAt, 0).UTC().Format(time.RFC3339Nano), "uri": systemsettings.GetBaseURL() + "/emojis/" + e.Shortcode}
}

func applyReactionRoles(tx *gorm.DB, e *models.Emoji, values []string) error {
	if len(values) > 100 {
		return emojiAPIError{422, "too many reaction roles"}
	}
	ids := []uint64{}
	seen := map[uint64]bool{}
	for _, value := range values {
		id, err := strconv.ParseUint(value, 10, 64)
		if err != nil || id == 0 {
			return emojiAPIError{422, "invalid reaction role ID"}
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	if len(ids) > 0 {
		var count int64
		if err := tx.Model(&models.Role{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(ids)) {
			return emojiAPIError{422, "reaction role does not exist"}
		}
	}
	e.ReactionRoleIDs = ids
	return nil
}
