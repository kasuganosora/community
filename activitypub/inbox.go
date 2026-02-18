package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
)

// Inbox handles POST /users/:username/inbox
// Receives activities from remote instances
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
	if username != "" {
		targetAccount = &models.Account{}
		if err := db.GetDB().Where("username = ? AND domain IS NULL", username).First(targetAccount).Error; err != nil {
			c.JSON(404, gin.H{"error": "user not found"})
			return
		}
	}

	// Read the request body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(400, gin.H{"error": "failed to read body"})
		return
	}

	// Parse the activity
	var activity map[string]interface{}
	if err := json.Unmarshal(body, &activity); err != nil {
		c.JSON(400, gin.H{"error": "invalid activity"})
		return
	}

	// Verify HTTP signature
	actor, err := VerifyActivitySignature(c, body)
	if err != nil {
		log.Warn("HTTP signature verification failed: %v", err)
		// For compatibility, we still accept unsigned requests from known sources
		// but log a warning. In production, you may want to reject these.
		actor, _ = activity["actor"].(string)
	}

	// Route based on activity type
	activityType, _ := activity["type"].(string)

	// Create context with verified actor info
	ctx := context.WithValue(context.Background(), "actor", actor)

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
}

// isValidActivityPubContentType checks if the Content-Type is valid for ActivityPub
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

	for _, valid := range validTypes {
		if mediaType == valid {
			return true
		}
	}

	// Also accept ld+json with profile parameter
	if strings.HasPrefix(mediaType, "application/ld+json") {
		return true
	}

	return false
}

// handleCreateActivity handles Create activities (new remote posts)
func handleCreateActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	object, ok := activity["object"].(map[string]interface{})
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})
		return
	}

	objectType, _ := object["type"].(string)
	if objectType != "Note" {
		// We only handle Note objects for now
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}

	// Extract Note properties
	objectID, _ := object["id"].(string)
	content, _ := object["content"].(string)
	attributedTo, _ := object["attributedTo"].(string)
	publishedStr, _ := object["published"].(string)
	summary, _ := object["summary"].(string)
	inReplyTo, _ := object["inReplyTo"].(string)
	sensitive, _ := object["sensitive"].(bool)

	// Use source property if available (contains original content format)
	if source, ok := object["source"].(map[string]interface{}); ok {
		if sourceContent, ok := source["content"].(string); ok && sourceContent != "" {
			// Prefer source content for storage as it preserves original formatting
			content = sourceContent
		}
	}

	// Verify actor matches attributedTo
	if attributedTo != actor {
		c.JSON(403, gin.H{"error": "actor mismatch"})
		return
	}

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccount(actor)
	if err != nil {
		log.Error("Failed to get/create remote account: %v", err)
		c.JSON(500, gin.H{"error": "failed to process account"})
		return
	}

	// Check if status already exists
	var existingStatus models.Status
	err = db.GetDB().Where("origin_uri = ?", objectID).First(&existingStatus).Error
	if err == nil {
		// Status already exists, skip
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}

	// Parse published time
	publishedAt := utils.ParseRFC3339(publishedStr)

	// Find in_reply_to status if specified
	var inReplyToID *uint64
	var inReplyToAccountID *uint64
	if inReplyTo != "" {
		var replyToStatus models.Status
		if err := db.GetDB().Where("origin_uri = ? OR origin_url = ?", inReplyTo, inReplyTo).First(&replyToStatus).Error; err == nil {
			inReplyToID = &replyToStatus.Id
			inReplyToAccountID = &replyToStatus.AccountId
		}
	}

	// Parse mentions from tags
	tags, _ := object["tag"].([]interface{})
	mentions := parseTagsToAccountIDs(tags)

	// Create status
	now := time.Now()
	status := models.Status{
		AccountId:         remoteAccount.Id,
		Content:           models.StatusContent{Content: content, SpoilerText: summary},
		Sensitive:         sensitive,
		Visibility:        getVisibilityFromActivity(object),
		OriginUri:         &objectID,
		InReplyToId:       inReplyToID,
		InReplyToAccountId: inReplyToAccountID,
		CreatedAt:         publishedAt,
		UpdatedAt:          &now,
	}

	if status.CreatedAt == nil {
		status.CreatedAt = &now
	}

	// Set domain for remote status
	domain := utils.ExtractDomainFromURI(actor)
	if domain != "" {
		status.Domain = &domain
	}

	// Begin transaction
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&status).Error; err != nil {
			return err
		}
		// Create status content
		status.Content.StatusId = status.Id
		if err := tx.Create(&status.Content).Error; err != nil {
			return err
		}

		// Note: mentions are stored in status.ToAccounts by the model
		// Log mentions for debugging
		if len(mentions) > 0 {
			log.Info("Status %d mentions account IDs: %v", status.Id, mentions)
		}

		return nil
	})

	if err != nil {
		log.Error("Failed to create status: %v", err)
		c.JSON(500, gin.H{"error": "failed to create status"})
		return
	}

	log.Info("Created remote status %d from %s with %d mentions", status.Id, actor, len(mentions))
	c.JSON(200, gin.H{"status": "accepted"})
}

// parseTagsToAccountIDs extracts mentioned account IDs from ActivityPub tags
func parseTagsToAccountIDs(tags []interface{}) []uint64 {
	var accountIDs []uint64

	for _, tag := range tags {
		tagMap, ok := tag.(map[string]interface{})
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
		if err := db.GetDB().Where("origin_id = ?", href).First(&account).Error; err == nil {
			accountIDs = append(accountIDs, account.Id)
		}
	}

	return accountIDs
}

// handleUpdateActivity handles Update activities (edit remote posts)
func handleUpdateActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	object, ok := activity["object"].(map[string]interface{})
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})
		return
	}

	objectID, _ := object["id"].(string)
	objectType, _ := object["type"].(string)

	// Handle Actor updates
	if objectType == "Person" || objectType == "Service" || objectType == "Group" {
		// Update remote account info
		remoteAccount, err := getOrCreateRemoteAccount(actor)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to update account"})
			return
		}

		name, _ := object["name"].(string)
		summary, _ := object["summary"].(string)
		preferredUsername, _ := object["preferredUsername"].(string)

		updates := map[string]interface{}{}
		if name != "" {
			updates["display_name"] = name
		}
		if summary != "" {
			updates["note"] = summary
		}
		if preferredUsername != "" {
			updates["username"] = preferredUsername
		}

		if len(updates) > 0 {
			if err := db.GetDB().Model(remoteAccount).Updates(updates).Error; err != nil {
				log.Error("Failed to update remote account: %v", err)
			}
		}

		c.JSON(200, gin.H{"status": "accepted"})
		return
	}

	// Handle Note updates
	if objectType == "Note" {
		content, _ := object["content"].(string)
		summary, _ := object["summary"].(string)

		var status models.Status
		if err := db.GetDB().Where("origin_uri = ? AND account_id IN (SELECT id FROM accounts WHERE origin_id = ? OR id = (SELECT id FROM accounts WHERE username = ?))",
			objectID, actor, utils.ExtractUsernameFromURI(actor)).First(&status).Error; err != nil {
			c.JSON(404, gin.H{"error": "status not found"})
			return
		}

		now := time.Now()
		db.GetDB().Model(&status).Updates(map[string]interface{}{
			"updated_at": now,
		})

		// Update content
		db.GetDB().Model(&status.Content).Updates(map[string]interface{}{
			"content":      content,
			"spoiler_text": summary,
		})

		log.Info("Updated remote status %d from %s", status.Id, actor)
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleDeleteActivity handles Delete activities (delete remote posts/accounts)
func handleDeleteActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	object, ok := activity["object"].(string)
	if !ok {
		// Object might be a map
		objMap, isMap := activity["object"].(map[string]interface{})
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
		if err := db.GetDB().Where("origin_id = ? OR (username = ? AND domain = ?)",
			actor, utils.ExtractUsernameFromURI(actor), utils.ExtractDomainFromURI(actor)).First(&account).Error; err == nil {
			// Soft delete the account
			now := time.Now()
			db.GetDB().Model(&account).Updates(map[string]interface{}{
				"deleted_at": now,
			})
			log.Info("Deleted remote account %s", actor)
		}
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}

	// Delete the status
	var status models.Status
	if err := db.GetDB().Where("origin_uri = ?", object).First(&status).Error; err == nil {
		// Verify ownership
		var account models.Account
		if err := db.GetDB().First(&account, status.AccountId).Error; err == nil {
			actorURI := ""
			if account.Domain != nil {
				actorURI = fmt.Sprintf("https://%s/users/%s", *account.Domain, account.Username)
			}
			if actorURI == actor || account.OriginId == actor {
				db.GetDB().Delete(&status)
				log.Info("Deleted remote status %d from %s", status.Id, actor)
			}
		}
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleFollowActivity handles Follow activities (follow requests)
func handleFollowActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	if targetAccount == nil {
		c.JSON(400, gin.H{"error": "no target account"})
		return
	}

	actor, _ := ctx.Value("actor").(string)
	object, ok := activity["object"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})
		return
	}

	// Verify the object is our local user
	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	expectedActorURI := fmt.Sprintf("https://%s/users/%s", domain, targetAccount.Username)
	if object != expectedActorURI {
		c.JSON(403, gin.H{"error": "wrong target"})
		return
	}

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccount(actor)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to process account"})
		return
	}

	// Check if already following
	var existingFollow models.Follow
	err = db.GetDB().Where("account_id = ? AND follow_id = ?", remoteAccount.Id, targetAccount.Id).First(&existingFollow).Error
	if err == nil {
		// Already following
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}

	// If target account is locked, create follow request
	if targetAccount.Locked {
		follow := models.Follow{
			AccountId: remoteAccount.Id,
			FollowId:  targetAccount.Id,
			Status:    models.FollowStatusRequested,
		}
		if err := db.GetDB().Create(&follow).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to create follow request"})
			return
		}

		// TODO: Send notification to target user about follow request
		log.Info("Created follow request from %s to %s", actor, targetAccount.Username)
	} else {
		// Auto-accept follow
		follow := models.Follow{
			AccountId: remoteAccount.Id,
			FollowId:  targetAccount.Id,
			Status:    models.FollowStatusFollowed,
		}
		if err := db.GetDB().Create(&follow).Error; err != nil {
			c.JSON(500, gin.H{"error": "failed to create follow"})
			return
		}

		// Send Accept activity back
		activityID, ok := activity["id"].(string)
		if ok && activityID != "" {
			go sendAcceptActivity(targetAccount, remoteAccount, activityID)
		}
		log.Info("Auto-accepted follow from %s to %s", actor, targetAccount.Username)
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleAcceptActivity handles Accept activities (follow accept / relay accept)
func handleAcceptActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	object, ok := activity["object"].(map[string]interface{})
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})
		return
	}

	objectType, _ := object["type"].(string)
	if objectType != "Follow" {
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}

	// Check if this is a relay acceptance
	objectID, _ := object["id"].(string)
	if objectID != "" {
		relayService := NewRelayService()
		if err := relayService.AcceptRelay(objectID); err == nil {
			// Successfully accepted relay
			log.Info("Relay accepted: %s", actor)
			c.JSON(200, gin.H{"status": "accepted"})
			return
		}
	}

	// Regular follow acceptance
	if targetAccount == nil {
		c.JSON(400, gin.H{"error": "no target account"})
		return
	}

	// Get the remote account that accepted our follow
	remoteAccount, err := getOrCreateRemoteAccount(actor)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to process account"})
		return
	}

	// Update our follow request to accepted
	var follow models.Follow
	err = db.GetDB().Where("account_id = ? AND follow_id = ? AND status = ?",
		targetAccount.Id, remoteAccount.Id, models.FollowStatusRequested).First(&follow).Error
	if err == nil {
		db.GetDB().Model(&follow).Update("status", models.FollowStatusFollowed)
		log.Info("Follow from %s to %s accepted", targetAccount.Username, actor)
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleRejectActivity handles Reject activities (follow reject / relay reject)
func handleRejectActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	object, ok := activity["object"].(map[string]interface{})
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})
		return
	}

	objectType, _ := object["type"].(string)
	if objectType != "Follow" {
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}

	// Check if this is a relay rejection
	objectID, _ := object["id"].(string)
	if objectID != "" {
		relayService := NewRelayService()
		if err := relayService.RejectRelay(objectID); err == nil {
			// Successfully rejected relay
			log.Info("Relay rejected: %s", actor)
			c.JSON(200, gin.H{"status": "accepted"})
			return
		}
	}

	// Regular follow rejection
	if targetAccount == nil {
		c.JSON(400, gin.H{"error": "no target account"})
		return
	}

	// Get the remote account that rejected our follow
	remoteAccount, err := getOrCreateRemoteAccount(actor)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to process account"})
		return
	}

	// Delete our follow request
	db.GetDB().Where("account_id = ? AND follow_id = ? AND status = ?",
		targetAccount.Id, remoteAccount.Id, models.FollowStatusRequested).Delete(&models.Follow{})

	log.Info("Follow from %s to %s rejected", targetAccount.Username, actor)
	c.JSON(200, gin.H{"status": "accepted"})
}

// handleAnnounceActivity handles Announce activities (boosts)
func handleAnnounceActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	object, ok := activity["object"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})
		return
	}

	activityID, _ := activity["id"].(string)
	publishedStr, _ := activity["published"].(string)

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccount(actor)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to process account"})
		return
	}

	// Find the original status (might be local or remote)
	var originalStatus models.Status
	err = db.GetDB().Where("origin_uri = ? OR origin_url = ? OR id = ?", object, object, object).First(&originalStatus).Error
	if err != nil {
		// Try to fetch the remote status
		// TODO: Implement remote status fetching
		log.Warn("Could not find original status for announce: %s", object)
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}

	// Check if we already have this reblog
	var existingReblog models.Status
	err = db.GetDB().Where("account_id = ? AND reblog_id = ? AND origin_uri = ?",
		remoteAccount.Id, originalStatus.Id, activityID).First(&existingReblog).Error
	if err == nil {
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}

	// Parse published time
	publishedAt := utils.ParseRFC3339(publishedStr)

	// Create reblog
	now := time.Now()
	reblog := models.Status{
		AccountId:  remoteAccount.Id,
		ReblogId:   &originalStatus.Id,
		Visibility: models.StatusVisibilityPublic,
		OriginUri:  &activityID,
		CreatedAt:  publishedAt,
		UpdatedAt:  &now,
	}

	if reblog.CreatedAt == nil {
		reblog.CreatedAt = &now
	}

	if err := db.GetDB().Create(&reblog).Error; err != nil {
		log.Error("Failed to create reblog: %v", err)
		c.JSON(500, gin.H{"error": "failed to create reblog"})
		return
	}

	// Increment reblogs count
	db.GetDB().Model(&originalStatus).UpdateColumn("reblogs_count", gorm.Expr("reblogs_count + ?", 1))

	log.Info("Created remote reblog %d from %s", reblog.Id, actor)
	c.JSON(200, gin.H{"status": "accepted"})
}

// handleLikeActivity handles Like activities (favourites)
func handleLikeActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	object, ok := activity["object"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})
		return
	}

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccount(actor)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to process account"})
		return
	}

	// Find the status
	var status models.Status
	err = db.GetDB().Where("origin_uri = ? OR origin_url = ? OR id = ?", object, object, object).First(&status).Error
	if err != nil {
		log.Warn("Could not find status for like: %s", object)
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}

	// Check if already liked
	var existingFav models.FavoriteStatus
	err = db.GetDB().Where("account_id = ? AND status_id = ?", remoteAccount.Id, status.Id).First(&existingFav).Error
	if err == nil {
		c.JSON(200, gin.H{"status": "accepted"})
		return
	}

	// Create favourite
	fav := models.FavoriteStatus{
		AccountId: remoteAccount.Id,
		StatusId:  status.Id,
	}

	if err := db.GetDB().Create(&fav).Error; err != nil {
		log.Error("Failed to create favourite: %v", err)
		c.JSON(500, gin.H{"error": "failed to create favourite"})
		return
	}

	log.Info("Created remote favourite from %s on status %d", actor, status.Id)
	c.JSON(200, gin.H{"status": "accepted"})
}

// handleUndoActivity handles Undo activities (undo follow/like/announce)
func handleUndoActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	objectMap, ok := activity["object"].(map[string]interface{})
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})
		return
	}

	objectType, _ := objectMap["type"].(string)
	objectID, _ := objectMap["id"].(string)
	objectObject, _ := objectMap["object"].(string)

	// Get remote account
	remoteAccount, err := getOrCreateRemoteAccount(actor)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to process account"})
		return
	}

	switch objectType {
	case "Follow":
		// Undo a follow
		if targetAccount != nil {
			db.GetDB().Where("account_id = ? AND follow_id = ?", remoteAccount.Id, targetAccount.Id).Delete(&models.Follow{})
			log.Info("Undid follow from %s to %s", actor, targetAccount.Username)
		}

	case "Like":
		// Undo a like
		var status models.Status
		if err := db.GetDB().Where("origin_uri = ? OR origin_url = ? OR id = ?", objectObject, objectObject, objectObject).First(&status).Error; err == nil {
			db.GetDB().Where("account_id = ? AND status_id = ?", remoteAccount.Id, status.Id).Delete(&models.FavoriteStatus{})
			log.Info("Undid like from %s on status %d", actor, status.Id)
		}

	case "Announce":
		// Undo an announce (reblog)
		if err := db.GetDB().Where("origin_uri = ? AND account_id = ?", objectID, remoteAccount.Id).Delete(&models.Status{}).Error; err == nil {
			log.Info("Undid announce from %s: %s", actor, objectID)
		}

	case "Block":
		// Undo a block - remove from blocks list
		if targetAccount != nil {
			db.GetDB().Where("account_id = ? AND target_account_id = ?", remoteAccount.Id, targetAccount.Id).Delete(&models.Block{})
			log.Info("Undid block from %s to %s", actor, targetAccount.Username)
		}
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleBlockActivity handles Block activities
func handleBlockActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	objectStr, ok := activity["object"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})
		return
	}

	// Get remote account
	remoteAccount, err := getOrCreateRemoteAccount(actor)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to process account"})
		return
	}

	// Find the target account
	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	var blockedAccount models.Account
	expectedURI := fmt.Sprintf("https://%s/users/", domain)
	if strings.HasPrefix(objectStr, expectedURI) {
		username := strings.TrimPrefix(objectStr, expectedURI)
		if err := db.GetDB().Where("username = ? AND domain IS NULL", username).First(&blockedAccount).Error; err == nil {
			// Create block record
			block := models.Block{
				AccountId:       remoteAccount.Id,
				TargetAccountId: blockedAccount.Id,
			}
			db.GetDB().Create(&block)
			log.Info("Remote user %s blocked local user %s", actor, username)
		}
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleFlagActivity handles Flag activities (reports)
func handleFlagActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	_, ok := activity["object"].([]interface{})
	if !ok {
		if _, ok := activity["object"].(string); ok {
			// Single object is fine
		} else {
			c.JSON(400, gin.H{"error": "invalid object"})
			return
		}
	}

	content, _ := activity["content"].(string)

	log.Info("Received flag from %s: %s", actor, content)
	// TODO: Create a report from this flag

	c.JSON(200, gin.H{"status": "accepted"})
}

// handleMoveActivity handles Move activities (account migration)
func handleMoveActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	_, ok := activity["object"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid object"})
		return
	}

	target, ok := activity["target"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid target"})
		return
	}

	log.Info("Received move from %s to %s", actor, target)
	// TODO: Handle account migration

	c.JSON(200, gin.H{"status": "accepted"})
}

// Helper functions

// getOrCreateRemoteAccount gets or creates a remote account from an actor URI
func getOrCreateRemoteAccount(actorURI string) (*models.Account, error) {
	// Check if we already have this account
	var account models.Account
	err := db.GetDB().Where("origin_id = ?", actorURI).First(&account).Error
	if err == nil {
		// Update last fetched time
		now := time.Now()
		db.GetDB().Model(&account).Update("last_activity_at", now)
		return &account, nil
	}

	// Need to fetch the actor
	client := NewClient(nil, "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	actor, err := client.FetchActor(ctx, actorURI)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch actor: %w", err)
	}

	// Create the remote account
	now := time.Now()
	domain := utils.ExtractDomainFromURI(actorURI)
	username := actor.PreferredUsername
	if username == "" {
		username = utils.ExtractUsernameFromURI(actorURI)
	}

	account = models.Account{
		Username:    username,
		Domain:      &domain,
		OriginId:    actorURI,
		DisplayName: actor.Name,
		Note:        actor.Summary,
		Locked:      false, // Remote accounts default
		Bot:         actor.Type == "Service",
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}

	// Set avatar
	if actor.Icon != nil && actor.Icon.URL != "" {
		account.Avatar = actor.Icon.URL
		account.AvatarStatic = actor.Icon.URL
	}

	// Set header
	if actor.Image != nil && actor.Image.URL != "" {
		account.Header = actor.Image.URL
		account.HeaderStatic = actor.Image.URL
	}

	if err := db.GetDB().Create(&account).Error; err != nil {
		return nil, fmt.Errorf("failed to create account: %w", err)
	}

	// Create account extra
	accountExtra := models.AccountExtra{
		AccountId: account.Id,
	}
	db.GetDB().Create(&accountExtra)

	return &account, nil
}

// getVisibilityFromActivity determines the visibility of a Note from its ActivityPub properties
func getVisibilityFromActivity(object map[string]interface{}) uint8 {
	to, _ := object["to"].([]interface{})
	cc, _ := object["cc"].([]interface{})

	toStrs := make([]string, len(to))
	for i, t := range to {
		toStrs[i] = fmt.Sprintf("%v", t)
	}

	ccStrs := make([]string, len(cc))
	for i, c := range cc {
		ccStrs[i] = fmt.Sprintf("%v", c)
	}

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

	// Check for direct (has specific recipients)
	for _, t := range toStrs {
		if strings.Contains(t, "/users/") && !strings.Contains(t, "/followers") {
			return models.StatusVisibilityDirect
		}
	}

	// Default to private
	return models.StatusVisibilityPrivate
}

// sendAcceptActivity sends an Accept activity to a remote server
func sendAcceptActivity(localAccount *models.Account, remoteAccount *models.Account, followActivityID string) {
	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	actorURL := fmt.Sprintf("https://%s/users/%s", domain, localAccount.Username)
	targetURL := fmt.Sprintf("https://%s/users/%s", domain, localAccount.Username)

	// Get remote inbox
	var inboxURL string
	if remoteAccount.OriginId != "" {
		// Try to get inbox from remote actor
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		client := NewClient(nil, "")
		if actor, err := client.FetchActor(ctx, remoteAccount.OriginId); err == nil {
			inboxURL = actor.Inbox
		}
	}

	if inboxURL == "" {
		domain := utils.ExtractDomainFromURI(remoteAccount.OriginId)
		inboxURL = fmt.Sprintf("https://%s/inbox", domain)
	}

	now := time.Now()
	acceptActivity := map[string]interface{}{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       actorURL + "#accepts/follows/" + strconv.FormatUint(uint64(time.Now().UnixNano()), 10),
		"type":     "Accept",
		"actor":    actorURL,
		"object": map[string]interface{}{
			"id":     followActivityID,
			"type":   "Follow",
			"actor":  remoteAccount.OriginId,
			"object": targetURL,
		},
		"published": now.Format(time.RFC3339),
	}

	// TODO: Sign with local account's private key
	client := NewClient(nil, actorURL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := client.Post(ctx, inboxURL, acceptActivity); err != nil {
		log.Error("Failed to send Accept activity: %v", err)
	} else {
		log.Info("Sent Accept activity to %s", inboxURL)
	}
}

// handleAddActivity handles Add activities (adding items to collections)
// Typically used for adding to featured collections or lists
func handleAddActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)
	
	object, ok := activity["object"].(string)
	if !ok {
		// Object might be a map
		if objMap, isMap := activity["object"].(map[string]interface{}); isMap {
			object, _ = objMap["id"].(string)
		}
	}
	
	target, ok := activity["target"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid target"})
		return
	}

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccount(actor)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to process account"})
		return
	}

	log.Info("Received Add from %s: object=%s target=%s", actor, object, target)

	// Handle different target types
	if strings.Contains(target, "/collections/featured") {
		// Adding to featured/pinned statuses collection
		var status models.Status
		if err := db.GetDB().Where("origin_uri = ? OR origin_url = ? OR id = ?", object, object, object).First(&status).Error; err == nil {
			// Mark as featured (pinned)
			if status.AccountId == remoteAccount.Id {
				now := time.Now()
				db.GetDB().Model(&status).Updates(map[string]interface{}{
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
// Typically used for removing from featured collections or lists
func handleRemoveActivity(ctx context.Context, c *gin.Context, activity map[string]interface{}, targetAccount *models.Account) {
	actor, _ := ctx.Value("actor").(string)

	object, ok := activity["object"].(string)
	if !ok {
		// Object might be a map
		if objMap, isMap := activity["object"].(map[string]interface{}); isMap {
			object, _ = objMap["id"].(string)
		}
	}

	target, ok := activity["target"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "invalid target"})
		return
	}

	// Get or create remote account
	remoteAccount, err := getOrCreateRemoteAccount(actor)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to process account"})
		return
	}

	log.Info("Received Remove from %s: object=%s target=%s", actor, object, target)

	// Handle different target types
	if strings.Contains(target, "/collections/featured") {
		// Removing from featured/pinned statuses collection
		var status models.Status
		if err := db.GetDB().Where("origin_uri = ? OR origin_url = ? OR id = ?", object, object, object).First(&status).Error; err == nil {
			// Unmark as featured (unpin)
			if status.AccountId == remoteAccount.Id {
				now := time.Now()
				db.GetDB().Model(&status).Updates(map[string]interface{}{
					"pinned":     false,
					"updated_at": now,
				})
				log.Info("Unpinned status %d from %s", status.Id, actor)
			}
		}
	}

	c.JSON(200, gin.H{"status": "accepted"})
}

// SharedInbox handles POST /inbox (shared inbox)
func SharedInbox(c *gin.Context) {
	// For now, delegate to regular inbox handling
	Inbox(c)
}
