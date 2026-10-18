// Package connections manages personal credentials and outbound integrations.
package connections

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/adminwebhook"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// An API token must not mint credentials with greater privileges or configure
// ongoing private-data delivery. These operations require a web login session.
func WebSession(c *gin.Context) {
	data, ok := c.Get("token_data")
	token, valid := data.(*models.AuthTokenData)
	if !ok || !valid || token == nil || token.AuthType != models.AuthTokenTypeWeb {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "web_session_required"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	c.Next()
}
func owner(c *gin.Context) *models.Account { return c.MustGet("account").(*models.Account) }
func conn(c *gin.Context) *gorm.DB         { return db.GetDB().WithContext(c.Request.Context()) }
func failure(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.AbortWithStatusJSON(404, gin.H{"error": "not_found"})
		return
	}
	c.AbortWithStatusJSON(500, gin.H{"error": "server_error"})
}
func invalid(c *gin.Context) { c.AbortWithStatusJSON(422, gin.H{"error": "invalid_settings"}) }

func Tokens(c *gin.Context) {
	rows := []models.PersonalAccessToken{}
	if err := conn(c).Where("user_id = ?", owner(c).UserId).Order("created_at DESC").Find(&rows).Error; err != nil {
		failure(c, err)
		return
	}
	c.JSON(200, rows)
}
func CreateToken(c *gin.Context) {
	var input struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays int      `json:"expires_in_days"`
	}
	if c.ShouldBindJSON(&input) != nil {
		invalid(c)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 100 || len(input.Scopes) == 0 || len(input.Scopes) > 2 || (len(input.Scopes) == 2 && input.Scopes[0] == input.Scopes[1]) || !slices.Contains([]int{0, 7, 30, 90, 365}, input.ExpiresInDays) {
		invalid(c)
		return
	}
	scopes := &models.ApplicationScope{}
	for _, s := range input.Scopes {
		switch s {
		case "read":
			scopes.ReadScope = models.AppScopeRead
		case "write":
			scopes.WriteScope = models.AppScopeWrite
		default:
			invalid(c)
			return
		}
	}
	row, raw := models.NewAuthToken(owner(c).UserId, models.AuthTokenTypeOauth, 0, 0, scopes, input.ExpiresInDays*86400, config.C.EncryptedKey)
	row.ExternalClient = models.PersonalTokenClient
	if raw == "" {
		failure(c, errors.New("token generation failed"))
		return
	}
	meta := models.PersonalAccessToken{ID: uuid.NewString(), UserID: owner(c).UserId, Name: input.Name, TokenHash: row.Token, Scopes: input.Scopes, ExpiresAt: row.ExpiredAt}
	err := conn(c).Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, meta.UserID).Error; err != nil {
			return err
		}
		if user.IsBan || user.Status != models.UserStatusNormal {
			return gorm.ErrRecordNotFound
		}
		var count int64
		if err := tx.Model(&models.PersonalAccessToken{}).Where("user_id = ?", meta.UserID).Count(&count).Error; err != nil {
			return err
		}
		if count >= 50 {
			return errLimit
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		return tx.Create(&meta).Error
	})
	if errors.Is(err, errLimit) {
		c.JSON(422, gin.H{"error": "integration_limit"})
		return
	}
	if err != nil {
		failure(c, err)
		return
	}
	c.JSON(201, gin.H{"token": raw, "item": meta})
}

var errLimit = errors.New("integration limit reached")

func RevokeToken(c *gin.Context) {
	var meta models.PersonalAccessToken
	err := conn(c).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND user_id = ?", c.Param("id"), owner(c).UserId).First(&meta).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.AuthToken{}).Where("token = ? AND user_id = ?", meta.TokenHash, meta.UserID).Update("revoke", time.Now()).Error; err != nil {
			return err
		}
		return tx.Delete(&meta).Error
	})
	if err != nil {
		failure(c, err)
		return
	}
	middleware.InvalidateTokenCacheHash(meta.TokenHash)
	c.Status(204)
}

func Webhooks(c *gin.Context) {
	rows := []models.UserWebhook{}
	if err := conn(c).Where("account_id = ?", owner(c).Id).Order("created_at DESC").Find(&rows).Error; err != nil {
		failure(c, err)
		return
	}
	c.JSON(200, rows)
}

type hookInput struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Secret  string   `json:"secret"`
	Events  []string `json:"events"`
	Enabled bool     `json:"enabled"`
}

func (i *hookInput) valid() bool {
	i.Name = strings.TrimSpace(i.Name)
	i.URL = strings.TrimSpace(i.URL)
	if i.Name == "" || utf8.RuneCountInString(i.Name) > 100 || adminwebhook.ValidateURL(i.URL) != nil || len(i.Events) == 0 || len(i.Events) > len(models.UserWebhookEvents) || len(i.Secret) > 256 {
		return false
	}
	seen := map[string]bool{}
	for _, event := range i.Events {
		if !slices.Contains(models.UserWebhookEvents, event) || seen[event] {
			return false
		}
		seen[event] = true
	}
	return true
}
func SaveWebhook(c *gin.Context) {
	var input hookInput
	if c.ShouldBindJSON(&input) != nil || !input.valid() {
		invalid(c)
		return
	}
	row := models.UserWebhook{}
	creating := c.Param("id") == ""
	generated := ""
	err := conn(c).Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, owner(c).UserId).Error; err != nil {
			return err
		}
		if user.IsBan || user.Status != models.UserStatusNormal {
			return gorm.ErrRecordNotFound
		}
		if creating {
			var count int64
			if err := tx.Model(&row).Where("account_id = ?", owner(c).Id).Count(&count).Error; err != nil {
				return err
			}
			if count >= 10 {
				return errLimit
			}
			row.ID = uuid.NewString()
			row.AccountID = owner(c).Id
		} else if err := tx.Where("id = ? AND account_id = ?", c.Param("id"), owner(c).Id).First(&row).Error; err != nil {
			return err
		}
		if !creating && (row.URL != input.URL || input.Secret != "" || !input.Enabled) {
			if err := tx.Model(&models.UserWebhookDelivery{}).Where("webhook_id = ? AND state IN ?", row.ID, []string{"pending", "processing"}).Updates(map[string]any{"state": "cancelled", "payload": "", "lease_token": ""}).Error; err != nil {
				return err
			}
		}
		row.Name = input.Name
		row.URL = input.URL
		row.Events = input.Events
		row.Enabled = input.Enabled
		if input.Secret != "" {
			row.Secret = input.Secret
		}
		if row.Secret == "" {
			var random [32]byte
			if _, err := rand.Read(random[:]); err != nil {
				return err
			}
			row.Secret = hex.EncodeToString(random[:])
			generated = row.Secret
		}
		return tx.Save(&row).Error
	})
	if errors.Is(err, errLimit) {
		c.JSON(422, gin.H{"error": "integration_limit"})
		return
	}
	if err != nil {
		failure(c, err)
		return
	}
	code := 200
	if creating {
		code = 201
	}
	c.JSON(code, gin.H{"item": row, "secret": generated})
}
func DeleteWebhook(c *gin.Context) {
	err := conn(c).Transaction(func(tx *gorm.DB) error {
		var row models.UserWebhook
		if err := tx.Where("id = ? AND account_id = ?", c.Param("id"), owner(c).Id).First(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("webhook_id = ?", row.ID).Delete(&models.UserWebhookDelivery{}).Error; err != nil {
			return err
		}
		return tx.Delete(&row).Error
	})
	if err != nil {
		failure(c, err)
		return
	}
	c.Status(204)
}
func Deliveries(c *gin.Context) {
	var row models.UserWebhook
	if err := conn(c).Where("id = ? AND account_id = ?", c.Param("id"), owner(c).Id).First(&row).Error; err != nil {
		failure(c, err)
		return
	}
	rows := []models.UserWebhookDelivery{}
	if err := conn(c).Where("webhook_id = ?", row.ID).Order("created_at DESC").Limit(20).Find(&rows).Error; err != nil {
		failure(c, err)
		return
	}
	c.JSON(200, rows)
}
func TestWebhook(c *gin.Context) {
	err := conn(c).Transaction(func(tx *gorm.DB) error {
		var row models.UserWebhook
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND account_id = ?", c.Param("id"), owner(c).Id).First(&row).Error; err != nil {
			return err
		}
		if !row.Enabled {
			return errLimit
		}
		var count int64
		if err := tx.Model(&models.UserWebhookDelivery{}).Where("webhook_id = ? AND event = ? AND created_at > ?", row.ID, "test", time.Now().Add(-time.Minute)).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errLimit
		}
		id := uuid.NewString()
		return tx.Create(&models.UserWebhookDelivery{ID: id, WebhookID: row.ID, Source: "test:" + id, Event: "test", Payload: `{"type":"test","body":{"message":"Community webhook test"}}`, State: "pending", NextAttemptAt: time.Now()}).Error
	})
	if errors.Is(err, errLimit) {
		c.JSON(422, gin.H{"error": "enable_hook_and_wait_before_test"})
		return
	}
	if err != nil {
		failure(c, err)
		return
	}
	c.Status(202)
}
