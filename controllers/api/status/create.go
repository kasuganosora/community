package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"net/http"
	"strconv"
	"time"
)

// {"status":"所谓的穷游不是旅游，而是修炼","in_reply_to_id":null,"media_ids":[],"sensitive":false,"spoiler_text":"","visibility":"public","poll":null,"language":"zh"}
// {"status":"test poll","in_reply_to_id":null,"media_ids":[],"sensitive":false,"spoiler_text":"","visibility":"public","poll":{"options":["a","b"],"expires_in":"604800","multiple":false},"language":"zh"}
// {"status":"test upload","in_reply_to_id":null,"media_ids":["110738601623414564"],"sensitive":false,"spoiler_text":"","visibility":"public","poll":null,"language":"zh"}

type CreateStatusRequest struct {
	Status      string             `json:"status"`
	InReplyToId string             `json:"in_reply_to_id" binding:"omitempty,numeric"`
	MediaIds    []string           `json:"media_ids"`
	Sensitive   bool               `json:"sensitive"`
	SpoilerText string             `json:"spoiler_text"`
	Visibility  string             `json:"visibility" binding:"required,oneof=public unlisted private direct"`
	Poll        *CreatePollContent `json:"poll"`
	Language    string             `json:"language"`
}

type CreatePollContent struct {
	Options    []string `json:"options"`
	ExpiresIn  uint64   `json:"expires_in" binding:"required,numeric"`
	Multiple   bool     `json:"multiple"`
	HideTotals bool     `json:"hide_totals"`
}

func Create(c *gin.Context) {
	var req CreateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		status.InReplyToId = &inReplyToId
	}

	if req.Poll != nil {
		expiresAt := time.Now().Add(time.Duration(req.Poll.ExpiresIn) * time.Second)
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
		attachmentIds := make([]uint64, 0, len(req.MediaIds))
		for _, mediaId := range req.MediaIds {
			var attachmentId uint64
			attachmentId, err = strconv.ParseUint(mediaId, 10, 64)
			if err != nil {
				continue // ignore invalid media id
			}
			attachmentIds = append(attachmentIds, attachmentId)
		}

		if len(attachmentIds) > 0 {
			attachments := make([]*models.Attachment, 0, len(attachmentIds))
			err = db.GetDB().Where("id IN (?)", attachmentIds).Where("account_id = ?", currentAccount.Id).Find(&attachments).Error
			controllers.AbortIfError(c, err)

			status.Attachments = attachments
		}
	}

	err = db.GetDB().Create(status).Error

	if err != nil {
		_ = c.Error(err)
		return
	}

	now := time.Now()
	currentAccount.LastStatusAt = &now
	currentAccount.LastActivityAt = &now
	err = db.GetDB().Select("last_status_at", "last_activity_at").Save(currentAccount).Error

	controllers.AbortIfError(c, err)

	returnFullSingleStatus(c, status, currentAccount)
}
