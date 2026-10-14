package status

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// {"status":"所谓的穷游不是旅游，而是修炼","in_reply_to_id":null,"media_ids":[],"sensitive":false,"spoiler_text":"","visibility":"public","poll":null,"language":"zh"}
// {"status":"test poll","in_reply_to_id":null,"media_ids":[],"sensitive":false,"spoiler_text":"","visibility":"public","poll":{"options":["a","b"],"expires_in":"604800","multiple":false},"language":"zh"}
// {"status":"test upload","in_reply_to_id":null,"media_ids":["110738601623414564"],"sensitive":false,"spoiler_text":"","visibility":"public","poll":null,"language":"zh"}

type CreateStatusRequest struct {
	Status      string             `json:"status"`
	InReplyToId string             `binding:"omitempty,numeric"                             json:"in_reply_to_id"`
	MediaIds    []string           `json:"media_ids"`
	Sensitive   bool               `json:"sensitive"`
	SpoilerText string             `json:"spoiler_text"`
	Visibility  string             `binding:"required,oneof=public unlisted private direct" json:"visibility"`
	Poll        *CreatePollContent `json:"poll"`
	Language    string             `json:"language"`
	QuoteId     string             `binding:"omitempty,numeric"                             json:"quote_id"`
}

type CreatePollContent struct {
	Options    []string `json:"options"`
	ExpiresIn  uint64   `binding:"required,numeric" json:"expires_in"`
	Multiple   bool     `json:"multiple"`
	HideTotals bool     `json:"hide_totals"`
}

func Create(c *gin.Context) {
	var req CreateStatusRequest
	if err := controllers.BindBody(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	if msg := validateCreateStatusRequest(&req); msg != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": msg})

		return
	}

	currentAccount := controllers.MustGetAccount(c)

	status := &models.Status{
		AccountId: currentAccount.Id,
		Content:   models.StatusContent{Content: req.Status, SpoilerText: req.SpoilerText},
		Sensitive: req.Sensitive,
		Language:  req.Language,
	}

	err := status.SetVisibilityByString(req.Visibility)
	controllers.AbortIfError(c, err)

	if req.InReplyToId != "" {
		var inReplyToId uint64

		inReplyToId, err = strconv.ParseUint(req.InReplyToId, 10, 64)
		controllers.AbortIfError(c, err)

		var parent models.Status
		if err := db.GetDB().Preload("ToAccounts").First(&parent, inReplyToId).Error; err != nil {
			c.JSON(404, gin.H{"error": "reply target not found"})
			return
		}
		allowed, err := parent.CanViewEx(db.GetDB(), currentAccount)
		if err != nil {
			controllers.AbortIfError(c, err)
			return
		}
		if !allowed {
			c.JSON(403, gin.H{"error": "reply target is not visible"})
			return
		}
		if !models.ValidateReplyVisibility(&parent, status.Visibility) {
			c.JSON(422, gin.H{"error": "Reply visibility cannot be broader than its parent"})
			return
		}
		status.InReplyToId = &inReplyToId
		status.InReplyToAccountId = &parent.AccountId
	}

	if req.QuoteId != "" {
		quoteId, parseErr := strconv.ParseUint(req.QuoteId, 10, 64)
		controllers.AbortIfError(c, parseErr)

		var quoted models.Status
		if err := db.GetDB().First(&quoted, quoteId).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "quote target not found"})
			return
		}
		if quoted.ReblogId != nil && *quoted.ReblogId > 0 {
			quoteId = *quoted.ReblogId
			if err := db.GetDB().First(&quoted, quoteId).Error; err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "quote target not found"})
				return
			}
		}
		allowed, viewErr := quoted.CanViewEx(db.GetDB(), currentAccount)
		if viewErr != nil {
			controllers.AbortIfError(c, viewErr)
			return
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "quote target is not visible"})
			return
		}
		switch quoted.Visibility {
		case models.StatusVisibilityPublic, models.StatusVisibilityUnlisted:
		default:
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "只能引用公开或不公开的帖子"})
			return
		}
		status.QuoteId = &quoteId
	}

	if req.Poll != nil {
		expiresAt := time.Now().Add(time.Duration(req.Poll.ExpiresIn) * time.Second) //nolint:gosec // G115: reviewed and safe
		poll := &models.Poll{
			AccountId: currentAccount.Id,
			ExpiresAt: &expiresAt,
			Multiple:  req.Poll.Multiple,
			Properties: map[string]any{
				"expires_in":  req.Poll.ExpiresIn,
				"hide_totals": req.Poll.HideTotals,
			},
		}

		for i, option := range req.Poll.Options {
			poll.Options = append(poll.Options, &models.PollOption{Content: option, Index: uint8(i)})
		}

		status.Poll = poll
	}

	if len(req.MediaIds) > 0 {
		attachments, ok := loadAttachableMedia(req.MediaIds, currentAccount.Id, 0)
		if !ok {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid media_ids"})

			return
		}

		// Saved with the status: GORM upserts has-many associations and
		// sets media_attachments.status_id before the after_create hooks run.
		status.Attachments = attachments
	}

	createDB := db.GetDB().WithContext(c.Request.Context())
	if status.Visibility == models.StatusVisibilityDirect {
		recipients, err := prepareDirectRecipients(c.Request.Context(), req.Status, currentAccount.Id, status.InReplyToAccountId)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		createDB = createDB.WithContext(models.WithStructuredRecipientIDs(c.Request.Context(), recipients))
	}
	err = createDB.Create(status).Error
	if err != nil {
		_ = c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create status"})

		return
	}

	now := time.Now()
	currentAccount.LastStatusAt = &now
	currentAccount.LastActivityAt = &now
	err = db.GetDB().Select("last_status_at", "last_activity_at").Save(currentAccount).Error

	controllers.AbortIfError(c, err)

	returnFullSingleStatus(c, status, currentAccount)
}

// validateCreateStatusRequest enforces the limits advertised by
// /api/v1/instance (configuration.statuses / configuration.polls). They were
// previously not checked at all: empty posts, posts far above
// max_characters (large ones failing with a DB "Data too long" 500),
// one-option polls and polls with more than max_options were accepted.
// It returns an error message, or "" if the request is valid.
func validateCreateStatusRequest(req *CreateStatusRequest) string {
	if strings.TrimSpace(req.Status) == "" && len(req.MediaIds) == 0 && req.Poll == nil && strings.TrimSpace(req.QuoteId) == "" {
		return "Validation failed: Text can't be blank"
	}

	maxChars := systemsettings.GetInt(systemsettings.KeyStatusMaxCharacters, 500)
	if maxChars > 0 && utf8.RuneCountInString(req.Status)+utf8.RuneCountInString(req.SpoilerText) > maxChars {
		return fmt.Sprintf("Validation failed: Text character limit of %d exceeded", maxChars)
	}

	maxMedia := systemsettings.GetInt(systemsettings.KeyStatusMaxMedia, 4)
	if maxMedia > 0 && len(req.MediaIds) > maxMedia {
		return fmt.Sprintf("Validation failed: Too many media attachments (max %d)", maxMedia)
	}

	if req.Poll != nil {
		if len(req.MediaIds) > 0 {
			return "Validation failed: Cannot attach both media and a poll"
		}

		maxOptions := systemsettings.GetInt(systemsettings.KeyPollMaxOptions, 4)
		if len(req.Poll.Options) < 2 || (maxOptions > 0 && len(req.Poll.Options) > maxOptions) {
			return fmt.Sprintf("Validation failed: Poll must have between 2 and %d options", maxOptions)
		}

		maxOptionChars := systemsettings.GetInt(systemsettings.KeyPollMaxCharactersPerOption, 50)
		for _, option := range req.Poll.Options {
			if strings.TrimSpace(option) == "" {
				return "Validation failed: Poll options can't be blank"
			}

			if maxOptionChars > 0 && utf8.RuneCountInString(option) > maxOptionChars {
				return fmt.Sprintf("Validation failed: Poll option character limit of %d exceeded", maxOptionChars)
			}
		}

		minExp := systemsettings.GetInt64(systemsettings.KeyPollMinExpiration, 300)
		maxExp := systemsettings.GetInt64(systemsettings.KeyPollMaxExpiration, 2629746)

		if expiresIn := int64(req.Poll.ExpiresIn); expiresIn < minExp || (maxExp > 0 && expiresIn > maxExp) { //nolint:gosec // G115: bounded by the check itself
			return fmt.Sprintf("Validation failed: Poll expiration must be between %d and %d seconds", minExp, maxExp)
		}
	}

	return ""
}
