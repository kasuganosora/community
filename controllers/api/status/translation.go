package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/utils/translation"
)

// Translate handles POST /api/v1/statuses/:id/translate (Mastodon: optional
// "lang" form/JSON/query parameter, defaulting to English) and the legacy
// POST /api/v1/statuses/:id/translate/:lang. It returns Mastodon's
// Translation entity. Errors carry an English "error" for Mastodon clients
// and a stable "error_code" that the web UI localises.
func Translate(c *gin.Context) {
	status := getRealStatus(c)
	account := controllers.GetAccount(c)
	abortUnlessVisible(c, db.GetDB(), status, account)

	targetLang := c.Param("lang")
	if targetLang == "" {
		targetLang = c.PostForm("lang")
	}
	if targetLang == "" {
		var body struct {
			Lang string `json:"lang"`
		}
		if c.ContentType() == "application/json" {
			_ = c.ShouldBindJSON(&body)
		}
		targetLang = body.Lang
	}
	if targetLang == "" {
		targetLang = c.Query("lang")
	}
	if targetLang == "" {
		targetLang = "en"
	}

	result, err := translation.TranslateStatus(c.Request.Context(), status, account, targetLang)
	if err != nil {
		c.JSON(err.Status, gin.H{"error": err.Message, "error_code": err.Code})

		return
	}
	c.JSON(200, result)
}

// TranslationLanguages handles GET /api/v1/instance/translation_languages.
func TranslationLanguages(c *gin.Context) {
	c.JSON(200, translation.LanguagePairs())
}
