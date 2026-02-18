package account

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

// UpdateCredentialsRequest represents the request for updating account credentials
type UpdateCredentialsRequest struct {
	DisplayName      *string `form:"display_name"`
	Note             *string `form:"note"`
	Avatar           string  `form:"avatar"`
	Header           string  `form:"header"`
	Locked           *bool   `form:"locked"`
	Bot              *bool   `form:"bot"`
	Discoverable     *bool   `form:"discoverable"`
	HideCollections  *bool   `form:"hide_collections"`
	Indexable        *bool   `form:"indexable"`
	FieldsAttributes []struct {
		Name  string `form:"name"`
		Value string `form:"value"`
	} `form:"fields_attributes"`
}

// UpdateCredentials handles PATCH /api/v1/accounts/update_credentials
func UpdateCredentials(c *gin.Context) {
	var req UpdateCredentialsRequest
	if err := c.ShouldBind(&req); err != nil {
		// Try to continue even with partial binding errors
	}

	// Get current account
	account := c.MustGet("account").(*models.Account)

	// Update fields if provided
	updates := make(map[string]interface{})

	if req.DisplayName != nil {
		updates["display_name"] = *req.DisplayName
	}
	if req.Note != nil {
		updates["note"] = *req.Note
	}
	if req.Locked != nil {
		updates["locked"] = *req.Locked
	}
	if req.Bot != nil {
		updates["bot"] = *req.Bot
	}
	if req.Discoverable != nil {
		updates["discoverable"] = *req.Discoverable
	}
	if req.HideCollections != nil {
		updates["hide_collections"] = *req.HideCollections
	}

	// Update avatar if provided
	if req.Avatar != "" {
		updates["avatar"] = req.Avatar
	}

	// Update header if provided
	if req.Header != "" {
		updates["header"] = req.Header
	}

	// Update timestamp
	now := time.Now()
	updates["updated_at"] = &now

	// Apply updates to account
	if len(updates) > 0 {
		if err := db.GetDB().Model(account).Updates(updates).Error; err != nil {
			_ = c.Error(errors.Wrap(err, "update account error"))
			c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
			return
		}
	}

	// Update fields attributes if provided
	if len(req.FieldsAttributes) > 0 {
		// Update account's fields array
		fields := make([]models.AccountField, 0)
		for _, field := range req.FieldsAttributes {
			if field.Name == "" && field.Value == "" {
				continue
			}
			fields = append(fields, models.AccountField{
				Name: field.Name,
				Val:  field.Value,
			})
		}
		// Update the fields via raw SQL since AccountField is a json field
		db.GetDB().Model(account).Update("fields", fields)
	}

	// Reload account with associations
	err := db.GetDB().Preload("Emojis").Preload("Extra").Preload("Fields").
		First(account, account.Id).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "reload account error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	c.JSON(200, serializer.ToAccount(account))
}

// GetAccountSettings handles GET /api/v1/accounts/settings (extension)
func GetAccountSettings(c *gin.Context) {
	// Get current account
	account := c.MustGet("account").(*models.Account)

	settings := gin.H{
		"locked":           account.Locked,
		"bot":              account.Bot,
		"discoverable":     account.Discoverable,
		"hide_collections": account.HideCollections,
		"indexable":        true,
	}

	// Add user settings if available
	if account.User != nil && account.User.Settings != nil && account.User.Settings.Data != nil {
		if v, ok := account.User.Settings.Data["indexable"].(bool); ok {
			settings["indexable"] = v
		}
	}

	c.JSON(200, settings)
}
