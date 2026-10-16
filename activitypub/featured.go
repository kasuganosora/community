package activitypub

import (
	"fmt"
	"html"
	"mime"
	urlpkg "net/url"
	"strings"
	"time"

	"github.com/kasuganosora/community/htmlsafe"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// Featured handles GET /users/:username/collections/featured
// Returns the user's pinned statuses as an OrderedCollection.
func Featured(c *gin.Context) {

	// Verify the local user exists
	var account models.Account
	err := localActorRouteQuery(db.GetDB().Preload("Extra"), c).First(&account).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"})

		return
	}

	baseURL := systemsettings.GetBaseURL()
	featuredURL := account.ActorURI(baseURL) + "/collections/featured"

	// Get pinned status IDs
	var pinnedStatuses []models.PinStatus

	db.GetDB().Where("account_id = ?", account.Id).
		Order("created_at DESC").
		Limit(10).
		Find(&pinnedStatuses)

	// Get status IDs
	statusIds := make([]uint64, 0, len(pinnedStatuses))
	for _, p := range pinnedStatuses {
		statusIds = append(statusIds, p.StatusId)
	}

	// Fetch statuses
	statuses := make([]*models.Status, 0)
	if len(statusIds) > 0 {
		db.GetDB().Preload("Content").
			Preload("Attachments").
			Preload("Emojis").
			Preload("ToAccounts").Preload("ReplyAccount").
			Preload("Tags").
			Where("id IN ?", statusIds).
			Where("account_id = ? AND visibility IN ? AND local_only = false AND deleted_at IS NULL AND scheduled_at IS NULL", account.Id, []int{int(models.StatusVisibilityPublic), int(models.StatusVisibilityUnlisted)}).
			Find(&statuses)
	}

	// Build ordered items
	items := make([]any, 0, len(statuses))

	for _, status := range statuses {
		note := buildNoteFromStatus(status, &account, baseURL)
		items = append(items, note)
	}

	// Build response using Collection type which has TotalItems
	response := Collection{
		Context:      []string{"https://www.w3.org/ns/activitystreams"},
		ID:           featuredURL,
		Type:         "OrderedCollection",
		TotalItems:   len(items),
		OrderedItems: items,
	}

	c.Header("Content-Type", "application/activity+json")
	c.JSON(200, response)
}

// buildNoteFromStatus builds an ActivityPub Note object from a Status.
func buildNoteFromStatus(status *models.Status, account *models.Account, baseURL string) map[string]any {
	statusURL := statusObjectURI(status, account, baseURL)
	actorURL := accountActorURI(account, baseURL)
	to, cc := statusRecipients(status, account, baseURL)

	note := map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           statusURL,
		"type":         "Note",
		"attributedTo": actorURL,
		"content":      status.ContentHTML(htmlsafe.OptionsForBaseURL(baseURL)),
		"url":          statusURL,
		"published":    status.CreatedAt,
		"to":           to,
		"cc":           cc,
		"sensitive":    status.Sensitive,
	}

	if status.UpdatedAt != nil && status.CreatedAt != nil && status.UpdatedAt.After(*status.CreatedAt) {
		note["updated"] = status.UpdatedAt
	}
	if status.Content.SpoilerText != "" {
		note["summary"] = status.SpoilerTextPlain()
	}

	// Add inReplyTo if applicable
	if status.InReplyToId != nil {
		var replyToStatus models.Status
		err := db.GetDB().Preload("Account").First(&replyToStatus, *status.InReplyToId).Error
		if err == nil && !replyToStatus.LocalOnly {
			if replyToStatus.Account != nil {
				replyURL := statusObjectURI(&replyToStatus, replyToStatus.Account, baseURL)
				note["inReplyTo"] = replyURL
			}
		}
	}

	// Add attachments if any
	if len(status.Attachments) > 0 {
		attachments := make([]map[string]any, 0, len(status.Attachments))

		for _, att := range status.Attachments {
			mediaType := att.ContentType
			if mediaType == "" {
				mediaType = "application/octet-stream"
			}

			url := att.RemoteUrl
			if url == "" {
				url = fmt.Sprintf("%s/media/%d/%s", baseURL, att.Id, att.FileName)
			}

			attachment := map[string]any{
				"type":      "Document",
				"mediaType": mediaType,
				"url":       url,
				"name":      att.Description,
			}
			if att.Blurhash != "" {
				attachment["blurhash"] = att.Blurhash
			}

			// Some peers retain unknown Documents without a working download
			// widget. Preserve a safe link to the exact opaque attachment URL
			// in the same audience-scoped Note; never derive a URL from name.
			kind, _, _ := mime.ParseMediaType(mediaType)
			if !strings.HasPrefix(kind, "image/") && !strings.HasPrefix(kind, "audio/") && !strings.HasPrefix(kind, "video/") {
				if u, err := urlpkg.Parse(url); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil {
					name := att.Description
					if strings.TrimSpace(name) == "" {
						name = "Download attachment"
					}
					note["content"] = note["content"].(string) + "<p><a href=\"" + html.EscapeString(url) + "\" rel=\"nofollow noopener noreferrer\">" + html.EscapeString(name) + "</a></p>"
				}
			}
			attachments = append(attachments, attachment)
		}

		note["attachment"] = attachments
	}

	// Add tags (mentions, hashtags, custom emoji)
	tags := make([]map[string]any, 0)

	// Add mentions
	if len(status.ToAccounts) > 0 {
		for _, mentioned := range status.ToAccounts {
			tag := map[string]any{
				"type": "Mention",
				"href": accountActorURI(mentioned, baseURL),
				"name": mentionName(mentioned),
			}
			tags = append(tags, tag)
		}
	}

	// Add hashtags
	if len(status.Tags) > 0 {
		for _, tag := range status.Tags {
			tagObj := map[string]any{
				"type": "Hashtag",
				"href": baseURL + "/tags/" + tag.Name,
				"name": "#" + tag.Name,
			}
			tags = append(tags, tagObj)
		}
	}

	for _, emoji := range emojiTags(status.Emojis, note["content"].(string)+" "+status.SpoilerTextPlain(), baseURL) {
		tags = append(tags, map[string]any{"id": emoji.ID, "type": emoji.Type, "name": emoji.Name, "updated": emoji.Updated, "icon": emoji.Icon, "_misskey_license": emoji.MisskeyLicense})
	}
	if len(tags) > 0 {
		note["@context"] = emojiContext()
		note["tag"] = tags
	}

	if poll := statusPoll(status); poll != nil && len(poll.Options) > 0 {
		applyQuestionFields(note, poll, time.Now())
	}

	return note
}
