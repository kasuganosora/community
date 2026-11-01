package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/langdetect"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/htmlsafe"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MaxRequestBodySize is the maximum allowed size for request body (1MB).
const MaxRequestBodySize = 1024 * 1024

// activityContextKey is a custom type for context keys in activity processing.
type activityContextKey string

const actorKey activityContextKey = "actor"

// Inbox handles POST /users/:username/inbox
// Receives activities from remote instances.
func Inbox(c *gin.Context) {
	username := c.Param("username")

	// Verify Content-Type header (ActivityPub requirement)
	contentType := c.GetHeader("Content-Type")
	if !isValidActivityPubContentType(contentType) {
		c.JSON(400, gin.H{"error": "invalid content-type, expected application/activity+json or application/ld+json"})

		return
	}

	// Verify the local user exists (for user-specific inbox)
	var targetAccount *models.Account
	if username != "" || c.Request.URL.Path == "/actor/inbox" {
		targetAccount = &models.Account{}
		err := localActorRouteQuery(db.GetDB(), c).First(targetAccount).Error
		if err != nil {
			c.JSON(404, gin.H{"error": "user not found"})

			return
		}
	}

	// Read the request body with size limit
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxRequestBodySize)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if err.Error() == "http: request body too large" {
			c.JSON(413, gin.H{"error": "request body too large"})
		} else {
			c.JSON(400, gin.H{"error": "failed to read body"})
		}

		return
	}

	// Parse the activity
	var activity map[string]any
	if err := json.Unmarshal(body, &activity); err != nil {
		c.JSON(400, gin.H{"error": "invalid activity"})

		return
	}

	activityActor, _ := activity["actor"].(string)
	activityType, _ := activity["type"].(string)
	activityID, _ := activity["id"].(string)
	// Log every refusal with enough context to tell the reasons apart; the
	// sender only sees the status code.
	signer := ""
	defer func() {
		if status := c.Writer.Status(); status == 401 {
			// The cause was already logged at the right level below.
			log.Debug("Inbox %s refused %s %s (actor %s, signer %s): HTTP %d", c.Request.URL.Path, activityType, activityID, activityActor, signer, status)
		} else if status >= 400 {
			log.Warn("Inbox %s refused %s %s (actor %s, signer %s): HTTP %d", c.Request.URL.Path, activityType, activityID, activityActor, signer, status)
		}
	}()

	// Verify HTTP signature
	actor, err := VerifyActivitySignature(c, body)
	if err != nil {
		if isSelfDeleteActivity(activity) {
			// A deleted remote account's key usually 404/410s, so its final
			// Delete can never verify. Like Mastodon, acknowledge it without
			// changing anything: a 401 only makes the sender retry for days.
			log.Debug("Ignoring unverifiable self-Delete of %s: %v", activityActor, err)
			c.JSON(202, gin.H{"status": "ignored"})
			return
		}
		if isRoutineSignatureFailure(err) {
			log.Debug("HTTP signature verification failed: %v", err)
		} else {
			log.Warn("HTTP signature verification failed: %v", err)
		}
		c.JSON(401, gin.H{"error": "signature verification failed"})

		return
	}
	signer = actor

	// A subscribed relay signs posts it forwards (actor = original author) and
	// its own Announces; both are re-fetched from their origin below.
	var relay *models.Relay
	if activityActor != actor || activityType == "Announce" {
		relay, err = acceptedRelayForSigner(c.Request.Context(), db.GetDB(), actor)
		if err != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}
	}
	// Relays also forward activities we cannot verify or use from them
	// (Undo{Announce}, Like, Follow... signed by the relay, not the actor).
	// Acknowledge and drop them: a 401 only makes the relay retry.
	if relay != nil && !isRelayForwardType(activityType) {
		log.Info("Relay %s: ignoring forwarded %s %s by %s", relay.InboxURL, activityType, activityID, activityActor)
		c.JSON(202, gin.H{"status": "ignored"})
		return
	}
	// Otherwise an authenticated HTTP key must identify the activity's declared actor.
	if relay == nil && activityActor != actor {
		c.JSON(401, gin.H{"error": "actor mismatch between signature and activity"})
		return
	}
	if activityID == "" || (relay == nil && !sameURIOrigin(activityID, actor)) || (relay != nil && validateFederationURI(activityID) != nil) {
		c.JSON(400, gin.H{"error": "invalid activity identity"})
		return
	}
	if host := actorHostname(actor); host != "" {
		suspend, _, severityErr := models.DomainSeverities(db.GetDB())
		if severityErr != nil {
			c.JSON(500, gin.H{"error": "internal error"})
			return
		}
		if suspend.Contains(host) {
			c.JSON(403, gin.H{"error": "domain suspended"})
			return
		}
	}
	// Verified: hand the activity to the inbox queue and answer at once.
	// The workers run the same processing below, off the request path.
	if enqueueInboxActivity(c, body, activityID, actor, targetAccount, relay) {
		return
	}
	processVerifiedActivity(c.Request.Context(), c, activity, actor, relay, targetAccount)
}

// processVerifiedActivity applies an activity whose HTTP signature, actor
// and domain checks already passed. It answers through c (directly from the
// inbox, or a recorder in an inbox queue worker).
func processVerifiedActivity(parent context.Context, c *gin.Context, activity map[string]any, actor string, relay *models.Relay, targetAccount *models.Account) {
	activityType, _ := activity["type"].(string)
	activityID, _ := activity["id"].(string)
	// Create context with verified actor info
	ctx := context.WithValue(parent, actorKey, actor)
	if relay != nil {
		handleRelayedActivity(ctx, c, activity, activityID, relay)
		return
	}

	if activityType == "Follow" && targetAccount != nil {
		// The Accept needs the follower's inbox. Resolve it now, outside the
		// transaction, rather than with the target account row locked and
		// the new follow's feed events uncommitted.
		ctx = withFollowerInbox(ctx, actor)
	}
	if activityType == "Announce" {
		// Fetch an unknown boosted object before the transaction opens.
		ctx = withPrefetchedAnnounceObject(ctx, activity["object"])
	}
	processInboxTransaction(ctx, c, activityID, func(ctx context.Context) {
		switch activityType {
		case "Create":
			handleCreateActivity(ctx, c, activity, targetAccount)
		case "Update":
			handleUpdateActivity(ctx, c, activity, targetAccount)
		case "Delete":
			handleDeleteActivity(ctx, c, activity, targetAccount)
		case "Follow":
			handleFollowActivity(ctx, c, activity, targetAccount)
		case "Accept":
			handleAcceptActivity(ctx, c, activity, targetAccount)
		case "Reject":
			handleRejectActivity(ctx, c, activity, targetAccount)
		case "Announce":
			handleAnnounceActivity(ctx, c, activity, targetAccount)
		case "Like":
			handleLikeActivity(ctx, c, activity, targetAccount)
		case "EmojiReact":
			// Pleroma/Akkoma emoji reactions.
			handleEmojiReaction(ctx, c, activity, false)
		case "Undo":
			handleUndoActivity(ctx, c, activity, targetAccount)
		case "Block":
			handleBlockActivity(ctx, c, activity, targetAccount)
		case "Flag":
			handleFlagActivity(ctx, c, activity, targetAccount)
		case "Move":
			handleMoveActivity(ctx, c, activity, targetAccount)
		case "Add":
			handleAddActivity(ctx, c, activity, targetAccount)
		case "Remove":
			handleRemoveActivity(ctx, c, activity, targetAccount)
		default:
			// Accept unknown activity types
			log.Info("Received unknown activity type: %s from %s", activityType, actor)
			c.JSON(200, gin.H{"status": "accepted"})
		}
	})
}

func actorHostname(actor string) string {
	parsed, err := url.Parse(actor)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// isValidActivityPubContentType checks if the Content-Type is valid for ActivityPub.
func isValidActivityPubContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	// Parse media type (ignore parameters like charset)
	mediaType := strings.ToLower(strings.Split(contentType, ";")[0])
	mediaType = strings.TrimSpace(mediaType)

	validTypes := []string{
		"application/activity+json",
		"application/ld+json",
		"application/json", // Accept plain JSON for compatibility
	}

	if slices.Contains(validTypes, mediaType) {
		return true
	}

	// Also accept ld+json with profile parameter
	if strings.HasPrefix(mediaType, "application/ld+json") {
		return true
	}

	return false
}

// handleCreateActivity handles Create activities (new remote posts).
func handleCreateActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) { //nolint:unparam // param reserved for future use
	actor, _ := ctx.Value(actorKey).(string)

	object, ok := activity["object"].(map[string]any)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})

		return
	}

	objectType, _ := object["type"].(string)
	if objectType != "Note" && objectType != "Question" {
		// We only handle Note and Question (poll) objects for now
		c.JSON(200, gin.H{"status": "accepted"})

		return
	}

	// Extract Note properties
	objectID, _ := object["id"].(string)
	content, _ := object["content"].(string)
	// attributedTo may be a string, an embedded actor or a one-element array.
	attributedTo := attributedToURI(object)
	publishedStr, _ := object["published"].(string)
	summary, _ := object["summary"].(string)
	inReplyTo := noteInReplyTo(object["inReplyTo"])
	sensitive, _ := object["sensitive"].(bool)

	// "content" is HTML; "source.content" is the author's raw source (plain
	// text, Markdown, MFM...). Remote statuses are stored as HTML, so only
	// fall back to the source (rendered as escaped text) when there is no
	// HTML content.
	if content == "" {
		if source, ok := object["source"].(map[string]any); ok {
			if sourceContent, ok := source["content"].(string); ok && sourceContent != "" {
				content = htmlsafe.RenderPlainText(sourceContent, htmlsafe.RenderOptions{})
			}
		}
	}

	content = htmlsafe.Sanitize(content)
	summary = htmlsafe.StripTags(summary)

	// Verify actor matches attributedTo
	if attributedTo != actor {
		c.JSON(403, gin.H{"error": "actor mismatch"})

		return
	}

	if objectID == "" || !sameURIOrigin(objectID, actor) {
		c.JSON(403, gin.H{"error": "invalid object identity"})
		return
	}

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccountContext(ctx, actor) //nolint:contextcheck // will be refactored
	if err != nil {
		respondRemoteAccountError(c, actor, err)
		return
	}

	// A vote on one of our polls is a named Note replying to the Question;
	// it is counted, never stored as a reply.
	if objectType == "Note" && isPollVoteNote(object) {
		voteErr := recordRemoteVote(ctx, remoteAccount, object)
		if voteErr == nil {
			c.JSON(200, gin.H{"status": "accepted"})
			return
		}
		if !errors.Is(voteErr, errNotLocalPoll) {
			log.Error("Failed to record poll vote %s: %v", objectID, voteErr)
			c.JSON(500, gin.H{"error": "failed to record vote"})
			return
		}
	}

	// Check if status already exists
	var existingStatus models.Status

	err = activityDB(ctx).Where("origin_uri = ?", objectID).First(&existingStatus).Error
	if err == nil {
		// A reply may have been stored before its parent was known. Link it
		// now so mention notifications can show what was replied to.
		if linkErr := backfillReplyParent(ctx, &existingStatus, inReplyTo); linkErr != nil {
			c.JSON(500, gin.H{"error": "failed to resolve reply"})
			return
		}
		c.JSON(200, gin.H{"status": "accepted"})

		return
	}

	// Parse published time
	publishedAt := utils.ParseRFC3339(publishedStr)

	// Find in_reply_to status if specified
	var (
		inReplyToID        *uint64
		inReplyToAccountID *uint64
	)

	if inReplyTo != "" {
		parent, err := findInboundParent(ctx, inReplyTo)
		if err == nil {
			visible, checkErr := parent.CanViewEx(activityDB(ctx), remoteAccount)
			if checkErr != nil {
				c.JSON(500, gin.H{"error": "failed reply authorization"})
				return
			}
			if !visible {
				c.JSON(403, gin.H{"error": "reply target is not visible"})
				return
			}
			if !models.ValidateReplyVisibility(parent, getVisibilityFromActivity(object)) {
				c.JSON(422, gin.H{"error": "reply visibility cannot be broader than its parent"})
				return
			}
			inReplyToID = &parent.Id
			inReplyToAccountID = &parent.AccountId
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(500, gin.H{"error": "failed to resolve reply"})
			return
		}
	}
	visibility := getVisibilityFromActivity(object)
	recipients, err := recipientAccounts(ctx, object, visibility == models.StatusVisibilityPublic || visibility == models.StatusVisibilityUnlisted)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to resolve recipients"})
		return
	}

	// A signed address is not a bypass for either side's block. Keep other
	// group recipients, but acknowledge a fully blocked direct delivery without
	// storing it or asking the remote server to retry forever.
	visibleRecipients := recipients[:0]
	for _, recipient := range recipients {
		blocked, err := models.AccountsBlocked(activityDB(ctx), remoteAccount, recipient)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to check recipient"})
			return
		}
		if !blocked {
			visibleRecipients = append(visibleRecipients, recipient)
		}
	}
	if visibility == models.StatusVisibilityDirect && len(recipients) > 0 && len(visibleRecipients) == 0 {
		c.Status(202)
		return
	}
	recipients = visibleRecipients

	// Create status
	now := time.Now()
	status := models.Status{
		AccountId:          remoteAccount.Id,
		Content:            models.StatusContent{Content: content, SpoilerText: summary},
		Sensitive:          sensitive,
		Visibility:         visibility,
		ToAccounts:         recipients,
		OriginUri:          &objectID,
		InReplyToId:        inReplyToID,
		InReplyToAccountId: inReplyToAccountID,
		QuoteId:            knownQuoteID(ctx, object),
		Language:           langdetect.FromContentMap(object),
		CreatedAt:          publishedAt,
		UpdatedAt:          &now,
	}

	if status.CreatedAt == nil {
		status.CreatedAt = &now
	}
	status.Poll = questionPoll(object, remoteAccount.Id)
	if models.ShouldBackfill(*status.CreatedAt, now, false) {
		status.MarkBackfilled()
	}

	// Set domain for remote status
	domain := utils.ExtractDomainFromURI(actor)
	if domain != "" {
		status.Domain = &domain
	}

	// Content association is saved once by GORM. Its local mention parser is
	// disabled because authenticated AP addressing is authoritative.
	if !applyInboundNotePolicy(&status, object, domain) {
		log.Info("Dropped remote status %s from %s: prohibited words", objectID, actor)
		c.Status(202)
		return
	}

	status.Attachments, err = incomingAttachments(ctx, object["attachment"], remoteAccount.Id)
	if err != nil {
		c.JSON(503, gin.H{"error": "attachment address validation temporarily failed"})
		return
	}
	applyMediaSilence(status.Attachments, domain)
	status.Sensitive = deriveSensitive(sensitive, summary, status.Attachments)
	err = persistIncomingStatusWithEmoji(ctx, activityDB(ctx), &status, object["tag"])

	if err != nil {
		log.Error("Failed to create status: %v", err)
		c.JSON(500, gin.H{"error": "failed to create status"})

		return
	}

	log.Info("Created remote status %d from %s with %d mentions", status.Id, actor, len(recipients))
	scheduleQuoteFetch(nil, &status, object)
	c.JSON(200, gin.H{"status": "accepted"})
}

// parseTagsToAccountIDs extracts mentioned account IDs from ActivityPub tags.
func parseTagsToAccountIDs(tags []any) []uint64 {
	var accountIDs []uint64

	for _, tag := range tags {
		tagMap, ok := tag.(map[string]any)
		if !ok {
			continue
		}

		tagType, _ := tagMap["type"].(string)
		if tagType != "Mention" {
			continue
		}

		href, _ := tagMap["href"].(string)
		if href == "" {
			continue
		}

		// Try to find the account
		var account models.Account
		err := db.GetDB().Where("origin_id = ?", href).First(&account).Error
		if err == nil {
			accountIDs = append(accountIDs, account.Id)
		}
	}

	return accountIDs
}

// handleUpdateActivity handles Update activities (edit remote posts).
func handleUpdateActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) { //nolint:unparam // param reserved for future use
	actor, _ := ctx.Value(actorKey).(string)

	object, ok := activity["object"].(map[string]any)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})

		return
	}

	objectID, _ := object["id"].(string)
	objectType, _ := object["type"].(string)

	// Handle Actor updates
	if objectType == "Person" || objectType == "Service" || objectType == "Group" {
		// Verify the object ID in the update matches the signing actor to
		// prevent an attacker from updating a different actor's profile.
		if objectID != actor {
			log.Warn("Actor update denied: object ID %q does not match signing actor %q", objectID, actor)
			c.JSON(403, gin.H{"error": "actor mismatch in update"})

			return
		}

		// Update remote account info
		remoteAccount, err := getOrCreateRemoteAccountContext(ctx, actor) //nolint:contextcheck
		if err != nil {
			respondRemoteAccountError(c, actor, err)
			return
		}

		nameValue, hasName := object["name"]
		name, validName := nameValue.(string)
		summaryValue, hasSummary := object["summary"]
		summary, validSummary := summaryValue.(string)
		preferredUsername, _ := object["preferredUsername"].(string)

		updates := map[string]any{}
		var profile Actor
		encodedProfile, marshalErr := json.Marshal(object)
		if marshalErr == nil {
			marshalErr = json.Unmarshal(encodedProfile, &profile)
		}
		if marshalErr != nil {
			c.JSON(400, gin.H{"error": "invalid actor profile"})
			return
		}
		if err := actorImageUpdates(object, updates); err != nil {
			c.JSON(400, gin.H{"error": "invalid actor image"})
			return
		}
		if profile.IsCat != nil {
			updates["is_cat"] = *profile.IsCat
		}
		if _, ok := object["vcard:Address"]; ok {
			updates["location"] = remoteLocation(&profile)
			ectx, ecancel := context.WithTimeout(ctx, 2*locationEmojiTimeout)
			addLocationEmojiTags(ectx, &profile)
			ecancel()
			if profile.Tag != nil {
				object["tag"] = profile.Tag
			}
		}
		if _, ok := object["vcard:bday"]; ok {
			updates["birthday"] = remoteBirthday(&profile)
		}
		if hasName && (validName || nameValue == nil) {
			updates["display_name"] = name
		}

		if hasSummary && (validSummary || summaryValue == nil) {
			updates["note"] = htmlsafe.Sanitize(summary)
		}

		if preferredUsername != "" {
			updates["username"] = preferredUsername
		}

		{
			err := activityDB(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(remoteAccount, remoteAccount.Id).Error; err != nil {
					return err
				}
				for key, value := range actorDiscoveryUpdates(remoteAccount, &profile, true, time.Now()) {
					updates[key] = value
				}
				if err := tx.Model(remoteAccount).Updates(updates).Error; err != nil {
					return err
				}
				if err := tx.First(remoteAccount, remoteAccount.Id).Error; err != nil {
					return err
				}
				if tags, present := object["tag"]; present {
					return importAccountEmojiTags(tx, remoteAccount, tags)
				}
				return nil
			})
			if err != nil {
				log.Error("Failed to update remote account: %v", err)
				c.JSON(500, gin.H{"error": "failed account update"})
				return
			}
		}

		queueProfileInvalidation(ctx, remoteAccount)
		scheduleRemoteAvatarDecorations(remoteAccount.Id, true)
		c.JSON(200, gin.H{"status": "accepted"})

		return
	}

	// Handle Note (and Question/poll) updates
	if objectType == "Note" || objectType == "Question" {
		content, _ := object["content"].(string)
		summary, _ := object["summary"].(string)
		content = htmlsafe.Sanitize(content)
		summary = htmlsafe.StripTags(summary)

		// Find the status by origin_uri, then verify ownership by checking
		// that the status author's origin_id matches the signing actor.
		var status models.Status
		err := activityDB(ctx).Preload("Content").Preload("ToAccounts").Where("origin_uri = ?", objectID).First(&status).Error
		if err != nil {
			c.JSON(404, gin.H{"error": "status not found"})

			return
		}

		// Verify the actor owns this status
		var statusAuthor models.Account
		if err := activityDB(ctx).First(&statusAuthor, status.AccountId).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to verify ownership"})

			return
		}

		if statusAuthor.OriginId != actor {
			log.Warn("Update denied: status %d author %q does not match actor %q", status.Id, statusAuthor.OriginId, actor)
			c.JSON(403, gin.H{"error": "not authorized to update this status"})

			return
		}

		// Edits go through the same moderation policy as new notes: an edit
		// introducing prohibited words is dropped (the stored version stays),
		// sensitive words or a silenced host downgrade public to unlisted.
		authorHost := ""
		if statusAuthor.Domain != nil {
			authorHost = *statusAuthor.Domain
		}
		probe := models.Status{Visibility: status.Visibility, Content: models.StatusContent{Content: content, SpoilerText: summary}}
		if !applyInboundNotePolicy(&probe, object, authorHost) {
			log.Info("Dropped update of remote status %d from %s: prohibited words", status.Id, actor)
			c.Status(202)
			return
		}

		now := time.Now()
		attachments, err := incomingAttachments(ctx, object["attachment"], status.AccountId)
		if err != nil {
			c.JSON(503, gin.H{"error": "attachment address validation temporarily failed"})
			return
		}
		applyMediaSilence(attachments, authorHost)
		err = activityDB(ctx).Transaction(func(tx *gorm.DB) error {
			dialect.AsyncCommit(tx) // remote Update
			var preserveErr error
			tx, preserveErr = models.PreserveFeedStatusAudience(tx, status.Id)
			if preserveErr != nil {
				return preserveErr
			}
			if _, present := object["attachment"]; present {
				if err := tx.Where("status_id = ?", status.Id).Delete(&models.MediaAttachment{}).Error; err != nil {
					return err
				}
				for _, attachment := range attachments {
					attachment.StatusId = &status.Id
					if err := tx.Create(attachment).Error; err != nil {
						return err
					}
				}
			}
			domain := ""
			if statusAuthor.Domain != nil {
				domain = *statusAuthor.Domain
			}
			emojis, err := importEmojiTags(tx, domain, content+" "+summary, object["tag"])
			if err != nil {
				return err
			}
			// Keep the previous version so the post shows as edited and its
			// history is available. Updates that only refresh counters (poll
			// tallies, Misskey reactions) leave the text alone and are not edits.
			if status.Content.Content != content || status.Content.SpoilerText != summary {
				if err := recordStatusEdit(tx, &status); err != nil {
					return err
				}
				now = editTime(object, now)
			}
			// Save the complete content value so association hooks see the new text.
			status.Content.Content = content
			status.Content.SpoilerText = summary
			writeCtx := withObjectHashtags(models.WithStructuredEmojiIDs(models.WithStructuredMentions(ctx), emojis.GetIds()), object["tag"])
			if err := tx.WithContext(writeCtx).Save(&status.Content).Error; err != nil {
				return err
			}
			// Not Model(&status): its loaded Content association would be
			// upserted again and re-run the tag/emoji hooks.
			updates := map[string]any{"updated_at": now}
			if probe.Visibility != status.Visibility {
				updates["visibility"] = probe.Visibility
			}
			_, hasFlag := object["sensitive"]
			_, hasFiles := object["attachment"]
			_, hasSummary := object["summary"]
			if hasFlag || hasFiles || hasSummary {
				noteSensitive, _ := object["sensitive"].(bool)
				updates["sensitive"] = deriveSensitive(noteSensitive, summary, attachments)
			}
			if objectType == "Question" {
				if err := updateRemotePoll(tx, status.Id, status.AccountId, object); err != nil {
					return err
				}
			}
			return tx.Model(&models.Status{}).Where("id = ?", status.Id).Updates(updates).Error
		})
		if err != nil {
			c.JSON(500, gin.H{"error": "failed content update"})
			return
		}

		log.Info("Updated remote status %d from %s", status.Id, actor)
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleDeleteActivity handles Delete activities (delete remote posts/accounts).
func handleDeleteActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) { //nolint:unparam // param reserved for future use
	actor, _ := ctx.Value(actorKey).(string)

	object, ok := activity["object"].(string)
	if !ok {
		// Object might be a map
		objMap, isMap := activity["object"].(map[string]any)
		if isMap {
			object, _ = objMap["id"].(string)
		}
	}

	if object == "" {
		c.JSON(400, gin.H{"error": "invalid object"})

		return
	}

	// Check if deleting an actor
	if object == actor {
		// Delete/suspend the remote account
		var account models.Account
		err := activityDB(ctx).Where("origin_id = ? OR (username = ? AND domain = ?)",
			actor, utils.ExtractUsernameFromURI(actor), utils.ExtractDomainFromURI(actor)).First(&account).Error
		if err == nil {
			// Soft delete the account using GORM's built-in soft delete
			if err := activityDB(ctx).Delete(&account).Error; err != nil {
				log.Error("Failed to soft-delete remote account %s: %v", actor, err)
			} else {
				log.Info("Deleted remote account %s", actor)
			}
		}

		c.JSON(200, gin.H{"status": "accepted"})

		return
	}

	// Delete the status
	var status models.Status
	err := activityDB(ctx).Where("origin_uri = ?", object).First(&status).Error
	if err == nil {
		// Verify ownership — check the status author's origin_id matches actor
		var account models.Account
		err := activityDB(ctx).First(&account, status.AccountId).Error
		if err == nil {
			// Ownership is verified via the account's OriginId, not by constructing
			// a URL from username/domain (which could differ from the actual AP URI).
			if account.OriginId == actor {
				err := activityDB(ctx).Delete(&status).Error
				if err != nil {
					log.Error("Failed to delete status %d: %v", status.Id, err)
					c.JSON(500, gin.H{"error": "failed status deletion"})
					return
				} else {
					log.Info("Deleted remote status %d from %s", status.Id, actor)
				}
			} else {
				log.Warn("Delete denied: status %d author origin_id %q does not match actor %q", status.Id, account.OriginId, actor)
				c.JSON(403, gin.H{"error": "not authorized to delete this status"})
				return
			}
		}
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleFollowActivity handles Follow activities (follow requests).
func handleFollowActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) {
	if targetAccount == nil {
		if uri, ok := activity["object"].(string); ok {
			var local models.Account
			if localActorQuery(activityDB(ctx), uri).First(&local).Error == nil {
				targetAccount = &local
			}
		}
	}
	if targetAccount == nil {
		c.JSON(400, gin.H{"error": "no target account"})

		return
	}

	actor, _ := ctx.Value(actorKey).(string)

	object, ok := activity["object"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})

		return
	}

	// Verify the object is our local user

	expectedActorURI := targetAccount.ActorURI(systemsettings.GetBaseURL())
	if object != expectedActorURI {
		c.JSON(403, gin.H{"error": "wrong target"})

		return
	}

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccountContext(ctx, actor) //nolint:contextcheck
	if err != nil {
		respondRemoteAccountError(c, actor, err)
		return
	}

	// Lock the followed local account so concurrent activities from the same
	// remote identity cannot create duplicate relationships/counter increments.
	var locked models.Account
	if err := activityDB(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, targetAccount.Id).Error; err != nil {
		c.JSON(500, gin.H{"error": "failed to lock follow target"})
		return
	}
	// Check if already following
	var existingFollow models.Follow

	err = activityDB(ctx).Where("account_id = ? AND follow_id = ?", remoteAccount.Id, targetAccount.Id).First(&existingFollow).Error
	if err == nil {
		// A new Follow activity ID can retry the acknowledgement of an existing relationship.
		if existingFollow.Status == models.FollowStatusFollowed {
			if id, ok := activity["id"].(string); ok && id != "" {
				if err := queueAcceptActivity(ctx, targetAccount, remoteAccount, id); err != nil {
					c.JSON(500, gin.H{"error": "failed to queue acceptance"})
					return
				}
			}
		}
		c.JSON(200, gin.H{"status": "accepted"})

		return
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(500, gin.H{"error": "failed to query follow"})
		return
	}
	// If target account is locked, create follow request. Like Misskey,
	// follows from a silenced instance always need approval.
	// autoAcceptFollowed approves accounts the target already follows, even
	// from a silenced instance (Misskey behaviour).
	if (targetAccount.Locked || (remoteAccount.Domain != nil && moderation.Current().IsSilencedHost(*remoteAccount.Domain))) && !targetAccount.AutoAcceptsFollowFrom(activityDB(ctx), remoteAccount) {
		follow := models.Follow{
			AccountId:  remoteAccount.Id,
			FollowId:   targetAccount.Id,
			Status:     models.FollowStatusRequested,
			Properties: map[string]any{"activitypub_follow_id": activity["id"]},
		}
		err := activityDB(ctx).Create(&follow).Error
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to create follow request"})

			return
		}

		// Send notification to target user about follow request
		createFollowRequestNotificationContext(ctx, remoteAccount.Id, targetAccount.Id)
		log.Info("Created follow request from %s to %s", actor, targetAccount.Username)
	} else {
		// Auto-accept follow
		follow := models.Follow{
			AccountId: remoteAccount.Id,
			FollowId:  targetAccount.Id,
			Status:    models.FollowStatusFollowed,
		}
		err := activityDB(ctx).Create(&follow).Error
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to create follow"})

			return
		}

		// Send Accept activity back
		activityID, ok := activity["id"].(string)
		if ok && activityID != "" {
			if err := queueAcceptActivity(ctx, targetAccount, remoteAccount, activityID); err != nil {
				c.JSON(500, gin.H{"error": "failed to queue follow acceptance"})
				return
			}
		}

		log.Info("Auto-accepted follow from %s to %s", actor, targetAccount.Username)
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleAcceptActivity handles Accept activities (follow accept / relay accept).
func handleAcceptActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) {
	handleFollowResponse(ctx, c, activity, targetAccount, true)
}
func handleRejectActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) {
	handleFollowResponse(ctx, c, activity, targetAccount, false)
}

// handleAnnounceActivity handles Announce activities (boosts).
func handleAnnounceActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) { //nolint:unparam // param reserved for future use
	actor, _ := ctx.Value(actorKey).(string)

	object, embedded := announceObject(activity["object"])
	if object == "" {
		c.JSON(400, gin.H{"error": "invalid object"})

		return
	}

	activityID, _ := activity["id"].(string)
	publishedStr, _ := activity["published"].(string)

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccountContext(ctx, actor) //nolint:contextcheck
	if err != nil {
		respondRemoteAccountError(c, actor, err)
		return
	}

	originalStatus, fetchErr := resolveAnnouncedStatus(ctx, actor, object, embedded) //nolint:contextcheck
	if fetchErr != nil {
		log.Warn("Could not find or fetch original status for announce %s by %s of %s (class=%s, queued for retry unless gone): %v", activityID, actor, object, announceFailureClass(fetchErr), fetchErr)
		// Accept the activity (the sender must not retry forever) but keep it
		// for our own capped retries: the origin may be briefly down or only
		// serve the object later.
		enqueueAnnounceRetry(ctx, activityID, actor, object, publishedStr, fetchErr)
		c.JSON(200, gin.H{"status": "accepted"})

		return
	}

	if originalStatus.LocalOnly {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}
	if _, err := createRemoteReblog(ctx, remoteAccount, originalStatus, activityID, publishedStr); err != nil {
		log.Error("Failed to create reblog: %v", err)
		c.JSON(500, gin.H{"error": "failed to create reblog"})

		return
	}
	c.JSON(200, gin.H{"status": "accepted"})
}

// announceObject returns the boosted object's id and, when the Announce
// embeds the full object (Misskey, some relays), that object.
func announceObject(object any) (string, map[string]any) {
	switch v := object.(type) {
	case string:
		return v, nil
	case map[string]any:
		id, _ := v["id"].(string)
		return id, v
	}
	return "", nil
}

// resolveAnnouncedStatus finds the boosted status locally or fetches it from
// its origin (signed by the instance actor when the origin requires it). An
// embedded copy is used only when the fetch fails and the booster is hosted
// on the object's own origin: the HTTP signature already authenticated that
// server, so it is the authority for the object.
func resolveAnnouncedStatus(ctx context.Context, actor, object string, embedded map[string]any) (*models.Status, error) {
	var originalStatus models.Status
	if err := activityDB(ctx).Scopes(statusByObjectRef(object)).First(&originalStatus).Error; err == nil {
		return &originalStatus, nil
	}
	fetched, fetchErr := fetchAndCreateRemoteStatus(ctx, object)
	if fetchErr == nil {
		return fetched, nil
	}
	if embedded != nil && actor != "" && sameURIOrigin(object, actor) {
		if status, err := importFetchedStatus(ctx, object, embedded); err == nil {
			return status, nil
		}
	}
	return nil, fetchErr
}

// createRemoteReblog stores remoteAccount's boost of originalStatus once per
// Announce id and bumps the reblog counter. It returns the existing row when
// the Announce was already applied.
func createRemoteReblog(ctx context.Context, remoteAccount *models.Account, originalStatus *models.Status, activityID, publishedStr string) (*models.Status, error) {
	var existingReblog models.Status
	if err := activityDB(ctx).Where("account_id = ? AND reblog_id = ? AND origin_uri = ?",
		remoteAccount.Id, originalStatus.Id, activityID).First(&existingReblog).Error; err == nil {
		return &existingReblog, nil
	}

	publishedAt := utils.ParseRFC3339(publishedStr)
	now := time.Now()
	reblog := models.Status{
		AccountId:  remoteAccount.Id,
		ReblogId:   &originalStatus.Id,
		Visibility: models.StatusVisibilityPublic,
		// Timelines classify local/remote by statuses.domain.
		Domain:    remoteAccount.Domain,
		OriginUri: &activityID,
		CreatedAt: publishedAt,
		UpdatedAt: &now,
	}
	if reblog.CreatedAt == nil {
		reblog.CreatedAt = &now
	}
	if err := activityDB(ctx).Create(&reblog).Error; err != nil {
		return nil, err
	}
	if err := activityDB(ctx).Model(originalStatus).UpdateColumn("reblogs_count", gorm.Expr("reblogs_count + ?", 1)).Error; err != nil {
		log.Error("Failed to increment reblogs_count for status %d: %v", originalStatus.Id, err)
	}
	log.Info("Created remote reblog %d from account %d", reblog.Id, remoteAccount.Id)
	return &reblog, nil
}

// handleLikeActivity handles Like activities (favourites).
func handleLikeActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) { //nolint:unparam // param reserved for future use
	if isReactionLike(activity) {
		handleEmojiReaction(ctx, c, activity, false)
		return
	}
	actor, _ := ctx.Value(actorKey).(string)

	object, ok := activity["object"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})

		return
	}

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccountContext(ctx, actor) //nolint:contextcheck
	if err != nil {
		respondRemoteAccountError(c, actor, err)
		return
	}

	statusPtr, err := reactionStatus(activityDB(ctx), object)
	if err != nil {
		c.JSON(202, gin.H{"status": "ignored"})
		return
	}
	status := *statusPtr
	allowed, err := status.CanViewEx(activityDB(ctx), remoteAccount)
	if err != nil {
		c.JSON(500, gin.H{"error": "permission lookup failed"})
		return
	}
	if !allowed {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}

	// Check if already liked
	var existingFav models.FavoriteStatus

	err = activityDB(ctx).Where("account_id = ? AND status_id = ?", remoteAccount.Id, status.Id).First(&existingFav).Error
	if err == nil {
		c.JSON(200, gin.H{"status": "accepted"})

		return
	}

	// Create favourite
	fav := models.FavoriteStatus{
		AccountId: remoteAccount.Id,
		StatusId:  status.Id,
	}

	if err := activityDB(ctx).Create(&fav).Error; err != nil {
		log.Error("Failed to create favourite: %v", err)
		c.JSON(500, gin.H{"error": "failed to create favourite"})

		return
	}

	// FavoriteStatus.AfterCreate owns the counter increment.

	log.Info("Created remote favourite from %s on status %d", actor, status.Id)
	c.JSON(200, gin.H{"status": "accepted"})
}

// handleUndoActivity handles Undo activities (undo follow/like/announce).
func handleUndoActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) {
	actor, _ := ctx.Value(actorKey).(string)

	objectMap, ok := activity["object"].(map[string]any)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})

		return
	}

	if embedded, ok := objectMap["actor"].(string); ok && embedded != actor {
		c.JSON(403, gin.H{"error": "actor mismatch"})
		return
	}
	objectType, _ := objectMap["type"].(string)
	if (objectType == "Like" && isReactionLike(objectMap)) || objectType == "EmojiReact" {
		handleEmojiReaction(ctx, c, activity, true)
		return
	}
	objectID, _ := objectMap["id"].(string)
	objectObject, _ := objectMap["object"].(string)

	// Get remote account
	remoteAccount, err := getOrCreateRemoteAccountContext(ctx, actor) //nolint:contextcheck
	if err != nil {
		respondRemoteAccountError(c, actor, err)
		return
	}

	switch objectType {
	case "Follow":
		// Undo a follow
		if targetAccount != nil {
			err := models.DeleteFollow(activityDB(ctx), remoteAccount.Id, targetAccount.Id)
			if err != nil {
				log.Error("Failed to undo follow from %s to %s: %v", actor, targetAccount.Username, err)
			} else {
				log.Info("Undid follow from %s to %s", actor, targetAccount.Username)
			}
		}

	case "Like":
		// Undo a like
		statusPtr, err := reactionStatus(activityDB(ctx), objectObject)
		var status models.Status
		if err == nil {
			status = *statusPtr
		}
		if err == nil {
			var favorites []models.FavoriteStatus
			if err := activityDB(ctx).Where("account_id = ? AND status_id = ?", remoteAccount.Id, status.Id).Find(&favorites).Error; err != nil {
				c.JSON(500, gin.H{"error": "failed to read favourite"})
				return
			}
			for i := range favorites {
				if err := models.QueueMatrixDirectFavourite(activityDB(ctx), "FavoriteUndo", &favorites[i]); err != nil {
					c.JSON(500, gin.H{"error": "failed to queue favourite withdrawal"})
					return
				}
			}
			result := activityDB(ctx).Session(&gorm.Session{SkipHooks: true}).Where("account_id = ? AND status_id = ?", remoteAccount.Id, status.Id).Delete(&models.FavoriteStatus{})
			if result.Error != nil {
				c.JSON(500, gin.H{"error": "failed to undo favourite"})
				return
			}
			if result.RowsAffected > 0 {
				if err := activityDB(ctx).Model(&models.Status{}).Where("id = ?", status.Id).UpdateColumn("favourites_count", gorm.Expr("CASE WHEN favourites_count > 0 THEN favourites_count - 1 ELSE 0 END")).Error; err != nil {
					c.JSON(500, gin.H{"error": "failed to update favourite count"})
					return
				}
			}
		}

	case "Announce":
		// Undo an announce (reblog); also drop a pending fetch retry.
		activityDB(ctx).Where("activity_id = ? AND actor = ?", objectID, actor).Delete(&AnnounceRetry{})
		var reblog models.Status
		err := activityDB(ctx).Where("origin_uri = ? AND account_id = ?", objectID, remoteAccount.Id).First(&reblog).Error
		if err == nil {
			// Decrement reblogs_count on the original status
			if reblog.ReblogId != nil {
				activityDB(ctx).Model(&models.Status{}).Where("id = ?", *reblog.ReblogId).
					UpdateColumn("reblogs_count", gorm.Expr("CASE WHEN reblogs_count > 0 THEN reblogs_count - 1 ELSE 0 END"))
			}

			activityDB(ctx).Delete(&reblog)
			log.Info("Undid announce from %s: %s", actor, objectID)
		}

	case "Block":
		// Undo a block - remove from blocks list
		if targetAccount != nil {
			err := activityDB(ctx).Where("account_id = ? AND target_account_id = ?", remoteAccount.Id, targetAccount.Id).Delete(&models.Block{}).Error
			if err != nil {
				log.Error("Failed to undo block from %s to %s: %v", actor, targetAccount.Username, err)
			} else {
				log.Info("Undid block from %s to %s", actor, targetAccount.Username)
			}
		}
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleBlockActivity handles Block activities.
func handleBlockActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) { //nolint:unparam // param reserved for future use
	actor, _ := ctx.Value(actorKey).(string)

	objectStr, ok := activity["object"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})

		return
	}

	// Get remote account
	remoteAccount, err := getOrCreateRemoteAccountContext(ctx, actor) //nolint:contextcheck
	if err != nil {
		respondRemoteAccountError(c, actor, err)
		return
	}

	// Find the target account

	var blockedAccount models.Account

	{
		err := localActorQuery(activityDB(ctx), objectStr).First(&blockedAccount).Error
		if err == nil {
			// Create block record
			block := models.Block{
				AccountId:       remoteAccount.Id,
				TargetAccountId: blockedAccount.Id,
			}
			err := activityDB(ctx).Create(&block).Error
			if err != nil {
				log.Error("Failed to create block from %s to %s: %v", actor, blockedAccount.Username, err)
			} else {
				log.Info("Remote user %s blocked local user %s", actor, blockedAccount.Username)
			}
		}
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleFlagActivity handles Flag activities (reports).
func handleFlagActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) { //nolint:unparam // param reserved for future use
	actor, _ := ctx.Value(actorKey).(string)

	// Get or create remote account (the reporter)
	remoteAccount, err := getOrCreateRemoteAccountContext(ctx, actor) //nolint:contextcheck
	if err != nil {
		respondRemoteAccountError(c, actor, err)
		return
	}

	// Parse the object(s) being reported
	var (
		reportedStatusIds []uint64
		reportedAccountId uint64
		objectIDs         []string
	)

	if objects, ok := activity["object"].([]any); ok {
		for _, obj := range objects {
			if objStr, ok := obj.(string); ok {
				objectIDs = append(objectIDs, objStr)
			}
		}
	} else if objStr, ok := activity["object"].(string); ok {
		objectIDs = append(objectIDs, objStr)
	} else {
		c.JSON(400, gin.H{"error": "invalid object"})

		return
	}

	// Process each object to find reported statuses and accounts
	for _, objectID := range objectIDs {
		// Check if it's a status
		var status models.Status
		err := activityDB(ctx).Where("origin_uri = ? OR origin_url = ? OR local_object_uri = ?", objectID, objectID, objectID).First(&status).Error
		if err == nil {
			reportedStatusIds = append(reportedStatusIds, status.Id)
			if reportedAccountId == 0 {
				reportedAccountId = status.AccountId
			}
		} else {
			// Check if it's an account
			var account models.Account
			err := activityDB(ctx).Where("origin_id = ?", objectID).First(&account).Error
			if err == nil {
				reportedAccountId = account.Id
			}
		}
	}

	content, _ := activity["content"].(string)
	forwarded, _ := activity["forwarded"].(bool)

	// Create the report
	report := models.Report{
		AccountId:       remoteAccount.Id,
		TargetAccountId: reportedAccountId,
		StatusIds:       reportedStatusIds,
		Comment:         content,
		Forwarded:       forwarded,
		Category:        models.ReportCategoryOther,
		Uri:             actor,
	}

	if err := activityDB(ctx).Create(&report).Error; err != nil {
		log.Error("Failed to create report from flag: %v", err)
		c.JSON(500, gin.H{"error": "failed to create report"})

		return
	}

	// Create admin notification for the report
	createAdminReportNotification(ctx, report.Id, remoteAccount.Id, reportedAccountId)

	log.Info("Created report %d from flag by %s", report.Id, actor)
	c.JSON(200, gin.H{"status": "accepted"})
}

// handleMoveActivity handles Move activities (account migration).
func handleMoveActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) { //nolint:unparam // param reserved for future use
	actor, _ := ctx.Value(actorKey).(string)

	oldActorURI, ok := activity["object"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})

		return
	}

	newActorURI, ok := activity["target"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid target"})

		return
	}

	log.Info("Received move from %s to %s", oldActorURI, newActorURI)

	// Verify the actor matches the old account
	if actor != oldActorURI {
		c.JSON(403, gin.H{"error": "actor must match the old account"})

		return
	}

	// Find the old account
	var oldAccount models.Account
	if err := activityDB(ctx).Where("origin_id = ?", oldActorURI).First(&oldAccount).Error; err != nil {
		c.JSON(404, gin.H{"error": "old account not found"})

		return
	}

	newAccount, err := ResolveMigrationAccount(ctx, activityDB(ctx), newActorURI)
	if err != nil {
		respondRemoteAccountError(c, newActorURI, err)
		return
	}
	if err := VerifyMigrationTarget(ctx, activityDB(ctx), &oldAccount, newAccount, NewFetchClient()); err != nil {
		c.JSON(422, gin.H{"error": "target must verify the source account alias"})
		return
	}
	if err := ApplyAccountMove(activityDB(ctx), oldAccount.Id, newAccount.Id, false); err != nil {
		c.JSON(500, gin.H{"error": "account migration could not be applied"})
		return
	}

	// Create notification for followers about the move
	createAccountMoveNotification(ctx, oldAccount.Id, newAccount.Id)

	log.Info("Successfully processed account migration from %s to %s", oldActorURI, newActorURI)
	c.JSON(200, gin.H{"status": "accepted"})
}

// Helper functions

// getOrCreateRemoteAccount gets or creates a remote account from an actor URI.
func getOrCreateRemoteAccount(actorURI string) (*models.Account, error) {
	return getOrCreateRemoteAccountContext(context.Background(), actorURI)
}
func getOrCreateRemoteAccountContext(ctx context.Context, actorURI string) (*models.Account, error) {
	var account models.Account
	err := activityDB(ctx).Where("origin_id = ? AND domain IS NOT NULL", actorURI).First(&account).Error
	if err == nil {
		return &account, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err := validateFederationURI(actorURI); err != nil {
		return nil, &actorFetchError{actorURI: actorURI, err: err}
	}
	actor, err := NewFetchClient().FetchActor(ctx, actorURI)
	if err != nil {
		return nil, &actorFetchError{actorURI: actorURI, err: err}
	}
	if actor.ID != actorURI {
		// Threads advertises ".../ap/users/<id>/" but servers differ on the
		// trailing slash; that is still the same document. Keep the URI we
		// were given so later lookups by it match the stored row.
		if strings.TrimSuffix(actor.ID, "/") != strings.TrimSuffix(actorURI, "/") {
			return nil, &actorFetchError{actorURI: actorURI, err: errors.New("actor ID does not match requested URI")}
		}
		actor.ID = actorURI
	}
	account, err = createRemoteAccount(activityDB(ctx), actor)
	return &account, err
}

// actorFetchError means a remote actor could not be dereferenced, as
// opposed to a local database failure.
type actorFetchError struct {
	actorURI string
	err      error
}

func (e *actorFetchError) Error() string {
	return fmt.Sprintf("fetching actor %s: %v", e.actorURI, e.err)
}
func (e *actorFetchError) Unwrap() error { return e.err }

// respondRemoteAccountError answers an inbox delivery whose actor could not
// be resolved. A 5xx makes Mastodon, Misskey and relays redeliver, so it is
// reserved for failures a retry can fix: local database errors (500) and
// transient remote failures such as timeouts, 429 or 5xx (503 + Retry-After;
// the sender's backoff bounds the retries). A permanent failure (the actor
// is gone, refuses us even with a signed fetch, or serves a mismatching
// document) is acknowledged with 202 and dropped, because redelivering it
// can never succeed.
func respondRemoteAccountError(c *gin.Context, actorURI string, err error) {
	var fetchErr *actorFetchError
	if !errors.As(err, &fetchErr) {
		log.Error("Failed to get/create remote account %s: %v", actorURI, err)
		c.JSON(500, gin.H{"error": "failed to process account"})
		return
	}
	if isTransientFetchError(err) {
		log.Warn("Deferring activity from %s: actor temporarily unavailable: %v", actorURI, err)
		c.Header("Retry-After", "300")
		c.JSON(503, gin.H{"error": "actor temporarily unavailable"})
		return
	}
	log.Warn("Dropping activity: cannot resolve remote actor: %v", err)
	c.JSON(202, gin.H{"status": "ignored", "reason": "actor unavailable"})
}

// ImportRemoteActor resolves and persists a verified remote actor identity.
func ImportRemoteActor(ctx context.Context, actorURI string) (*models.Account, error) {
	if err := validateFederationURI(actorURI); err != nil {
		return nil, err
	}
	return NewResolver(nil, "").ResolveAccount(ctx, actorURI)
}

// getVisibilityFromActivity determines the visibility of a Note from its ActivityPub properties.
func getVisibilityFromActivity(object map[string]any) uint8 {
	toStrs := activityAddresses(object["to"])
	ccStrs := activityAddresses(object["cc"])

	// Check for public visibility
	for _, t := range toStrs {
		if t == "https://www.w3.org/ns/activitystreams#Public" || t == "as:Public" || t == "Public" {
			return models.StatusVisibilityPublic
		}
	}

	for _, c := range ccStrs {
		if c == "https://www.w3.org/ns/activitystreams#Public" || c == "as:Public" || c == "Public" {
			return models.StatusVisibilityUnlisted
		}
	}

	// Check if "to" only contains specific user URIs (no followers collection)
	hasFollowersCollection := false
	hasUserRecipient := false

	for _, t := range toStrs {
		if strings.HasSuffix(t, "/followers") {
			hasFollowersCollection = true
		} else if strings.Contains(t, "/users/") || strings.Contains(t, "/@") {
			hasUserRecipient = true
		}
	}

	for _, c := range ccStrs {
		if strings.HasSuffix(c, "/followers") {
			hasFollowersCollection = true
		}
	}

	if hasFollowersCollection {
		// Has followers collection but not public = private/followers-only
		return models.StatusVisibilityPrivate
	}

	if hasUserRecipient {
		// Only specific users, no followers collection = direct message
		return models.StatusVisibilityDirect
	}

	// Default to private
	return models.StatusVisibilityPrivate
}

// sendAcceptActivity sends an Accept activity to a remote server.
func queueAcceptActivity(ctx context.Context, localAccount *models.Account, remoteAccount *models.Account, followActivityID string) error {
	actorURL := localAccount.ActorURI(systemsettings.GetBaseURL())
	targetURL := localAccount.ActorURI(systemsettings.GetBaseURL())

	// Get remote inbox
	var inboxURL string

	if inbox, ok := ctx.Value(followerInboxKey{}).(followerInbox); ok && inbox.actor == remoteAccount.OriginId {
		inboxURL = inbox.url
	} else if remoteAccount.OriginId != "" {
		// Try to get inbox from remote actor
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		client := NewFetchClient()
		if actor, err := client.FetchActor(ctx, remoteAccount.OriginId); err == nil {
			inboxURL = actor.Inbox
		}
	}

	if inboxURL == "" {
		domain := utils.ExtractDomainFromURI(remoteAccount.OriginId)
		inboxURL = fmt.Sprintf("https://%s/inbox", domain)
	}

	now := time.Now()
	acceptActivity := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       actorURL + "#accepts/follows/" + strconv.FormatUint(uint64(time.Now().UnixNano()), 10),
		"type":     "Accept",
		"actor":    actorURL,
		"object": map[string]any{
			"id":     followActivityID,
			"type":   "Follow",
			"actor":  remoteAccount.OriginId,
			"object": targetURL,
		},
		"published": now.Format(time.RFC3339),
	}

	payload, err := json.Marshal(acceptActivity)
	if err != nil {
		return err
	}
	return activityDB(ctx).Create(&Delivery{ActivityID: acceptActivity["id"].(string), TargetInbox: inboxURL, Payload: string(payload), Status: DeliveryStatusPending}).Error
}

// handleAddActivity handles Add activities (adding items to collections)
// Typically used for adding to featured collections or lists.
func handleAddActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) { //nolint:unparam // param reserved for future use
	actor, _ := ctx.Value(actorKey).(string)

	object, ok := activity["object"].(string)
	if !ok {
		// Object might be a map
		if objMap, isMap := activity["object"].(map[string]any); isMap {
			object, _ = objMap["id"].(string)
		}
	}

	target, ok := activity["target"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid target"})

		return
	}

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccountContext(ctx, actor) //nolint:contextcheck
	if err != nil {
		respondRemoteAccountError(c, actor, err)
		return
	}

	log.Info("Received Add from %s: object=%s target=%s", actor, object, target)

	// Handle different target types
	if strings.Contains(target, "/collections/featured") {
		// Adding to featured/pinned statuses collection
		var status models.Status
		err := activityDB(ctx).Scopes(statusByObjectRef(object)).First(&status).Error
		if err == nil {
			// Mark as featured (pinned)
			if status.AccountId == remoteAccount.Id {
				now := time.Now()
				activityDB(ctx).Model(&status).Updates(map[string]any{
					"pinned":     true,
					"updated_at": now,
				})
				log.Info("Pinned status %d from %s", status.Id, actor)
			}
		}
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleRemoveActivity handles Remove activities (removing items from collections)
// Typically used for removing from featured collections or lists.
func handleRemoveActivity(ctx context.Context, c *gin.Context, activity map[string]any, targetAccount *models.Account) { //nolint:unparam // param reserved for future use
	actor, _ := ctx.Value(actorKey).(string)

	object, ok := activity["object"].(string)
	if !ok {
		// Object might be a map
		if objMap, isMap := activity["object"].(map[string]any); isMap {
			object, _ = objMap["id"].(string)
		}
	}

	target, ok := activity["target"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid target"})

		return
	}

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccountContext(ctx, actor) //nolint:contextcheck
	if err != nil {
		respondRemoteAccountError(c, actor, err)
		return
	}

	log.Info("Received Remove from %s: object=%s target=%s", actor, object, target)

	// Handle different target types
	if strings.Contains(target, "/collections/featured") {
		// Removing from featured/pinned statuses collection
		var status models.Status
		err := activityDB(ctx).Scopes(statusByObjectRef(object)).First(&status).Error
		if err == nil {
			// Unmark as featured (unpin)
			if status.AccountId == remoteAccount.Id {
				now := time.Now()
				activityDB(ctx).Model(&status).Updates(map[string]any{
					"pinned":     false,
					"updated_at": now,
				})
				log.Info("Unpinned status %d from %s", status.Id, actor)
			}
		}
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// SharedInbox handles POST /inbox (shared inbox).
func SharedInbox(c *gin.Context) {
	// For now, delegate to regular inbox handling
	Inbox(c)
}

// createFollowRequestNotification creates a notification for follow requests.
func createFollowRequestNotificationContext(ctx context.Context, fromAccountId, toAccountId uint64) {
	notification := &models.Notification{
		AccountId:     toAccountId,
		FromAccountId: fromAccountId,
		Type:          models.NotificationTypeFollowRequest,
	}

	err := activityDB(ctx).Create(notification).Error
	if err != nil {
		log.Error("Failed to create follow request notification: %v", err)
	} else {
		log.Debug("Created follow request notification for account %d from %d", toAccountId, fromAccountId)
	}
}

// createAdminReportNotification creates a notification for admins about a new report.
func createAdminReportNotification(ctx context.Context, reportId uint64, fromAccountId, targetAccountId uint64) { //nolint:unparam // param reserved for future use
	// Find all admin users
	var adminRoles []models.Role
	err := activityDB(ctx).Where("name = ? OR name = ?", "admin", "moderator").Find(&adminRoles).Error
	if err != nil {
		log.Error("Failed to find admin roles: %v", err)

		return
	}

	if len(adminRoles) == 0 {
		// No admin roles found, skip notification
		return
	}

	var roleIds []uint64
	for _, role := range adminRoles {
		roleIds = append(roleIds, uint64(role.ID)) //nolint:gosec // G115: reviewed and safe
	}

	// Find all users with admin/moderator roles
	var roleUsers []models.RoleUser
	err = activityDB(ctx).Where("role_id IN ?", roleIds).Find(&roleUsers).Error
	if err != nil {
		log.Error("Failed to find user roles: %v", err)

		return
	}

	// Create notification for each admin
	for _, roleUser := range roleUsers {
		// Find the account for this user
		var account models.Account
		err := activityDB(ctx).Where("user_id = ?", roleUser.UserID).First(&account).Error
		if err != nil {
			continue
		}

		notification := &models.Notification{
			AccountId:     account.Id,
			FromAccountId: fromAccountId,
			ReportId:      reportId,
			Type:          models.NotificationTypeAdminReport,
		}

		err = activityDB(ctx).Create(notification).Error
		if err != nil {
			log.Error("Failed to create admin report notification: %v", err)
		}
	}

	log.Debug("Created admin report notifications for report %d", reportId)
}

// createAccountMoveNotification notifies followers about account migration.
func createAccountMoveNotification(ctx context.Context, oldAccountId, newAccountId uint64) { //nolint:unparam // param reserved for future use
	// Find all local followers of the old account
	var follows []models.Follow
	err := activityDB(ctx).Where("follow_id = ? AND account_id IN (SELECT id FROM accounts WHERE domain IS NULL)", oldAccountId).Find(&follows).Error
	if err != nil {
		log.Error("Failed to find followers for move notification: %v", err)

		return
	}

	for _, follow := range follows {
		// Create notification about the account move
		notification := &models.Notification{
			AccountId:     follow.AccountId,
			FromAccountId: oldAccountId,
			Type:          models.NotificationTypeFollow,
		}

		err := activityDB(ctx).Create(notification).Error
		if err != nil {
			log.Error("Failed to create move notification: %v", err)
		}
	}

	log.Debug("Created move notifications for %d followers", len(follows))
}

// fetchAndCreateRemoteStatus fetches a remote status and creates a local copy.
func fetchAndCreateRemoteStatus(ctx context.Context, statusURI string) (*models.Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Fetch the status from its canonical id (signed retry on 401/403),
	// unless the inbox already fetched it outside its transaction.
	var statusData map[string]any
	var canonical string
	var err error
	if pre, ok := ctx.Value(prefetchedObjectKey{}).(prefetchedObject); ok && pre.uri == statusURI {
		statusData, canonical, err = pre.data, pre.canonical, pre.err
	} else {
		statusData, canonical, err = NewResolver(nil, "").fetchCanonicalObject(ctx, statusURI)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fetch remote status: %w", err)
	}
	if canonical != statusURI {
		var existing models.Status
		if activityDB(ctx).Where("origin_uri = ?", canonical).First(&existing).Error == nil {
			return &existing, nil
		}
		statusURI = canonical
	}

	return importFetchedStatus(ctx, statusURI, statusData)
}

// rememberReplyParent records a reply target that is already stored here.
// Missing parents stay unlinked; reading a notification must not fetch them.
func noteInReplyTo(value any) string {
	return strings.TrimSpace(objectID(value))
}

func rememberReplyParent(ctx context.Context, status *models.Status, inReplyTo string) error {
	inReplyTo = strings.TrimSpace(inReplyTo)
	if status == nil || inReplyTo == "" || (status.InReplyToId != nil && *status.InReplyToId != 0) {
		return nil
	}
	parent, err := findInboundParent(ctx, inReplyTo)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		models.SetReplyToURI(status, inReplyTo)
		return nil
	}
	if err != nil {
		return err
	}
	if parent.Id != 0 && parent.Id == status.Id {
		return nil
	}
	status.InReplyToId = &parent.Id
	status.InReplyToAccountId = &parent.AccountId
	models.SetReplyToURI(status, "")
	return nil
}

// backfillReplyParent repairs a reply that was saved before its parent
// could be matched, so a later delivery still exposes that parent.
func backfillReplyParent(ctx context.Context, status *models.Status, inReplyTo string) error {
	if status == nil || status.Id == 0 {
		return nil
	}
	if err := rememberReplyParent(ctx, status, inReplyTo); err != nil {
		return err
	}
	return activityDB(ctx).Model(status).Select("InReplyToId", "InReplyToAccountId", "Properties").Updates(status).Error
}

// importFetchedStatus stores an already fetched, canonical status object.
func importFetchedStatus(ctx context.Context, statusURI string, statusData map[string]any) (*models.Status, error) {
	var err error
	// Parse the status data
	statusType, _ := statusData["type"].(string)
	if !isStatusObjectType(statusType) {
		return nil, fmt.Errorf("unsupported status type: %s", statusType)
	}

	content, _ := statusData["content"].(string)
	summary, _ := statusData["summary"].(string)
	content = htmlsafe.Sanitize(content)
	summary = htmlsafe.StripTags(summary)
	publishedStr, _ := statusData["published"].(string)
	authorURI := attributedToURI(statusData)
	inReplyTo := noteInReplyTo(statusData["inReplyTo"])

	// The author must be hosted where the object is: otherwise any server
	// could publish a note "by" someone else and have it boosted in.
	if authorURI == "" || !sameURIOrigin(statusURI, authorURI) {
		return nil, fmt.Errorf("remote status %s is not hosted by its author %q", statusURI, authorURI)
	}
	authorAccount, err := getOrCreateRemoteAccountContext(ctx, authorURI)
	if err != nil {
		return nil, fmt.Errorf("failed to get author account: %w", err)
	}

	// Parse visibility from to/cc fields
	visibility := uint8(models.StatusVisibilityPublic)
	to, _ := statusData["to"].([]any)
	cc, _ := statusData["cc"].([]any)

	isPublic := false
	isUnlisted := false

	for _, t := range to {
		if t == "https://www.w3.org/ns/activitystreams#Public" || t == "as:Public" || t == "Public" {
			isPublic = true

			break
		}
	}

	for _, c := range cc {
		if c == "https://www.w3.org/ns/activitystreams#Public" || c == "as:Public" || c == "Public" {
			isUnlisted = true

			break
		}
	}

	if !isPublic && !isUnlisted {
		// Check if it's a direct message
		if len(to) == 1 && len(cc) == 0 {
			visibility = uint8(models.StatusVisibilityDirect)
		} else {
			visibility = uint8(models.StatusVisibilityPrivate)
		}
	} else if isUnlisted {
		visibility = uint8(models.StatusVisibilityUnlisted)
	}

	// Parse published time
	publishedAt := utils.ParseRFC3339(publishedStr)
	if publishedAt == nil {
		now := time.Now()
		publishedAt = &now
	}

	// Create the status
	status := models.Status{
		AccountId:   authorAccount.Id,
		Visibility:  visibility,
		OriginUri:   &statusURI,
		InReplyToId: nil,
		Language:    langdetect.FromContentMap(statusData),
		CreatedAt:   publishedAt,
		UpdatedAt:   publishedAt,
		Poll:        questionPoll(statusData, authorAccount.Id),
	}

	// Remote statuses carry their host in statuses.domain (suspension, link
	// cards, refresh jobs and local-post counts all key on it).
	if authorAccount.Domain != nil && *authorAccount.Domain != "" {
		status.Domain = authorAccount.Domain
	} else if host := utils.ExtractDomainFromURI(statusURI); host != "" {
		status.Domain = &host
	}

	// Link a parent this server already has. A numeric local id
	// (/users/:name/statuses/:id) is not stored in local_object_uri.
	if err = rememberReplyParent(ctx, &status, inReplyTo); err != nil {
		return nil, err
	}

	statusHost := ""
	if status.Domain != nil {
		statusHost = *status.Domain
	}
	probe := status
	probe.Content = models.StatusContent{Content: content, SpoilerText: summary}
	if !applyInboundNotePolicy(&probe, statusData, statusHost) {
		return nil, fmt.Errorf("remote status %s contains prohibited words", statusURI)
	}
	status.Visibility = probe.Visibility

	status.Attachments, err = incomingAttachments(ctx, statusData["attachment"], authorAccount.Id)
	if err != nil {
		return nil, err
	}
	applyMediaSilence(status.Attachments, statusHost)
	noteSensitive, _ := statusData["sensitive"].(bool)
	status.Sensitive = deriveSensitive(noteSensitive, summary, status.Attachments)

	// Create status in transaction (safe against panics — Transaction auto-rolls-back)
	err = activityDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&status).Error; err != nil {
			return fmt.Errorf("failed to create status: %w", err)
		}

		// Create status content
		statusContent := &models.StatusContent{
			StatusId:    status.Id,
			Content:     content,
			SpoilerText: summary,
		}

		if err := tx.WithContext(withObjectHashtags(ctx, statusData["tag"])).Create(statusContent).Error; err != nil {
			return fmt.Errorf("failed to create status content: %w", err)
		}

		// Load the complete status
		status.Content = *statusContent

		return nil
	})
	if err != nil {
		return nil, err
	}

	log.Info("Created remote status %d from %s", status.Id, statusURI)

	return &status, nil
}

// statusByObjectRef matches a status by its object URI (remote origin or
// preserved local URI), or by numeric id. The id comparison is only added
// for numeric references: on PostgreSQL comparing the bigint id with a URL
// is an error that aborts the surrounding inbox transaction.
func statusByObjectRef(object string) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB {
		if id, err := strconv.ParseUint(object, 10, 64); err == nil {
			return q.Where("origin_uri = ? OR origin_url = ? OR local_object_uri = ? OR id = ?", object, object, object, id)
		}
		return q.Where("origin_uri = ? OR origin_url = ? OR local_object_uri = ?", object, object, object)
	}
}

// isSelfDeleteActivity reports whether activity is an actor deleting itself.
func isSelfDeleteActivity(activity map[string]any) bool {
	if t, _ := activity["type"].(string); t != "Delete" {
		return false
	}
	actor, _ := activity["actor"].(string)
	object, ok := activity["object"].(string)
	if !ok {
		if m, isMap := activity["object"].(map[string]any); isMap {
			object, _ = m["id"].(string)
		}
	}
	return actor != "" && object == actor
}

// isRoutineSignatureFailure separates the expected background noise of
// federation (unsigned probes, keys of deleted actors) from failures worth a
// warning (bad digests, wrong keys).
func isRoutineSignatureFailure(err error) bool {
	if code := fetchStatusCode(err); code == http.StatusGone || code == http.StatusNotFound {
		return true
	}
	if errors.Is(err, ErrActorGone) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "no signature header") || strings.Contains(msg, "missing required signature") ||
		strings.Contains(msg, "status 410") || strings.Contains(msg, "status 404") || strings.Contains(msg, "HTTP 410")
}

func alreadyProcessedActivity(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	var ids []uint64
	if err := db.GetDB().WithContext(ctx).Model(&ProcessedActivity{}).Where("activity_id = ?", id).Limit(1).Pluck("id", &ids).Error; err != nil {
		return false
	}
	return len(ids) > 0
}

type followerInboxKey struct{}

type followerInbox struct{ actor, url string }

// withFollowerInbox resolves actor's inbox before the inbox transaction opens.
// An empty result is cached too, so the transaction never fetches again.
func withFollowerInbox(ctx context.Context, actor string) context.Context {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	url := ""
	if remote, err := NewFetchClient().FetchActor(fetchCtx, actor); err == nil && remote.ID == actor && validateFederationURI(remote.Inbox) == nil {
		url = remote.Inbox
	}
	return context.WithValue(ctx, followerInboxKey{}, followerInbox{actor: actor, url: url})
}

type prefetchedObjectKey struct{}

type prefetchedObject struct {
	uri, canonical string
	data           map[string]any
	err            error
}

func withPrefetchedAnnounceObject(ctx context.Context, object any) context.Context {
	uri, _ := announceObject(object)
	if uri == "" {
		return ctx
	}
	var ids []uint64
	if err := db.GetDB().WithContext(ctx).Model(&models.Status{}).Scopes(statusByObjectRef(uri)).Limit(1).Pluck("statuses.id", &ids).Error; err != nil || len(ids) > 0 {
		return ctx
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	data, canonical, err := NewResolver(nil, "").fetchCanonicalObject(fetchCtx, uri)
	return context.WithValue(ctx, prefetchedObjectKey{}, prefetchedObject{uri: uri, canonical: canonical, data: data, err: err})
}
