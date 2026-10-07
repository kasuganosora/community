package admin

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// GetStatus handles GET /api/v1/admin/statuses/:id.
func GetStatus(c *gin.Context) {
	id, parseErr := strconv.ParseUint(c.Param("id"), 10, 64)
	if parseErr != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid status id"}) //nolint:goconst

		return
	}

	status := &models.Status{}
	err := db.GetDB().WithContext(c.Request.Context()).Preload("Content").Preload("Account").Preload("Account.Extra").First(status, id).Error
	if err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "status not found"}) //nolint:goconst

		return
	}

	c.JSON(200, serializeAdminStatus(status))
}

// StatusActionRequest represents a status action request.
type StatusActionRequest struct {
	Type string `json:"type"`
}

// PerformStatusAction handles POST /api/v1/admin/statuses/:id/action.
func PerformStatusAction(c *gin.Context) {
	id, parseErr := strconv.ParseUint(c.Param("id"), 10, 64)
	if parseErr != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid status id"})

		return
	}

	var req StatusActionRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid request"}) //nolint:goconst

		return
	}

	status := &models.Status{}
	err = db.GetDB().WithContext(c.Request.Context()).Preload("Content").Preload("Account").First(status, id).Error
	if err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "status not found"})

		return
	}

	var dbErr error

	switch req.Type {
	case "delete":
		status, dbErr = deleteAdminStatus(c.Request.Context(), db.GetDB(), id)
	case "mark_as_sensitive":
		status.Sensitive = true
		dbErr = db.GetDB().WithContext(c.Request.Context()).Model(status).Update("sensitive", status.Sensitive).Error
	case "mark_as_not_sensitive":
		status.Sensitive = false
		dbErr = db.GetDB().WithContext(c.Request.Context()).Model(status).Update("sensitive", status.Sensitive).Error
	default:
		c.JSON(422, gin.H{"error": "unsupported status action"})
		return
	}

	if dbErr != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	c.JSON(200, serializeAdminStatus(status))
}

// DeleteStatus handles DELETE /api/v1/admin/statuses/:id.
func DeleteStatus(c *gin.Context) {
	id, parseErr := strconv.ParseUint(c.Param("id"), 10, 64)
	if parseErr != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid status id"})

		return
	}

	status := &models.Status{}
	err := db.GetDB().WithContext(c.Request.Context()).Preload("Content").Preload("Account").First(status, id).Error
	if err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "status not found"})

		return
	}

	_, err = deleteAdminStatus(c.Request.Context(), db.GetDB(), id)
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, gin.H{})
}

func serializeAdminStatus(status *models.Status) gin.H {
	var createdAt, updatedAt any
	if status.CreatedAt != nil {
		createdAt = status.CreatedAt.Format("2006-01-02T15:04:05.000Z")
	}

	if status.UpdatedAt != nil {
		updatedAt = status.UpdatedAt.Format("2006-01-02T15:04:05.000Z")
	}

	var uri, url any
	if status.OriginUri != nil {
		uri = *status.OriginUri
	}

	if status.OriginUrl != nil {
		url = *status.OriginUrl
	}

	return gin.H{
		"id":               strconv.FormatUint(status.Id, 10),
		"uri":              uri,
		"url":              url,
		"content":          status.Content.Content,
		"spoiler_text":     status.Content.SpoilerText,
		"sensitive":        status.Sensitive,
		"visibility":       status.Visibility,
		"language":         status.Language,
		"replies_count":    status.RepliesCount,
		"reblogs_count":    status.ReblogsCount,
		"favourites_count": status.FavouritesCount,
		"created_at":       createdAt, //nolint:goconst
		"updated_at":       updatedAt,
		"account_id":       strconv.FormatUint(status.AccountId, 10),
		"account":          serializeAdminStatusAccount(status.Account),
	}
}

func serializeAdminStatusAccount(account *models.Account) gin.H {
	if account == nil {
		return gin.H{}
	}

	var domain any
	if account.Domain != nil {
		domain = *account.Domain
	}

	return gin.H{
		"id":           strconv.FormatUint(account.Id, 10),
		"username":     account.Username,
		"domain":       domain,              //nolint:goconst
		"display_name": account.DisplayName, //nolint:goconst
		"avatar":       account.Avatar,
		"bot":          account.Bot,
		"suspended":    account.IsBan, //nolint:goconst
		"silenced":     false,         //nolint:goconst
	}
}
