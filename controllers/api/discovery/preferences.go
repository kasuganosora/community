package discovery

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	service "github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
)

func feedViewer(c *gin.Context) *models.Account {
	c.Header("Cache-Control", "no-store")
	value, exists := c.Get("account")
	if !exists {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return nil
	}
	account, ok := value.(*models.Account)
	if !ok || account == nil || account.Id == 0 || account.UserId == 0 {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return nil
	}
	return account
}

func GetPreferences(c *gin.Context) {
	account := feedViewer(c)
	if account == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()
	preference, err := models.GetRecommendationPreference(db.GetDB().WithContext(ctx), account.UserId)
	if err != nil {
		feedAPIError(c, err)
		return
	}
	enabled, err := service.EnabledContext(ctx)
	if err != nil {
		feedAPIError(c, err)
		return
	}
	c.JSON(200, gin.H{"preference": preference.Preference, "revision": preference.Revision, "languages": preference.Languages, "enabled": enabled})
}

func UpdatePreferences(c *gin.Context) {
	account := feedViewer(c)
	if account == nil {
		return
	}
	var input struct {
		Preference string    `json:"preference"`
		Languages  *[]string `json:"languages"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		feedAPIError(c, service.ErrInvalidRequest)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()
	preference, err := models.SetRecommendationPreferences(db.GetDB().WithContext(ctx), account.UserId, input.Preference, input.Languages)
	if err != nil {
		feedAPIError(c, err)
		return
	}
	enabled, err := service.EnabledContext(ctx)
	if err != nil {
		feedAPIError(c, err)
		return
	}
	c.JSON(200, gin.H{"preference": preference.Preference, "revision": preference.Revision, "languages": preference.Languages, "enabled": enabled})
}
