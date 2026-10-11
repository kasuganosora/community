package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/linkcard"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

// Card handles GET /api/v1/statuses/:id/card (deprecated in Mastodon 3.x
// but still used by clients): the status's link preview, or null.
func Card(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)
	controllers.AbortIf(c, status.DeletedAt != nil || status.ScheduledAt != nil, 404, gin.H{"error": "not found"})
	account := controllers.GetAccount(c)

	allowed, err := status.CanViewEx(db.GetDB(), account)
	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, !allowed, 403, gin.H{"error": "has no permission"})

	// Missing previews are repaired asynchronously only after authorization.
	// The endpoint never fetches a remote page on the HTTP request goroutine.
	if len(status.Content.Card) == 0 && status.Visibility != models.StatusVisibilityDirect && status.ReblogId == nil && len(status.Attachments) == 0 && linkcard.FirstLink(status.Content.Content) != "" {
		linkcard.Enqueue(status.Id)
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(200, serializer.ToCard(status.Content.Card))
}
