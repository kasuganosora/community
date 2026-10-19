package account

import (
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/profileverify"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"maps"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/kasuganosora/community/search"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

// UpdateCredentialsRequest represents the request for updating account credentials.
type UpdateCredentialsRequest struct {
	DisplayName     *string `form:"display_name" json:"display_name"`
	Note            *string `form:"note" json:"note"`
	Avatar          string  `form:"avatar" json:"avatar"`
	Header          string  `form:"header" json:"header"`
	Locked          *bool   `form:"locked" json:"locked"`
	Bot             *bool   `form:"bot" json:"bot"`
	Discoverable    *bool   `form:"discoverable" json:"discoverable"`
	HideCollections *bool   `form:"hide_collections" json:"hide_collections"`
	Indexable       *bool   `form:"indexable" json:"indexable"`
	// RequireSigninToView is an extension; Mastodon clients never send it.
	RequireSigninToView *bool `form:"require_signin_to_view" json:"require_signin_to_view"`
	IsCat               *bool `form:"is_cat" json:"is_cat"`
	// Misskey account settings (extensions).
	FollowingVisibility *string `form:"following_visibility" json:"following_visibility"`
	FollowersVisibility *string `form:"followers_visibility" json:"followers_visibility"`
	AutoAcceptFollowed  *bool   `form:"auto_accept_followed" json:"auto_accept_followed"`
	HideOnlineStatus    *bool   `form:"hide_online_status" json:"hide_online_status"`
	SpeakAsCat          *bool   `form:"speak_as_cat" json:"speak_as_cat"`
	Location            *string `form:"location" json:"location"`
	Birthday            *string `form:"birthday" json:"birthday"`
	Language            *string `form:"language" json:"language"`
	FollowedMessage     *string `form:"followed_message" json:"followed_message"`
	ReactionAcceptance  *string `form:"reaction_acceptance" json:"reaction_acceptance"`
	FieldsAttributes    []struct {
		Name  string `form:"name" json:"name"`
		Value string `form:"value" json:"value"`
	} `form:"fields_attributes" json:"fields_attributes"`
}

// UpdateCredentials handles PATCH /api/v1/accounts/update_credentials.
func UpdateCredentials(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512*1024)
	var req UpdateCredentialsRequest

	if err := controllers.BindBody(c, &req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid profile update"})
		return
	}
	if err := validateProfileUpdate(&req); err != nil {
		c.AbortWithStatusJSON(422, gin.H{"error": err.Error()})
		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Update fields if provided
	updates := make(map[string]any)
	for column, value := range map[string]*string{
		"location": req.Location, "birthday": req.Birthday, "language": req.Language,
		"followed_message": req.FollowedMessage, "reaction_acceptance": req.ReactionAcceptance,
	} {
		if value != nil {
			updates[column] = *value
		}
	}
	if req.IsCat != nil {
		updates["is_cat"] = *req.IsCat
	}

	if req.DisplayName != nil {
		if *req.DisplayName != account.DisplayName && moderation.Current().IsProhibitedName(*req.DisplayName) {
			c.AbortWithStatusJSON(422, gin.H{"error": moderation.ErrProhibitedName.Error(), "code": "PROHIBITED_NAME"})
			return
		}
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

	// hide_collections=true hides both lists; false makes both public. The
	// per-list values win when both are sent.
	following, followers := account.FollowingVisibilityValue(), account.FollowersVisibilityValue()
	if req.HideCollections != nil {
		following, followers = models.CollectionPublic, models.CollectionPublic
		if *req.HideCollections {
			following, followers = models.CollectionPrivate, models.CollectionPrivate
		}
	}
	if req.FollowingVisibility != nil {
		following = *req.FollowingVisibility
	}
	if req.FollowersVisibility != nil {
		followers = *req.FollowersVisibility
	}
	if !models.ValidCollectionVisibility(following) || !models.ValidCollectionVisibility(followers) {
		c.AbortWithStatusJSON(422, gin.H{"error": "invalid collection visibility"})
		return
	}
	if req.HideCollections != nil || req.FollowingVisibility != nil || req.FollowersVisibility != nil {
		maps.Copy(updates, models.CollectionVisibilityUpdates(following, followers))
	}
	for column, value := range map[string]*bool{"auto_accept_followed": req.AutoAcceptFollowed, "hide_online_status": req.HideOnlineStatus, "speak_as_cat": req.SpeakAsCat} {
		if value != nil {
			updates[column] = *value
		}
	}

	if req.RequireSigninToView != nil {
		updates["require_signin_to_view"] = *req.RequireSigninToView
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
	err := db.GetDB().WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var locked models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, account.Id).Error; err != nil {
			return err
		}
		if locked.DeletionRequestedAt != nil || locked.IsBan {
			return gorm.ErrRecordNotFound
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
			previous := extra.Fields
			extra.Fields = make([]models.AccountField, 0, len(req.FieldsAttributes))
			for _, field := range req.FieldsAttributes {
				if field.Name != "" || field.Value != "" {
					updated := models.AccountField{Name: field.Name, Val: field.Value}
					for _, old := range previous {
						if old.Name == field.Name && old.Val == field.Value {
							updated.VerifiedAt = old.VerifiedAt
							break
						}
					}
					extra.Fields = append(extra.Fields, updated)
				}
			}
			if err := tx.Model(&extra).Select("Fields").Updates(&extra).Error; err != nil {
				return err
			}
			if err := profileverify.Enqueue(tx, account.Id); err != nil {
				return err
			}
		}
		if err := models.QueueFederationProfile(tx, account.Id); err != nil {
			return err
		}
		return tx.Preload("Emojis").Preload("Extra").First(account, account.Id).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(403, gin.H{"error": "account is unavailable"})
			return
		}
		_ = c.Error(errors.Wrap(err, "update account"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})
		return
	}

	// 使账户缓存失效
	_ = cache.InvalidateAccount(c.Request.Context(), account.Id)
	search.QueueAccountIndex(account.Id)

	result := serializer.ToAccount(account)
	result.FollowedMessage = account.FollowedMessage
	c.JSON(200, result)
}

// GetAccountSettings handles GET /api/v1/accounts/settings (extension).
func GetAccountSettings(c *gin.Context) {
	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	settings := gin.H{
		"locked":                 account.Locked,
		"bot":                    account.Bot,
		"discoverable":           account.Discoverable,
		"hide_collections":       account.HideCollections,
		"following_visibility":   account.FollowingVisibilityValue(),
		"followers_visibility":   account.FollowersVisibilityValue(),
		"auto_accept_followed":   account.AutoAcceptFollowed,
		"hide_online_status":     account.HideOnlineStatus,
		"speak_as_cat":           account.SpeakAsCat,
		"indexable":              !account.NoIndex,
		"require_signin_to_view": account.RequireSigninToView,
	}

	c.JSON(200, settings)
}
