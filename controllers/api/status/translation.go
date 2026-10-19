package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils/translation"
)

// TranslationResponse represents the translation response.
type TranslationResponse struct {
	Content          string `json:"content"`
	Language         string `json:"language"`
	DetectedLanguage string `json:"detected_source_language"`
	Provider         string `json:"provider"`
}

// Translate handles POST /api/v1/statuses/:id/translate
// Translates a status content to the specified language.
func Translate(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)
	account := controllers.GetAccount(c)

	abortUnlessVisible(c, db.GetDB(), status, account)

	// Get target language from URL parameter
	targetLang := c.Param("lang")
	if targetLang == "" {
		c.JSON(400, gin.H{"error": "target language required"})

		return
	}

	// Use translation service
	service := translation.GetService()

	// Detect source language if not provided
	sourceLang := status.Language
	if sourceLang == "" {
		detected, err := service.DetectLanguage(status.Content.Content)
		if err != nil {
			log.Warn("Failed to detect language: %v", err)

			sourceLang = "auto"
		} else {
			sourceLang = detected
		}
	}

	// Perform translation
	result, err := service.Translate(status.Content.Content, sourceLang, targetLang)
	if err != nil {
		log.Error("Translation failed: %v", err)
		// Return original content if translation fails
		c.JSON(200, TranslationResponse{
			Content:          status.Content.Content,
			Language:         targetLang,
			DetectedLanguage: sourceLang,
			Provider:         "error",
		})

		return
	}

	c.JSON(200, TranslationResponse{
		Content:          result.Text,
		Language:         targetLang,
		DetectedLanguage: result.DetectedSource,
		Provider:         result.Provider,
	})
}
