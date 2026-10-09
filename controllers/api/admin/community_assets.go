package admin

import (
	"errors"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/systemsettings"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func assetID(c *gin.Context) (uint64, bool) {
	id, e := strconv.ParseUint(c.Param("id"), 10, 64)
	if e != nil || id == 0 {
		c.JSON(400, gin.H{"error": "invalid ID"})
		return 0, false
	}
	return id, true
}
func assetError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(404, gin.H{"error": "not found"})
	} else {
		c.JSON(500, gin.H{"error": "operation failed"})
	}
}
func uploadBrandImage(c *gin.Context) (*emojiasset.Asset, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, emojiasset.MaxSize+(128<<10))
	if err := c.Request.ParseMultipartForm(emojiasset.MaxSize); err != nil {
		return nil, errors.New("image must be at most 2 MiB")
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	header, err := c.FormFile("file")
	if err != nil {
		return nil, errors.New("file is required")
	}
	file, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return emojiasset.StoreEmojiImage(c.Request.Context(), file, header.Header.Get("Content-Type"))
}
func validBrandName(name string, max int) bool {
	return strings.TrimSpace(name) != "" && utf8.RuneCountInString(name) <= max
}
func validAdLink(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && len(raw) <= 2048
}
func ListAvatarDecorations(c *gin.Context) {
	var rows []models.AvatarDecoration
	if err := db.GetDB().Order("id DESC").Limit(500).Find(&rows).Error; err != nil {
		assetError(c, err)
		return
	}
	c.JSON(200, rows)
}
func PublicAvatarDecorations(c *gin.Context) {
	var rows []models.AvatarDecoration
	if err := db.GetDB().Where("enabled = ?", true).Order("id DESC").Limit(500).Find(&rows).Error; err != nil {
		assetError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, rows)
}
func CreateAvatarDecoration(c *gin.Context) {
	asset, err := uploadBrandImage(c)
	if err != nil {
		c.JSON(422, gin.H{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(c.PostForm("name"))
	if !validBrandName(name, 80) {
		_ = emojiasset.DeleteAsset(asset)
		c.JSON(422, gin.H{"error": "name is required (80 characters maximum)"})
		return
	}
	row := models.AvatarDecoration{Name: name, ImageURL: asset.StaticURL, Enabled: c.PostForm("enabled") == "true"}
	if err = db.GetDB().Create(&row).Error; err != nil {
		_ = emojiasset.DeleteAsset(asset)
		assetError(c, err)
		return
	}
	if asset.URL != asset.StaticURL {
		_ = emojiasset.DeleteAsset(&emojiasset.Asset{URL: asset.URL, StaticURL: asset.URL})
	}
	c.JSON(201, row)
}
func UpdateAvatarDecoration(c *gin.Context) {
	id, ok := assetID(c)
	if !ok {
		return
	}
	var req struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "invalid JSON"})
		return
	}
	var row models.AvatarDecoration
	if err := db.GetDB().First(&row, id).Error; err != nil {
		assetError(c, err)
		return
	}
	if req.Name != nil {
		if !validBrandName(*req.Name, 80) {
			c.Status(422)
			return
		}
		row.Name = strings.TrimSpace(*req.Name)
	}
	if req.Enabled != nil {
		row.Enabled = *req.Enabled
	}
	if err := db.GetDB().Model(&row).Select("*").Updates(&row).Error; err != nil {
		assetError(c, err)
		return
	}
	c.JSON(200, row)
}
func DeleteAvatarDecoration(c *gin.Context) {
	id, ok := assetID(c)
	if !ok {
		return
	}
	var row models.AvatarDecoration
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, id).Error; e != nil {
			return e
		}
		if e := tx.Model(&models.Account{}).Where("avatar_decoration_id = ?", id).UpdateColumn("avatar_decoration_id", 0).Error; e != nil {
			return e
		}
		return tx.Delete(&row).Error
	})
	if err != nil {
		assetError(c, err)
		return
	}
	cleanupBrandImage(row.ImageURL)
	c.Status(204)
}
func AvatarDecorationImage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	id, ok := assetID(c)
	if !ok {
		return
	}
	var row models.AvatarDecoration
	if err := db.GetDB().Where("id = ? AND enabled = ?", id, true).First(&row).Error; err != nil {
		assetError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	u, err := url.Parse(row.ImageURL)
	if err != nil || u.Scheme+"://"+u.Host != systemsettings.GetBaseURL() || u.RawQuery != "" || u.Fragment != "" || u.User != nil || !strings.HasPrefix(u.Path, "/emoji-assets/") {
		c.Status(404)
		return
	}
	name := strings.TrimPrefix(u.Path, "/emoji-assets/")
	if path.Base(name) != name || !strings.HasSuffix(name, "-static.png") {
		c.Status(404)
		return
	}
	if _, err = uuid.Parse(strings.TrimSuffix(name, "-static.png")); err != nil {
		c.Status(404)
		return
	}
	emojiasset.ServeObject(c, "emojis/"+name, name, "image/png", "")
}
func SelectAvatarDecoration(c *gin.Context) {
	account := controllers.GetAccount(c)
	if account == nil {
		c.Status(401)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.Status(400)
		return
	}
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		c.Status(422)
		return
	}
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		if id != 0 {
			var row models.AvatarDecoration
			if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND enabled = ?", id, true).First(&row).Error; e != nil {
				return e
			}
		}
		return tx.Model(&models.Account{}).Where("id = ?", account.Id).UpdateColumn("avatar_decoration_id", id).Error
	})
	if err != nil {
		assetError(c, err)
		return
	}
	account.AvatarDecorationID = id
	_ = cache.InvalidateAccount(c.Request.Context(), account.Id)
	c.JSON(200, gin.H{"id": strconv.FormatUint(id, 10)})
}

func ListAdvertisements(c *gin.Context) {
	var rows []models.Advertisement
	if err := db.GetDB().Order("id DESC").Limit(500).Find(&rows).Error; err != nil {
		assetError(c, err)
		return
	}
	c.JSON(200, rows)
}
func PublicAdvertisements(c *gin.Context) {
	now := time.Now()
	var rows []models.Advertisement
	if err := db.GetDB().Where("enabled = ? AND (starts_at IS NULL OR starts_at <= ?) AND (ends_at IS NULL OR ends_at > ?)", true, now, now).Order("id DESC").Limit(3).Find(&rows).Error; err != nil {
		assetError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, rows)
}
func parseAdDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, e := time.Parse(time.RFC3339, s)
	return &t, e
}
func CreateAdvertisement(c *gin.Context) {
	asset, err := uploadBrandImage(c)
	if err != nil {
		c.JSON(422, gin.H{"error": err.Error()})
		return
	}
	row := models.Advertisement{Title: strings.TrimSpace(c.PostForm("title")), TargetURL: strings.TrimSpace(c.PostForm("target_url")), ImageURL: asset.StaticURL, Enabled: c.PostForm("enabled") == "true"}
	row.StartsAt, err = parseAdDate(c.PostForm("starts_at"))
	end, endErr := parseAdDate(c.PostForm("ends_at"))
	row.EndsAt = end
	if err != nil || endErr != nil || !validBrandName(row.Title, 120) || !validAdLink(row.TargetURL) || (row.StartsAt != nil && end != nil && !end.After(*row.StartsAt)) {
		_ = emojiasset.DeleteAsset(asset)
		c.JSON(422, gin.H{"error": "invalid title, link or schedule"})
		return
	}
	if err = db.GetDB().Create(&row).Error; err != nil {
		_ = emojiasset.DeleteAsset(asset)
		assetError(c, err)
		return
	}
	if asset.URL != asset.StaticURL {
		_ = emojiasset.DeleteAsset(&emojiasset.Asset{URL: asset.URL, StaticURL: asset.URL})
	}
	c.JSON(201, row)
}
func UpdateAdvertisement(c *gin.Context) {
	id, ok := assetID(c)
	if !ok {
		return
	}
	var req struct {
		Title     *string `json:"title"`
		TargetURL *string `json:"target_url"`
		Enabled   *bool   `json:"enabled"`
		StartsAt  *string `json:"starts_at"`
		EndsAt    *string `json:"ends_at"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.Status(400)
		return
	}
	var row models.Advertisement
	if err := db.GetDB().First(&row, id).Error; err != nil {
		assetError(c, err)
		return
	}
	if req.Title != nil {
		row.Title = strings.TrimSpace(*req.Title)
	}
	if req.TargetURL != nil {
		row.TargetURL = *req.TargetURL
	}
	if req.Enabled != nil {
		row.Enabled = *req.Enabled
	}
	var err error
	if req.StartsAt != nil {
		row.StartsAt, err = parseAdDate(*req.StartsAt)
		if err != nil {
			c.Status(422)
			return
		}
	}
	if req.EndsAt != nil {
		row.EndsAt, err = parseAdDate(*req.EndsAt)
		if err != nil {
			c.Status(422)
			return
		}
	}
	if !validBrandName(row.Title, 120) || !validAdLink(row.TargetURL) || (row.StartsAt != nil && row.EndsAt != nil && !row.EndsAt.After(*row.StartsAt)) {
		c.JSON(422, gin.H{"error": "invalid title, link or schedule"})
		return
	}
	if err = db.GetDB().Model(&row).Select("*").Updates(&row).Error; err != nil {
		assetError(c, err)
		return
	}
	c.JSON(200, row)
}
func DeleteAdvertisement(c *gin.Context) {
	id, ok := assetID(c)
	if !ok {
		return
	}
	var row models.Advertisement
	if err := db.GetDB().First(&row, id).Error; err != nil {
		assetError(c, err)
		return
	}
	if err := db.GetDB().Delete(&row).Error; err != nil {
		assetError(c, err)
		return
	}
	cleanupBrandImage(row.ImageURL)
	c.Status(204)
}
func cleanupBrandImage(raw string) {
	var decorations, ads int64
	if db.GetDB().Model(&models.AvatarDecoration{}).Where("image_url = ?", raw).Count(&decorations).Error != nil {
		return
	}
	if db.GetDB().Model(&models.Advertisement{}).Where("image_url = ?", raw).Count(&ads).Error != nil {
		return
	}
	if decorations+ads == 0 {
		_ = emojiasset.DeleteAsset(&emojiasset.Asset{URL: raw, StaticURL: raw})
	}
}
