package oauth

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

type authorizedApp struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Website string `json:"website,omitempty"`
	Scopes  string `json:"scopes,omitempty"`
	Created string `json:"created_at,omitempty"`
}

// ListAuthorized handles GET /api/v1/oauth/authorized_applications.
func ListAuthorized(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	var links []models.ApplicationUserAuth
	if err := db.GetDB().Where("user_id = ?", account.UserId).Order("id desc").Find(&links).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	out := make([]authorizedApp, 0, len(links))
	for _, link := range links {
		app := &OAuthApplication{}
		if err := db.GetDB().First(app, link.ApplicationId).Error; err != nil {
			continue
		}

		item := authorizedApp{
			ID:      strconv.FormatUint(app.Id, 10),
			Name:    app.Name,
			Website: app.Website,
			Scopes:  app.Scopes.OAuthScopeString(),
		}
		if link.CreatedAt != nil {
			item.Created = link.CreatedAt.UTC().Format(time.RFC3339)
		}

		out = append(out, item)
	}

	c.JSON(http.StatusOK, out)
}

// RevokeAuthorized handles DELETE /api/v1/oauth/authorized_applications/:id.
func RevokeAuthorized(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid id"})

		return
	}

	if err = db.GetDB().Where("application_id = ? AND user_id = ?", id, account.UserId).Delete(&models.ApplicationUserAuth{}).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	now := time.Now()
	if err = db.GetDB().Model(&models.AuthToken{}).
		Where("user_id = ? AND application_id = ? AND revoke IS NULL", account.UserId, id).
		Update("revoke", now).Error; err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "server_error"})

		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}
