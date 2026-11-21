package serializer

import (
	"encoding/json"
	"strconv"

	"github.com/kasuganosora/community/models"
)

type ScheduledStatus struct {
	Id               string                `json:"id"`
	ScheduledAt      *APITime              `json:"scheduled_at"`
	Params           ScheduledStatusParams `json:"params"`
	MediaAttachments []*Attachment         `json:"media_attachments"`
}

type ScheduledStatusParams struct {
	Poll                *ScheduledStatusPollParams `json:"poll"`
	Text                string                     `json:"text"`
	MediaIds            []string                   `json:"media_ids"`
	Sensitive           bool                       `json:"sensitive"`
	Visibility          string                     `json:"visibility"`
	Idempotency         *string                    `json:"idempotency"`
	ScheduledAt         *string                    `json:"scheduled_at"`
	SpoilerText         *string                    `json:"spoiler_text"`
	ApplicationId       *uint64                    `json:"application_id"`
	InReplyToId         *string                    `json:"in_reply_to_id"`
	Language            *string                    `json:"language"`
	WithRateLimit       bool                       `json:"with_rate_limit"`
	AllowedMentions     []string                   `json:"allowed_mentions"`
	QuotedStatusId      *string                    `json:"quoted_status_id"`
	QuoteApprovalPolicy string                     `json:"quote_approval_policy"`
}

type ScheduledStatusPollParams struct {
	ExpiresIn  *uint64  `json:"expires_in"`
	Multiple   bool     `json:"multiple"`
	Options    []string `json:"options"`
	HideTotals *bool    `json:"hide_totals"`
}

func ToScheduledStatus(status *models.Status) (ret *ScheduledStatus) {
	ret = &ScheduledStatus{
		Id:          strconv.FormatUint(status.Id, 10),
		ScheduledAt: NewAPITime(status.ScheduledAt),
	}

	if status.Poll != nil {
		poll := &ScheduledStatusPollParams{Multiple: status.Poll.Multiple}
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

	if status.Content.SpoilerText != "" {
		ret.Params.SpoilerText = &status.Content.SpoilerText
	}
	ret.Params.QuoteApprovalPolicy = "nobody"
	if status.Visibility == models.StatusVisibilityPublic || status.Visibility == models.StatusVisibilityUnlisted {
		ret.Params.QuoteApprovalPolicy = "public"
	}
	if status.QuoteId != nil && *status.QuoteId > 0 {
		ret.Params.QuotedStatusId = new(strconv.FormatUint(*status.QuoteId, 10))
	}

	ret.Params.ApplicationId = status.ApplicationId
	if legacy, ok := status.Properties["application_id"]; ret.Params.ApplicationId == nil && ok {
		if raw, err := json.Marshal(legacy); err == nil {
			if id, err := strconv.ParseUint(string(raw), 10, 64); err == nil {
				ret.Params.ApplicationId = &id
			}
		}
	}

	if status.InReplyToId != nil {
		ret.Params.InReplyToId = new(strconv.FormatUint(*status.InReplyToId, 10))
	}

	ret.Params.Language = normalizeLanguage(status.Language)

	ret.MediaAttachments = ToAttachments(status.Attachments)
	for _, attachment := range ret.MediaAttachments {
		ret.Params.MediaIds = append(ret.Params.MediaIds, attachment.Id)
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
