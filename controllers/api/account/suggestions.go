package account

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

// ListSuggestions preserves Mastodon's v2 Suggestion[] contract.
func ListSuggestions(c *gin.Context) { listSuggestions(c, false) }

// ListSuggestionsV1 returns accounts directly, as Mastodon's v1 requires.
func ListSuggestionsV1(c *gin.Context) { listSuggestions(c, true) }
func listSuggestions(c *gin.Context, v1 bool) {
	c.Header("Cache-Control", "no-store")
	viewer := directoryViewer(c)
	if viewer == nil {
		c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
		return
	}
	limit := 20
	if value, err := strconv.Atoi(c.Query("limit")); err == nil && value > 0 {
		limit = min(value, 40)
	}
	suggestions, err := discovery.SuggestAccounts(c.Request.Context(), db.GetDB(), viewer, limit)
	if err != nil {
		_ = c.Error(err)
		c.Header("Retry-After", "5")
		c.AbortWithStatusJSON(503, gin.H{"error": "suggestions temporarily unavailable"})
		return
	}
	accounts := make([]*models.Account, 0, len(suggestions))
	for _, s := range suggestions {
		accounts = append(accounts, s.Account)
	}
	serialized := serializer.ToAccounts(accounts)
	if v1 {
		c.JSON(200, serialized)
		return
	}
	result := make([]gin.H, 0, len(serialized))
	for i, a := range serialized {
		result = append(result, gin.H{"source": suggestions[i].Source, "account": a})
	}
	c.JSON(200, result)
}

func RemoveSuggestion(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	viewer := directoryViewer(c)
	if viewer == nil {
		c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
		return
	}
	_, err := models.PutRecommendationFeedback(db.GetDB().WithContext(c.Request.Context()), viewer.Id, "dismiss_suggestion", "account", c.Param("account_id"))
	if errors.Is(err, models.ErrInvalidRecommendationFeedback) {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})
		return
	}
	if err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(500, gin.H{"error": "failed to remove suggestion"})
		return
	}
	c.JSON(200, gin.H{})
}
