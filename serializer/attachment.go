package serializer

import (
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"strconv"
)

type Attachment struct {
	Id               string         `json:"id"`
	Type             string         `json:"type"`
	Url              string         `json:"url"`
	PreviewUrl       *string        `json:"preview_url"`
	RemoteUrl        string         `json:"remote_url"`
	PreviewRemoteUrl string         `json:"preview_remote_url"`
	TextUrl          string         `json:"text_url"`
	Meta             map[string]any `json:"meta"`
	Description      string         `json:"description"`
	Blurhash         *string        `json:"blurhash"`
}

func ToAttachment(attachment *models.Attachment) (att *Attachment) {
	url := systemsettings.GetString(systemsettings.KeyMediaAttachmentsBaseURL, "") + attachment.SavePath

	var previewUrl *string
	if attachment.PreviewSavePath != "" {
		_previewUrl := systemsettings.GetString(systemsettings.KeyMediaAttachmentsCacheBaseURL, "") + attachment.PreviewSavePath
		previewUrl = &_previewUrl
	}
	att = &Attachment{
		Type:             attachment.GetTypeString(),
		Url:              url,
		PreviewUrl:       previewUrl,
		RemoteUrl:        attachment.RemoteUrl,
		PreviewRemoteUrl: attachment.PreviewRemoteUrl,
		TextUrl:          attachment.TextUrl,
		Meta:             attachment.Meta,
		Description:      attachment.Description,
		Blurhash:         attachment.BlurHash,
	}

	att.Id = strconv.FormatUint(attachment.Id, 10)

	return
}

func ToAttachments(attachments []*models.Attachment) (atts []*Attachment) {
	atts = make([]*Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		atts = append(atts, ToAttachment(attachment))
	}
	return
}
