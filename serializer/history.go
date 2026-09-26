package serializer

import (
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"time"
)

type HistoryStatus struct {
	Content          string        `json:"content"`
	SpoilerText      string        `json:"spoiler_text"`
	Sensitive        bool          `json:"sensitive"`
	CreatedAt        *time.Time    `json:"created_at"`
	Account          *Account      `json:"account"`
	Emojis           []*Emoji      `json:"emojis"`
	MediaAttachments []*Attachment `json:"media_attachments"`
}

func ToHistoryStatusBase(history *models.StatusHistory) *HistoryStatus {
	local := history.Account == nil || history.Account.IsLocal()

	spoiler := history.SpoilerText
	if !local {
		spoiler = htmlsafe.StripTags(spoiler)
	}

	return &HistoryStatus{
		Content:     htmlsafe.LocalOrRemote(history.Content, local, contentRenderOptions()),
		SpoilerText: spoiler,
		Sensitive:   history.Sensitive,
		CreatedAt:   history.CreatedAt,
		Account:     ToAccount(history.Account),
	}
}

func ToHistoryStatus(dbConn *gorm.DB, history *models.StatusHistory) (ret *HistoryStatus, err error) {
	ret = ToHistoryStatusBase(history)
	emojis := models.Emojis{}

	if len(history.EmojisIds) > 0 {
		err = dbConn.Model(&models.Emoji{}).Where("id in ?", history.EmojisIds).Find(&emojis).Error
		if err != nil {
			return
		}
	}

	ret.Emojis = ToEmojis(emojis)

	attachments := models.Attachments{}

	err = dbConn.Model(&models.Attachment{}).Where("id in ?", history.MediaAttachmentsIds).Find(&attachments).Error
	if err != nil {
		return
	}

	ret.MediaAttachments = ToAttachments(attachments)

	return
}

func ToHistoryStatuses(dbConn *gorm.DB, histories []*models.StatusHistory) (ret []*HistoryStatus, err error) {
	ret = make([]*HistoryStatus, 0, len(histories))
	emojiIds := make([]uint64, 0)
	AttachmentIds := make([]uint64, 0)

	for _, history := range histories {
		emojiIds = append(emojiIds, history.EmojisIds...)
		AttachmentIds = append(AttachmentIds, history.MediaAttachmentsIds...)
	}

	emojis := models.Emojis{}

	if len(emojiIds) > 0 {
		err = dbConn.Model(&models.Emoji{}).Where("id in ?", emojiIds).Find(&emojis).Error
		if err != nil {
			return ret, err
		}
	}

	attachments := models.Attachments{}

	if len(AttachmentIds) > 0 {
		err = dbConn.Model(&models.Attachment{}).Where("id in ?", AttachmentIds).Find(&attachments).Error
		if err != nil {
			return ret, err
		}
	}

	attachmentsMap := utils.GetStructsMap[uint64](attachments, "Id")
	emojisMap := utils.GetStructsMap[uint64](emojis, "Id")

	for _, history := range histories {
		item := ToHistoryStatusBase(history)

		_emojis := make([]*Emoji, 0, len(history.EmojisIds))

		for _, emojiId := range history.EmojisIds {
			if emoji, ok := emojisMap[emojiId]; ok {
				_emojis = append(_emojis, ToEmoji(emoji))
			}
		}

		item.Emojis = _emojis

		_attachments := make([]*Attachment, 0, len(history.MediaAttachmentsIds))

		for _, attachmentId := range history.MediaAttachmentsIds {
			if attachment, ok := attachmentsMap[attachmentId]; ok {
				_attachments = append(_attachments, ToAttachment(attachment))
			}
		}

		item.MediaAttachments = _attachments

		ret = append(ret, item)
	}

	return ret, err
}
