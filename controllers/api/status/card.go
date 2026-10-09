package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// Card handles GET /api/v1/statuses/:id/card (deprecated in Mastodon 3.x
// but still used by clients): the status's link preview, or null.
func Card(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)
	account := controllers.GetAccount(c)

	allowed, err := status.CanViewEx(db.GetDB(), account)
	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, !allowed, 403, gin.H{"error": "has no permission"})

	c.JSON(200, status.Content.Card)
}
