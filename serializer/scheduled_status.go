package serializer

import (
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"strconv"
	"time"
)

type ScheduledStatus struct {
	Id               string     `json:"id"`
	ScheduledAt      *time.Time `json:"scheduled_at"`
	Params           ScheduledStatusParams
	MediaAttachments []*Attachment `json:"media_attachments"`
}

type ScheduledStatusParams struct {
	Poll          *Poll    `json:"poll"`
	Text          string   `json:"text"`
	MediaIds      []string `json:"media_ids"`
	Sensitive     bool     `json:"sensitive"`
	Visibility    string   `json:"visibility"`
	Idempotency   *string  `json:"idempotency"`
	ScheduledAt   *string  `json:"scheduled_at"`
	SpoilerText   string   `json:"spoiler_text"`
	ApplicationId *uint64  `json:"application_id"`
	InReplyToId   *string  `json:"in_reply_to_id"`
	Language      *string  `json:"language"`
	WithRateLimit *bool    `json:"with_rate_limit"`
}

type ScheduledStatusPollParams struct {
	ExpiresIn  *uint64  `json:"expires_in"`
	Multiple   bool     `json:"multiple"`
	Options    []string `json:"options"`
	HideTotals *bool    `json:"hide_totals"`
}

func ToScheduledStatus(status *models.Status) (ret *ScheduledStatus) {
	ret = &ScheduledStatus{
		Id: strconv.FormatUint(status.Id, 10),
	}

	if status.ScheduledAt != nil {
		ret.ScheduledAt = status.ScheduledAt
		ret.Params.ScheduledAt = utils.GetPtr(status.ScheduledAt.Format(time.RFC3339))
	}

	if status.Poll != nil {
		poll := &ScheduledStatusPollParams{}
		if status.Poll.ExpiresAt != nil {
			poll.ExpiresIn = utils.GetPtr(status.Poll.Properties["expires_in"].(uint64))
		}

		poll.Multiple = status.Poll.Properties["multiple"].(bool)
		poll.Options = status.Poll.Options.GetNames()
		poll.HideTotals = utils.GetPtr(status.Poll.Properties["hide_totals"].(bool))
	}

	ret.Params.Text = status.Content.Content
	ret.Params.Sensitive = status.Sensitive
	ret.Params.Visibility = status.GetVisibilityString()
	ret.Params.SpoilerText = status.Content.SpoilerText
	if appId, ok := status.Properties["application_id"]; ok {
		ret.Params.ApplicationId = utils.GetPtr(appId.(uint64))
	}

	if status.InReplyToId != nil {
		ret.Params.InReplyToId = utils.GetPtr(strconv.FormatUint(*status.InReplyToId, 10))
	}

	ret.Params.Language = &status.Language

	ret.MediaAttachments = make([]*Attachment, 0)
	ret.Params.MediaIds = make([]string, 0)

	if status.Attachments != nil {
		for _, attachment := range status.Attachments {
			ret.MediaAttachments = append(ret.MediaAttachments, ToAttachment(attachment))
			ret.Params.MediaIds = append(ret.Params.MediaIds, strconv.FormatUint(attachment.Id, 10))
		}
	}

	return
}

func ToScheduledStatuses(statuses []*models.Status) (ret []*ScheduledStatus) {
	for _, status := range statuses {
		ret = append(ret, ToScheduledStatus(status))
	}

	return
}
