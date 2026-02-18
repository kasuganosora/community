package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
)

// GetPreferences handles GET /api/v1/preferences
func GetPreferences(c *gin.Context) {
	// Get current account
	account := c.MustGet("account").(*models.Account)

	// Default preferences
	defaultPostingVisibility := "public"
	defaultPostingSensitive := false
	defaultPostingLanguage := "en"
	defaultPostingContentType := "text/plain"
	defaultExpandSpoilers := false
	defaultExpandMedia := "default"

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
