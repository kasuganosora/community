package account

import (
	"github.com/kasuganosora/community/controllers"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/search"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

// UpdateCredentialsRequest represents the request for updating account credentials.
type UpdateCredentialsRequest struct {
	DisplayName      *string `form:"display_name" json:"display_name"`
	Note             *string `form:"note" json:"note"`
	Avatar           string  `form:"avatar" json:"avatar"`
	Header           string  `form:"header" json:"header"`
	Locked           *bool   `form:"locked" json:"locked"`
	Bot              *bool   `form:"bot" json:"bot"`
	Discoverable     *bool   `form:"discoverable" json:"discoverable"`
	HideCollections  *bool   `form:"hide_collections" json:"hide_collections"`
	Indexable        *bool   `form:"indexable" json:"indexable"`
	FieldsAttributes []struct {
		Name  string `form:"name" json:"name"`
		Value string `form:"value" json:"value"`
	} `form:"fields_attributes" json:"fields_attributes"`
}

// UpdateCredentials handles PATCH /api/v1/accounts/update_credentials.
func UpdateCredentials(c *gin.Context) {
	var req UpdateCredentialsRequest

	if err := controllers.BindBody(c, &req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid profile update"})
		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Update fields if provided
	updates := make(map[string]any)

	if req.DisplayName != nil {
		updates["display_name"] = *req.DisplayName
	}

	if req.Note != nil {
		updates["note"] = *req.Note
		updates["imported_note_html"] = ""
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

	// Commit the profile, fields, and durable follower delivery together.
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		var locked models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, account.Id).Error; err != nil {
			return err
		}
		if req.Indexable != nil {
			updates["no_index"] = !*req.Indexable
		}
		if err := tx.Model(&locked).Updates(updates).Error; err != nil {
			return err
		}
		if req.FieldsAttributes != nil {
			var extra models.AccountExtra
			if err := tx.FirstOrCreate(&extra, models.AccountExtra{AccountId: account.Id}).Error; err != nil {
				return err
			}
			extra.Fields = make([]models.AccountField, 0, len(req.FieldsAttributes))
			for _, field := range req.FieldsAttributes {
				if field.Name != "" || field.Value != "" {
					extra.Fields = append(extra.Fields, models.AccountField{Name: field.Name, Val: field.Value})
				}
			}
			if err := tx.Model(&extra).Select("Fields").Updates(&extra).Error; err != nil {
				return err
			}
		}
		if err := models.QueueFederationProfile(tx, account.Id); err != nil {
			return err
		}
		return tx.Preload("Emojis").Preload("Extra").First(account, account.Id).Error
	})
	if err != nil {
		_ = c.Error(errors.Wrap(err, "update account"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	// 使账户缓存失效
	_ = cache.InvalidateAccount(c.Request.Context(), account.Id)
	search.QueueAccountIndex(account.Id)

	c.JSON(200, serializer.ToAccount(account))
}

// GetAccountSettings handles GET /api/v1/accounts/settings (extension).
func GetAccountSettings(c *gin.Context) {
	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	settings := gin.H{
		"locked":           account.Locked,
		"bot":              account.Bot,
		"discoverable":     account.Discoverable,
		"hide_collections": account.HideCollections,
		"indexable":        !account.NoIndex,
	}

	c.JSON(200, settings)
}
