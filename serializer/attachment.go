package serializer

import (
	"github.com/kasuganosora/community/models"
)

// Attachment is the Mastodon MediaAttachment entity used inside statuses.
// It is the same shape returned by the /api/v1/media endpoints.
type Attachment = MediaAttachmentResponse

// ToAttachment serializes a media attachment for status responses.
func ToAttachment(attachment *models.Attachment) *Attachment {
	att, _ := ToMediaAttachment(attachment) //nolint:errcheck // ToMediaAttachment never fails

	return att
}

// ToAttachments serializes a list of media attachments, skipping nil entries.
func ToAttachments(attachments []*models.Attachment) []*Attachment {
	atts := make([]*Attachment, 0, len(attachments))

	for _, attachment := range attachments {
		if att := ToAttachment(attachment); att != nil {
			atts = append(atts, att)
		}
	}

	return atts
}
