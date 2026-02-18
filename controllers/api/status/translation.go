package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// TranslationResponse represents the translation response
type TranslationResponse struct {
	Content      string              `json:"content"`
	Language     string              `json:"language"`
	DetectedLanguage string          `json:"detected_source_language"`
	Provider     string              `json:"provider"`
}

// Translate handles POST /api/v1/statuses/:id/translate
// Translates a status content to the specified language
func Translate(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)
	account := controllers.GetAccount(c)

	// Check permission
	if status.Visibility == models.StatusVisibilityPrivate || status.Visibility == models.StatusVisibilityDirect {
		if account == nil {
			c.JSON(403, gin.H{"error": "has no permission"})
			return
		}
		canView, err := status.CanViewEx(db.GetDB(), account)
		if err != nil {
			controllers.AbortIfError(c, err)
			return
		}
		if !canView {
			c.JSON(403, gin.H{"error": "has no permission"})
			return
		}
	}

	// Get target language from URL parameter
	targetLang := c.Param("lang")
	if targetLang == "" {
		c.JSON(400, gin.H{"error": "target language required"})
		return
	}

	// TODO: Integrate with actual translation service (e.g., DeepL, Google Translate, LibreTranslate)
	// For now, return a mock response
	c.JSON(200, TranslationResponse{
		Content:          status.Content.Content,
		Language:         targetLang,
		DetectedLanguage: status.Language,
		Provider:         "mock",
	})
}
