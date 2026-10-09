package account

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

const expandMediaKey = "reading_expand_media"

// validExpandMedia: "default" follows each post's sensitive flag,
// "show_all" never hides media, "hide_all" always hides it.
func validExpandMedia(v string) bool {
	return v == "default" || v == "show_all" || v == "hide_all"
}

// preferenceUserID is the signed-in user's id. AuthMiddleware sets "user"
// (the Account it loads has no User association), so use that first.
func preferenceUserID(c *gin.Context, account *models.Account) uint64 {
	if v, ok := c.Get("user"); ok {
		if u, ok := v.(*models.User); ok && u != nil {
			return u.Id
		}
	}
	if account.User != nil {
		return account.User.Id
	}

	return account.UserId
}

// UpdatePreferences handles PATCH /api/v1/preferences. Only
// "reading:expand:media" can be changed.
func UpdatePreferences(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	var req map[string]any
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(422, gin.H{"error": "invalid request body"})

		return
	}
	v, _ := req["reading:expand:media"].(string)
	if !validExpandMedia(v) {
		c.JSON(422, gin.H{"error": "reading:expand:media must be default, show_all or hide_all"})

		return
	}
	userID := preferenceUserID(c, account)
	if userID == 0 {
		c.JSON(401, gin.H{"error": "unauthorized"})

		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var setting models.UserSetting
	err := conn.Where("user_id = ?", userID).First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		setting = models.UserSetting{ID: uuid.NewString(), UserID: int64(userID), Data: map[string]any{}}
		err = nil
	}
	if err != nil {
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "internal error"})

		return
	}
	if setting.Data == nil {
		setting.Data = map[string]any{}
	}
	setting.Data[expandMediaKey] = v
	if err := conn.Save(&setting).Error; err != nil {
		_ = c.Error(err)
		c.JSON(500, gin.H{"error": "internal error"})

		return
	}
	GetPreferences(c)
}

// GetPreferences handles GET /api/v1/preferences.
func GetPreferences(c *gin.Context) {
	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Default preferences
	defaultPostingVisibility := "public"
	defaultPostingSensitive := false
	defaultPostingLanguage := "en"
	defaultPostingContentType := "text/plain"
	defaultExpandSpoilers := false
	defaultExpandMedia := "default"

	// The media display mode lives in user_setting; the account usually
	// arrives without that association loaded.
	if userID := preferenceUserID(c, account); userID != 0 {
		var setting models.UserSetting
		if err := db.GetDB().WithContext(c.Request.Context()).Where("user_id = ?", userID).First(&setting).Error; err == nil {
			if v, ok := setting.Data[expandMediaKey].(string); ok && validExpandMedia(v) {
				defaultExpandMedia = v
			}
		}
	}

	// Check account settings
	if account.User != nil && account.User.Settings != nil {

		// Check if user has privacy settings for posts
		if postProtect, ok := account.User.Settings.Data["post_protect"]; ok {
			if protect, ok := postProtect.(bool); ok && protect {
				defaultPostingVisibility = "private"
			}
		}
	}

	// Build response
	c.JSON(200, gin.H{
		"posting:default:visibility":   defaultPostingVisibility,
		"posting:default:sensitive":    defaultPostingSensitive,
		"posting:default:language":     defaultPostingLanguage,
		"posting:default:content_type": defaultPostingContentType,
		"reading:expand:spoilers":      defaultExpandSpoilers,
		"reading:expand:media":         defaultExpandMedia,
	})
}
