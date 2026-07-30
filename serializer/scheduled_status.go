package serializer

import (
	"github.com/kasuganosora/community/models"
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
	Poll          *ScheduledStatusPollParams `json:"poll"`
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
		ret.Params.ScheduledAt = new(status.ScheduledAt.Format(time.RFC3339))
	}

	if status.Poll != nil {
		poll := &ScheduledStatusPollParams{}
		if status.Poll.ExpiresAt != nil {
			if v, ok := status.Poll.Properties["expires_in"].(float64); ok {
				expiresIn := uint64(v)
				poll.ExpiresIn = &expiresIn
			} else if v, ok := status.Poll.Properties["expires_in"].(uint64); ok {
				poll.ExpiresIn = &v
			}
		}

		if v, ok := status.Poll.Properties["multiple"].(bool); ok {
			poll.Multiple = v
		}
		poll.Options = status.Poll.Options.GetNames()
		if v, ok := status.Poll.Properties["hide_totals"].(bool); ok {
			poll.HideTotals = &v
		}
		ret.Params.Poll = poll
	}

	ret.Params.Text = status.Content.Content
	ret.Params.Sensitive = status.Sensitive
	ret.Params.Visibility = status.GetVisibilityString()

	ret.Params.SpoilerText = status.Content.SpoilerText

	if appId, ok := status.Properties["application_id"]; ok {
		ret.Params.ApplicationId = new(appId.(uint64)) //nolint:forcetypeassert
	}

	if status.InReplyToId != nil {
		ret.Params.InReplyToId = new(strconv.FormatUint(*status.InReplyToId, 10))
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

	return ret
}

func ToScheduledStatuses(statuses []*models.Status) (ret []*ScheduledStatus) {
	ret = make([]*ScheduledStatus, 0, len(statuses))
	for _, status := range statuses {
		ret = append(ret, ToScheduledStatus(status))
	}

	return
}
