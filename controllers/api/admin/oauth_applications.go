package admin

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/oauth"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// ListOAuthApplications handles GET /api/v1/admin/oauth_applications.
func ListOAuthApplications(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionAdministrator) {
		return
	}
	apps, err := oauth.ListApplications(db.GetDB().WithContext(c.Request.Context()))
	if operationError(c, err) {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, apps)
}

// DeleteOAuthApplication handles DELETE /api/v1/admin/oauth_applications/:id.
// The application's tokens, authorization codes and grants are removed with it.
func DeleteOAuthApplication(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionAdministrator) {
		return
	}
	id, ok := operationsID(c)
	if !ok {
		return
	}
	tokens, err := oauth.DeleteApplication(db.GetDB().WithContext(c.Request.Context()), id)
	if errors.Is(err, oauth.ErrAppNotFound) {
		c.JSON(404, gin.H{"error": "not found"})

		return
	}
	if operationError(c, err) {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"id": c.Param("id"), "deleted_tokens": tokens})
}
