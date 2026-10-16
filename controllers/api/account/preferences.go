package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

const (
	expandMediaKey = "reading_expand_media"
	uiLocaleKey    = "interface_locale"
)

// validUILocale lists the web UI languages; mirrors frontend/src/shared/i18n.
func validUILocale(v string) bool {
	switch v {
	case "zh-CN", "zh-TW", "ja", "en", "ru", "fr", "de":
		return true
	}

	return false
}

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
// "reading:expand:media" and "interface:locale" can be changed; either may be
// sent alone.
func UpdatePreferences(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	var req map[string]any
	if err := controllers.BindBody(c, &req); err != nil {
		c.JSON(422, gin.H{"error": "invalid request body"})

		return
	}
	updates := map[string]any{}
	if raw, ok := req["reading:expand:media"]; ok {
		v, _ := raw.(string)
		if !validExpandMedia(v) {
			c.JSON(422, gin.H{"error": "reading:expand:media must be default, show_all or hide_all"})

			return
		}
		updates[expandMediaKey] = v
	}
	if raw, ok := req["interface:locale"]; ok {
		v, _ := raw.(string)
		if !validUILocale(v) {
			c.JSON(422, gin.H{"error": "interface:locale is not a supported language"})

			return
		}
		updates[uiLocaleKey] = v
	}
	if len(updates) == 0 {
		c.JSON(422, gin.H{"error": "no supported preference given"})

		return
	}
	userID := preferenceUserID(c, account)
	if userID == 0 {
		c.JSON(401, gin.H{"error": "unauthorized"})

		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	if _, err := models.UpdateUserSettingData(conn, userID, updates); err != nil {
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
	var uiLocale any // null until the user picks a web UI language

	// Display and privacy preferences live in user_setting; the account
	// usually arrives without that association loaded.
	if userID := preferenceUserID(c, account); userID != 0 {
		var setting models.UserSetting
		if err := db.GetDB().WithContext(c.Request.Context()).Where("user_id = ?", userID).First(&setting).Error; err == nil {
			if v, ok := setting.Data[expandMediaKey].(string); ok && validExpandMedia(v) {
				defaultExpandMedia = v
			}
			if v, ok := setting.Data[uiLocaleKey].(string); ok && validUILocale(v) {
				uiLocale = v
			}
			// Users who protect their posts default to private posting.
			if protect, ok := setting.Data["post_protect"].(bool); ok && protect {
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
		"reading:autoplay:gifs":        true,
		"interface:locale":             uiLocale,
	})
}
