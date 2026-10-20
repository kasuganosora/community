package status

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils/translation"
)

// Translate handles POST /api/v1/statuses/:id/translate (Mastodon: optional
// "lang" form/JSON/query parameter, defaulting to the user's language, Accept-Language, then English) and the legacy
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
		targetLang = defaultTargetLang(account, c.GetHeader("Accept-Language"))
	}

	result, err := translation.TranslateStatus(c.Request.Context(), status, account, targetLang)
	if err != nil {
		c.JSON(err.Status, gin.H{"error": err.Message, "error_code": err.Code})

		return
	}
	c.JSON(200, result)
}

// defaultTargetLang is the target when the client names none: the signed-in
// user's preferred language, then the browser's Accept-Language, then English.
func defaultTargetLang(account *models.Account, acceptLanguage string) string {
	if account != nil {
		if l := strings.TrimSpace(account.Language); l != "" {
			return l
		}
	}
	for _, part := range strings.Split(acceptLanguage, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if tag != "" && tag != "*" && len(tag) <= 35 {
			return tag
		}
	}
	return "en"
}

// TranslationLanguages handles GET /api/v1/instance/translation_languages.
func TranslationLanguages(c *gin.Context) {
	c.JSON(200, translation.LanguagePairs())
}
