package admin

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// GetStatus handles GET /api/v1/admin/statuses/:id
func GetStatus(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "status id required"})
		return
	}

	status := &models.Status{}
	if err := db.GetDB().Preload("Account").Preload("Account.Extra").First(status, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "status not found"})
		return
	}

	c.JSON(200, serializeAdminStatus(status))
}

// StatusActionRequest represents a status action request
type StatusActionRequest struct {
	Type string `json:"type"`
}

// PerformStatusAction handles POST /api/v1/admin/statuses/:id/action
func PerformStatusAction(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "status id required"})
		return
	}

	var req StatusActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid request"})
		return
	}

	status := &models.Status{}
	if err := db.GetDB().First(status, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "status not found"})
		return
	}

	var dbErr error
	switch req.Type {
	case "delete":
		dbErr = db.GetDB().Delete(status).Error
	case "mark_as_sensitive":
		status.Sensitive = true
		dbErr = db.GetDB().Save(status).Error
	case "mark_as_not_sensitive":
		status.Sensitive = false
		dbErr = db.GetDB().Save(status).Error
	}

	if dbErr != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, serializeAdminStatus(status))
}

// DeleteStatus handles DELETE /api/v1/admin/statuses/:id
func DeleteStatus(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "status id required"})
		return
	}

	status := &models.Status{}
	if err := db.GetDB().First(status, id).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "status not found"})
		return
	}

	if err := db.GetDB().Delete(status).Error; err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, gin.H{})
}

func serializeAdminStatus(status *models.Status) gin.H {
	var createdAt, updatedAt interface{}
	if status.CreatedAt != nil {
		createdAt = status.CreatedAt.Format("2006-01-02T15:04:05.000Z")
	}
	if status.UpdatedAt != nil {
		updatedAt = status.UpdatedAt.Format("2006-01-02T15:04:05.000Z")
	}

	var uri, url interface{}
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
		"created_at":       createdAt,
		"updated_at":       updatedAt,
		"account_id":       strconv.FormatUint(status.AccountId, 10),
		"account":          serializeAdminStatusAccount(status.Account),
	}
}

func serializeAdminStatusAccount(account *models.Account) gin.H {
	if account == nil {
		return gin.H{}
	}

	var domain interface{}
	if account.Domain != nil {
		domain = *account.Domain
	}

	return gin.H{
		"id":           strconv.FormatUint(account.Id, 10),
		"username":     account.Username,
		"domain":       domain,
		"display_name": account.DisplayName,
		"avatar":       account.Avatar,
		"bot":          account.Bot,
		"suspended":    account.IsBan,
		"silenced":     account.Locked && !account.IsBan,
	}
}
