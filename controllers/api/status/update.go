package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"time"
)

type UpdateStatusRequest struct {
	Status      *string            `json:"status"`
	InReplyToId *string            `json:"in_reply_to_id" binding:"omitempty,numeric"`
	MediaIds    *[]string          `json:"media_ids"`
	Sensitive   *bool              `json:"sensitive"`
	SpoilerText *string            `json:"spoiler_text"`
	Visibility  *string            `json:"visibility" binding:"required,oneof=public unlisted private direct"`
	Poll        *CreatePollContent `json:"poll"`
	Language    *string            `json:"language"`
}

func Update(c *gin.Context) {
	var req UpdateStatusRequest
	err := c.ShouldBindJSON(&req)
	controllers.AbortIf(c, err != nil, 400, gin.H{"error": err})

	currentAccount := controllers.MustGetAccount(c)

	statusIdStr := c.Param("id")
	statusId, err := strconv.ParseUint(statusIdStr, 10, 64)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": "id must be a number"})

	status := &models.Status{}
	query := db.GetDB().Model(&models.Status{}).Where("id = ?", statusId)
	query = models.StatusLoadAllQuery(query)

	err = query.First(status).Error
	controllers.AbortIfError(c, err)

	historyStatus := &models.StatusHistory{}
	historyStatus.StatusId = status.Id
	historyStatus.AccountId = currentAccount.Id
	historyStatus.SpoilerText = status.Content.SpoilerText
	historyStatus.Content = status.Content.Content
	historyStatus.Sensitive = status.Sensitive
	historyStatus.MediaAttachmentsIds = status.Attachments.GetIds()
	historyStatus.EmojisIds = status.Emojis.GetIds()

	controllers.AbortIf(c, status.AccountId != currentAccount.Id, 403, gin.H{"error": "forbidden"})

	var contentChanged bool
	if req.Status != nil {
		status.Content.Content = *req.Status
		contentChanged = true
	}

	if req.SpoilerText != nil {
		status.Content.SpoilerText = *req.SpoilerText
		contentChanged = true
	}

	var mediaIdsChanged bool
	if req.MediaIds != nil {
		attachmentsId := *req.MediaIds
		if len(attachmentsId) == 0 {
			status.Attachments = make([]*models.Attachment, 0)
		} else {
			attachments := make([]*models.Attachment, 0)
			err = db.GetDB().Model(&models.Attachment{}).Where("id IN ?", attachmentsId).
				Where("account_id = ?", currentAccount.Id).Find(&attachments).Error
			controllers.AbortIfError(c, err)
			controllers.AbortIf(c, len(attachments) != len(attachmentsId), 400, gin.H{"error": "invalid media_ids"})

			status.Attachments = attachments
		}

		mediaIdsChanged = true
	}

	var pollChanged bool
	var oldPoll *models.Poll
	if req.Poll != nil {
		if status.Poll != nil {
			oldPoll = status.Poll
		}

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
		pollChanged = true
	}

	if req.Language != nil {
		status.Language = *req.Language
	}

	if req.Visibility != nil {
		status.SetVisibilityByString(*req.Visibility)
	}

	err = db.GetDB().Transaction(func(tx *gorm.DB) (err error) {
		if contentChanged {
			err = tx.Save(&status.Content).Error
			if err != nil {
				err = errors.Wrap(err, "save status content failed")
				return
			}
		}

		if mediaIdsChanged {
			if len(status.Attachments) > 0 {
				err = tx.Model(status).Association("Attachments").Replace(status.Attachments)
			} else {
				err = tx.Model(status).Association("Attachments").Clear()
			}

			if err != nil {
				err = errors.Wrap(err, "save status attachments failed")
				return
			}
		}

		if pollChanged {
			if oldPoll != nil {
				err = oldPoll.Delete(tx)
				if err != nil {
					err = errors.Wrap(err, "delete old poll failed")
					return
				}
			}
			err = tx.Model(status).Association("Poll").Append(status.Poll)

			if err != nil {
				err = errors.Wrap(err, "save status poll failed")
				return
			}
		}

		err = tx.Omit(clause.Associations).Save(status).Error
		if err != nil {
			err = errors.Wrap(err, "save status failed")
			return
		}

		// 插入历史记录
		err = tx.Create(historyStatus).Error
		if err != nil {
			err = errors.Wrap(err, "create status history failed")
			return
		}

		return
	})

	controllers.AbortIfError(c, err)

	returnFullSingleStatus(c, status, currentAccount)
}
