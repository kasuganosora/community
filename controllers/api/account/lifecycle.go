package account

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/accountlifecycle"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// These endpoints are internal web-session endpoints, not delegated OAuth API.
func lifecycleIdentity(c *gin.Context) (*models.Account, *models.User, bool) {
	a, _ := c.Get("account")
	u, _ := c.Get("user")
	token, _ := c.Get("token_data")
	account, ok := a.(*models.Account)
	user, userOK := u.(*models.User)
	td, tokenOK := token.(*models.AuthTokenData)
	if !ok || !userOK || !tokenOK || account == nil || user == nil || td == nil {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return nil, nil, false
	}
	if td.AuthType != models.AuthTokenTypeWeb || !account.IsLocal() || account.UserId != user.Id {
		c.JSON(403, gin.H{"error": "web session required"})
		return nil, nil, false
	}
	return account, user, true
}

func Lifecycle(c *gin.Context) {
	a, _, ok := lifecycleIdentity(c)
	if !ok {
		return
	}
	if err := db.GetDB().WithContext(c.Request.Context()).First(a, a.Id).Error; err != nil {
		c.JSON(500, gin.H{"error": "account unavailable"})
		return
	}
	aliases := a.AlsoKnownAs
	if aliases == nil {
		aliases = []string{}
	}
	c.JSON(200, gin.H{"aliases": aliases, "moved_to": a.MovedTo, "deletion_requested": a.DeletionRequestedAt != nil})
}

func UpdateAliases(c *gin.Context) {
	a, _, ok := lifecycleIdentity(c)
	if !ok {
		return
	}
	var req struct {
		Aliases []string `json:"aliases"`
	}
	if c.ShouldBindJSON(&req) != nil || len(req.Aliases) > 10 {
		c.JSON(422, gin.H{"error": "up to 10 aliases are allowed"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	aliases := []string{}
	for _, input := range req.Aliases {
		account, err := activitypub.ResolveMigrationAccount(ctx, db.GetDB().WithContext(ctx), input)
		if err != nil || account.Id == a.Id {
			c.JSON(422, gin.H{"error": "alias account could not be resolved"})
			return
		}
		uri := account.ActorURI(systemsettings.GetBaseURL())
		found := false
		for _, old := range aliases {
			if old == uri {
				found = true
			}
		}
		if !found {
			aliases = append(aliases, uri)
		}
	}
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current models.Account
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, a.Id).Error; err != nil {
			return err
		}
		if current.MovedTo != "" || current.DeletionRequestedAt != nil {
			return activitypub.ErrMigrationState
		}
		encoded, _ := json.Marshal(aliases)
		if err := tx.Model(&current).UpdateColumn("also_known_as", string(encoded)).Error; err != nil {
			return err
		}
		return models.QueueFederationProfile(tx, current.Id)
	})
	if err != nil {
		c.JSON(422, gin.H{"error": "aliases could not be saved"})
		return
	}
	_ = cache.InvalidateAccount(ctx, a.Id)
	c.JSON(200, gin.H{"aliases": aliases, "moved_to": "", "deletion_requested": false})
}

func lifecyclePassword(c *gin.Context, userID uint64, password string) bool {
	if password == "" || len(password) > 1024 {
		c.JSON(422, gin.H{"error": "invalid password"})
		return false
	}
	var secret models.UserSecretData
	if err := db.GetDB().WithContext(c.Request.Context()).First(&secret, "user_id = ?", userID).Error; err != nil {
		c.JSON(500, gin.H{"error": "password verification unavailable"})
		return false
	}
	if secret.CheckPassword(password) != nil {
		c.JSON(422, gin.H{"error": "invalid password"})
		return false
	}
	return true
}

func MoveAccount(c *gin.Context) {
	a, u, ok := lifecycleIdentity(c)
	if !ok {
		return
	}
	var req struct {
		Target   string `json:"target"`
		Password string `json:"password"`
		Confirm  bool   `json:"confirm"`
	}
	if c.ShouldBindJSON(&req) != nil || !req.Confirm {
		c.JSON(422, gin.H{"error": "migration confirmation required"})
		return
	}
	if !lifecyclePassword(c, u.Id, req.Password) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	target, err := activitypub.ResolveMigrationAccount(ctx, db.GetDB().WithContext(ctx), req.Target)
	if err != nil {
		c.JSON(422, gin.H{"error": "target account could not be resolved"})
		return
	}
	err = activitypub.VerifyMigrationTarget(ctx, db.GetDB().WithContext(ctx), a, target, activitypub.NewFetchClient())
	if err == nil {
		err = activitypub.ApplyAccountMove(db.GetDB().WithContext(ctx), a.Id, target.Id, true)
	}
	if err != nil {
		message := "account migration failed"
		if errors.Is(err, activitypub.ErrMigrationAlias) || errors.Is(err, activitypub.ErrMigrationState) {
			message = err.Error()
		}
		c.JSON(422, gin.H{"error": message})
		return
	}
	_ = cache.InvalidateAccount(ctx, a.Id)
	c.JSON(200, gin.H{"aliases": a.AlsoKnownAs, "moved_to": target.ActorURI(systemsettings.GetBaseURL()), "deletion_requested": false})
}

func RequestAccountDeletion(c *gin.Context) {
	a, u, ok := lifecycleIdentity(c)
	if !ok {
		return
	}
	var req struct {
		Password     string `json:"password"`
		Confirmation string `json:"confirmation"`
		Confirm      bool   `json:"confirm"`
	}
	if c.ShouldBindJSON(&req) != nil || !req.Confirm || req.Confirmation != a.Username {
		c.JSON(422, gin.H{"error": "account deletion confirmation required"})
		return
	}
	if !lifecyclePassword(c, u.Id, req.Password) {
		return
	}
	if err := accountlifecycle.RequestDeletion(c.Request.Context(), db.GetDB(), a.Id, u.Id); err != nil {
		message := "account deletion could not be scheduled"
		if errors.Is(err, accountlifecycle.ErrLastAdministrator) {
			message = err.Error()
		}
		c.JSON(422, gin.H{"error": message})
		return
	}
	_ = cache.InvalidateAccount(c.Request.Context(), a.Id)
	c.SetCookie("app-token", "", -1, "/", "", c.Request.TLS != nil, true)
	c.JSON(202, gin.H{"state": "pending"})
}
