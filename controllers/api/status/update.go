package status

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdateStatusRequest is retained as the JSON shape for delete-and-repost.
// PUT no longer edits a status; clients must explicitly choose POST /replace.
type UpdateStatusRequest struct {
	Status      *string            `json:"status"`
	InReplyToId *string            `json:"in_reply_to_id"`
	MediaIds    *[]string          `json:"media_ids"`
	Sensitive   *bool              `json:"sensitive"`
	SpoilerText *string            `json:"spoiler_text"`
	Visibility  *string            `binding:"omitempty,oneof=public unlisted private direct" json:"visibility"`
	Poll        *CreatePollContent `json:"poll"`
	Language    *string            `json:"language"`
}

type replaceRequest struct {
	UpdateStatusRequest
	pollSet  bool
	replySet bool
}

func (r *replaceRequest) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &r.UpdateStatusRequest); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	_, r.pollSet = fields["poll"]
	_, r.replySet = fields["in_reply_to_id"]
	return nil
}

func Update(c *gin.Context) {
	c.Header("Allow", "GET, DELETE")
	c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "Editing is not supported. Use POST /api/v1/statuses/:id/replace to delete and repost with a new ID."})
}

type replacementError struct {
	code    int
	message string
}

func (e replacementError) Error() string { return e.message }
func invalidReplacement(message string) error {
	return replacementError{http.StatusUnprocessableEntity, message}
}

// Replace publishes a new identity and deletes the original in one database
// transaction. The old post and its media survive any validation/write failure.
func Replace(c *gin.Context) {
	var req replaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	owner := controllers.MustGetAccount(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(422, gin.H{"error": "id must be a number"})
		return
	}
	// Warm the shared validation settings before acquiring a connection/row
	// lock; first-use settings loading must not borrow a second connection
	// from a pool already exhausted by replacement transactions.
	_ = systemsettings.GetInt(systemsettings.KeyStatusMaxCharacters, 500)
	directSnapshot, directRecipients, err := prepareReplacementDirectRecipients(c.Request.Context(), id, owner, &req)
	if err != nil {
		var rejected replacementError
		switch {
		case errors.As(err, &rejected):
			c.JSON(rejected.code, gin.H{"error": rejected.message})
		case errors.Is(err, gorm.ErrRecordNotFound):
			c.JSON(404, gin.H{"error": "status or reply target not found"})
		default:
			_ = c.Error(err)
			c.JSON(500, gin.H{"error": "Failed to prepare replacement; the original post was retained"})
		}
		return
	}
	var replacement *models.Status
	transactionContext, publishStatusEvents := models.BeginDeferredStatusEvents(c.Request.Context())
	err = db.GetDB().WithContext(transactionContext).Transaction(func(tx *gorm.DB) error {
		original := &models.Status{}
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id)
		if err := models.StatusLoadAllQuery(query).First(original).Error; err != nil {
			return err
		}
		if directSnapshot != nil && !sameDirectRecipientSnapshot(directSnapshot, original) {
			return replacementError{409, "The original post changed while resolving recipients; retry the replacement"}
		}
		if original.AccountId != owner.Id || !original.IsLocal() {
			return replacementError{403, "forbidden"}
		}
		if original.ReblogId != nil || original.ScheduledAt != nil {
			return invalidReplacement("Only published original posts can be deleted and reposted")
		}
		next := &models.Status{AccountId: owner.Id, ApplicationId: original.ApplicationId, Content: models.StatusContent{Content: original.Content.Content, SpoilerText: original.Content.SpoilerText}, Sensitive: original.Sensitive, Language: original.Language, Visibility: original.Visibility, InReplyToId: original.InReplyToId, InReplyToAccountId: original.InReplyToAccountId}
		if req.Status != nil {
			next.Content.Content = *req.Status
		}
		if req.SpoilerText != nil {
			next.Content.SpoilerText = *req.SpoilerText
		}
		if req.Sensitive != nil {
			next.Sensitive = *req.Sensitive
		}
		if req.Language != nil {
			next.Language = *req.Language
		}
		if req.Visibility != nil {
			if err := next.SetVisibilityByString(*req.Visibility); err != nil {
				return invalidReplacement("Invalid visibility value")
			}
		}
		if req.replySet {
			next.InReplyToId = nil
			next.InReplyToAccountId = nil
			if req.InReplyToId != nil && *req.InReplyToId != "" {
				parent, err := strconv.ParseUint(*req.InReplyToId, 10, 64)
				if err != nil || parent == 0 {
					return invalidReplacement("invalid in_reply_to_id")
				}
				next.InReplyToId = &parent
			}
		}
		if next.InReplyToId != nil {
			if *next.InReplyToId == id {
				return invalidReplacement("Replacement cannot reply to the post being deleted")
			}
			var parent models.Status
			if err := tx.Preload("ToAccounts").First(&parent, *next.InReplyToId).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return invalidReplacement("Reply target no longer exists")
				}
				return err
			}
			visible, err := parent.CanViewEx(tx, owner)
			if err != nil {
				return err
			}
			if !visible {
				return replacementError{403, "Reply target is not visible"}
			}
			if !models.ValidateReplyVisibility(&parent, next.Visibility) {
				return invalidReplacement("Reply visibility cannot be broader than its parent")
			}
			next.InReplyToAccountId = &parent.AccountId
		}
		mediaIDs := make([]string, 0, len(original.Attachments))
		for _, media := range original.Attachments {
			mediaIDs = append(mediaIDs, strconv.FormatUint(media.Id, 10))
		}
		if req.MediaIds != nil {
			mediaIDs = *req.MediaIds
		}
		attachments, ok := loadAttachableMediaDB(tx.Clauses(clause.Locking{Strength: "UPDATE"}), mediaIDs, owner.Id, id)
		if !ok {
			return invalidReplacement("invalid media_ids")
		}
		next.Attachments = attachments
		validation := CreateStatusRequest{Status: next.Content.Content, SpoilerText: next.Content.SpoilerText, MediaIds: mediaIDs, Visibility: next.GetVisibilityString(), Sensitive: next.Sensitive, Language: next.Language}
		if req.pollSet {
			validation.Poll = req.Poll
			if req.Poll != nil {
				next.Poll = pollFromReplacement(owner.Id, req.Poll)
			}
		} else if original.Poll != nil {
			next.Poll = cloneReplacementPoll(original.Poll)
			// Existing poll expiry is preserved, including an already-closed poll.
			// Validate its choices/media combination without inventing a new expiry.
			validation.Poll = &CreatePollContent{Options: next.Poll.Options.GetNames(), Multiple: next.Poll.Multiple, ExpiresIn: uint64(systemsettings.GetInt64(systemsettings.KeyPollMinExpiration, 300))}
		}
		if message := validateCreateStatusRequest(&validation); message != "" {
			return invalidReplacement(message)
		}
		createTx := tx.WithContext(models.WithDeferredFederationCreate(tx.Statement.Context))
		if next.Visibility == models.StatusVisibilityDirect {
			if directSnapshot == nil {
				return invalidReplacement("Direct recipients were not validated")
			}
			createTx = createTx.WithContext(models.WithStructuredRecipientIDs(createTx.Statement.Context, directRecipients))
		}
		if err := createTx.Create(next).Error; err != nil {
			return fmt.Errorf("create replacement: %w", err)
		}
		if err := tx.Delete(original).Error; err != nil {
			return fmt.Errorf("delete original: %w", err)
		}
		// Delete is now queued before Create, while both become visible at commit.
		if err := models.QueueFederationReplacement(tx, next); err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&models.Account{}).Where("id = ?", owner.Id).Updates(map[string]any{"last_status_at": now, "last_activity_at": now}).Error; err != nil {
			return err
		}
		replacement = next
		return nil
	})
	if err != nil {
		var rejected replacementError
		switch {
		case errors.As(err, &rejected):
			c.JSON(rejected.code, gin.H{"error": rejected.message})
		case errors.Is(err, gorm.ErrRecordNotFound):
			c.JSON(404, gin.H{"error": "status not found"})
		default:
			_ = c.Error(err)
			c.JSON(500, gin.H{"error": "Failed to delete and repost; the original post was retained"})
		}
		return
	}
	publishStatusEvents()
	returnFullSingleStatus(c, replacement, owner)
}

func pollFromReplacement(accountID uint64, req *CreatePollContent) *models.Poll {
	expiry := time.Now().Add(time.Duration(req.ExpiresIn) * time.Second)
	poll := &models.Poll{AccountId: accountID, ExpiresAt: &expiry, Multiple: req.Multiple, Properties: map[string]any{"expires_in": req.ExpiresIn, "hide_totals": req.HideTotals}}
	for i, text := range req.Options {
		poll.Options = append(poll.Options, &models.PollOption{Content: text, Index: uint8(i)})
	}
	return poll
}
func cloneReplacementPoll(original *models.Poll) *models.Poll {
	poll := &models.Poll{AccountId: original.AccountId, ExpiresAt: original.ExpiresAt, Multiple: original.Multiple, Properties: original.Properties}
	for _, option := range original.Options {
		poll.Options = append(poll.Options, &models.PollOption{Content: option.Content, Index: option.Index})
	}
	return poll
}
