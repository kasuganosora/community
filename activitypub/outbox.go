package activitypub

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
)

// Outbox handles GET /users/:username/outbox
// Returns the user's activity stream.
func Outbox(c *gin.Context) {
	username := c.Param("username")

	// Verify the local user exists
	var account models.Account
	err := db.GetDB().Preload("Extra").Where("username = ?", username).First(&account).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"}) //nolint:goconst

		return
	}

	// Get pagination params
	page := c.Query("page") == "true"
	minID := c.Query("min_id")
	maxID := c.Query("max_id")
	limit := 20

	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	baseURL := "https://" + domain
	outboxURL := baseURL + "/users/" + username + "/outbox"

	if !page {
		// Return collection info
		var totalItems int
		if account.Extra != nil {
			totalItems = account.Extra.StatusesCount
		}

		c.JSON(200, Collection{
			Context:    []string{"https://www.w3.org/ns/activitystreams"}, //nolint:goconst
			ID:         outboxURL,
			Type:       "OrderedCollection", //nolint:goconst
			TotalItems: totalItems,
			First:      outboxURL + "?page=true",
			Last:       outboxURL + "?page=true&min_id=0",
		})

		return
	}

	// Get statuses
	query := db.GetDB().Model(&models.Status{}).
		Where("account_id = ?", account.Id).
		Where("visibility IN ?", []string{"public", "unlisted"}).
		Order("id DESC").
		Limit(limit)

	if minID != "" {
		if id, err := strconv.ParseUint(minID, 10, 64); err == nil {
			query = query.Where("id > ?", id)
		}
	}

	if maxID != "" {
		if id, err := strconv.ParseUint(maxID, 10, 64); err == nil {
			query = query.Where("id < ?", id)
		}
	}

	var statuses []*models.Status
	err = query.Find(&statuses).Error
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to fetch statuses"})

		return
	}

	// Build ordered items
	items := make([]any, 0, len(statuses))

	for _, status := range statuses {
		activity := buildCreateActivity(status, &account, baseURL)
		items = append(items, activity)
	}

	// Build response
	pageURL := outboxURL + "?page=true"
	if minID != "" {
		pageURL += "&min_id=" + minID
	}

	response := OrderedCollectionPage{
		Context:      []string{"https://www.w3.org/ns/activitystreams"},
		ID:           pageURL,
		Type:         "OrderedCollectionPage", //nolint:goconst
		PartOf:       outboxURL,
		OrderedItems: items,
	}

	if len(statuses) == limit && len(statuses) > 0 {
		lastStatus := statuses[len(statuses)-1]
		response.Next = outboxURL + "?page=true&max_id=" + strconv.FormatUint(lastStatus.Id, 10)
	}

	c.JSON(200, response)
}

// buildCreateActivity builds a Create activity for a status.
func buildCreateActivity(status *models.Status, account *models.Account, baseURL string) map[string]any {
	statusURL := baseURL + "/users/" + account.Username + "/statuses/" + strconv.FormatUint(status.Id, 10)
	actorURL := baseURL + "/users/" + account.Username

	// Use the status's original creation time, not current time
	published := status.CreatedAt

	note := map[string]any{
		"id":           statusURL,
		"type":         "Note", //nolint:goconst
		"attributedTo": actorURL,
		"content":      status.Content.Content, //nolint:goconst
		"url":          statusURL,
		"published":    published,
		"to":           []string{"https://www.w3.org/ns/activitystreams#Public"}, //nolint:goconst
		"cc":           []string{actorURL + "/followers"},
		"sensitive":    status.Sensitive,
	}

	if status.Content.SpoilerText != "" {
		note["summary"] = status.Content.SpoilerText
	}

	return map[string]any{
		"id":        statusURL + "#create",
		"type":      "Create", //nolint:goconst
		"actor":     actorURL, //nolint:goconst
		"published": published,
		"to":        []string{"https://www.w3.org/ns/activitystreams#Public"},
		"cc":        []string{actorURL + "/followers"},
		"object":    note, //nolint:goconst
	}
}

// Followers handles GET /users/:username/followers.
func Followers(c *gin.Context) {
	username := c.Param("username")

	var account models.Account
	err := db.GetDB().Where("username = ?", username).First(&account).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"})

		return
	}

	page := c.Query("page") == "true"
	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	baseURL := "https://" + domain
	followersURL := baseURL + "/users/" + username + "/followers"

	// Get followers count
	var count int64

	db.GetDB().Model(&models.Follow{}).Where("follow_id = ?", account.Id).Count(&count)

	if !page {
		c.JSON(200, Collection{
			Context:    []string{"https://www.w3.org/ns/activitystreams"},
			ID:         followersURL,
			Type:       "OrderedCollection",
			TotalItems: int(count),
			First:      followersURL + "?page=true",
		})

		return
	}

	// Get followers with preloaded accounts
	var follows []*models.Follow

	db.GetDB().Where("follow_id = ?", account.Id).
		Preload("Account").
		Limit(20).
		Find(&follows)

	items := make([]any, 0, len(follows))

	for _, follow := range follows {
		if follow.Account != nil {
			if follow.Account.Domain != nil && follow.Account.OriginId != "" {
				// Remote account: use their ActivityPub actor URI
				items = append(items, follow.Account.OriginId)
			} else {
				// Local account: use our local actor URL
				items = append(items, baseURL+"/users/"+follow.Account.Username)
			}
		}
	}

	c.JSON(200, OrderedCollectionPage{
		Context:      []string{"https://www.w3.org/ns/activitystreams"},
		ID:           followersURL + "?page=true",
		Type:         "OrderedCollectionPage",
		PartOf:       followersURL,
		OrderedItems: items,
	})
}

// Following handles GET /users/:username/following.
func Following(c *gin.Context) {
	username := c.Param("username")

	var account models.Account
	err := db.GetDB().Where("username = ?", username).First(&account).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"})

		return
	}

	page := c.Query("page") == "true"
	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	baseURL := "https://" + domain
	followingURL := baseURL + "/users/" + username + "/following"

	// Get following count
	var count int64

	db.GetDB().Model(&models.Follow{}).Where("account_id = ?", account.Id).Count(&count)

	if !page {
		c.JSON(200, Collection{
			Context:    []string{"https://www.w3.org/ns/activitystreams"},
			ID:         followingURL,
			Type:       "OrderedCollection",
			TotalItems: int(count),
			First:      followingURL + "?page=true",
		})

		return
	}

	var results []struct {
		FollowId uint64
	}

	db.GetDB().Model(&models.Follow{}).
		Select("follow_id").
		Where("account_id = ?", account.Id).
		Limit(20).
		Find(&results)

	// Collect all follow IDs for a single batch query (avoid N+1)
	followIDs := make([]uint64, 0, len(results))
	for _, r := range results {
		followIDs = append(followIDs, r.FollowId)
	}

	items := make([]any, 0)

	if len(followIDs) > 0 {
		var targetAccounts []models.Account
		err = db.GetDB().Where("id IN ?", followIDs).Find(&targetAccounts).Error
		if err == nil {
			for _, targetAccount := range targetAccounts {
				if targetAccount.Domain != nil && targetAccount.OriginId != "" {
					// Remote account: use their ActivityPub actor URI
					items = append(items, targetAccount.OriginId)
				} else {
					// Local account: use our local actor URL
					items = append(items, baseURL+"/users/"+targetAccount.Username)
				}
			}
		}
	}

	c.JSON(200, OrderedCollectionPage{
		Context:      []string{"https://www.w3.org/ns/activitystreams"},
		ID:           followingURL + "?page=true",
		Type:         "OrderedCollectionPage",
		PartOf:       followingURL,
		OrderedItems: items,
	})
}

// ActorProfile handles GET /users/:username
// Returns the ActivityPub actor profile.
func ActorProfile(c *gin.Context) {
	username := c.Param("username")

	var account models.Account
	err := db.GetDB().Preload("Extra").Preload("Emojis").Where("username = ?", username).First(&account).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"})

		return
	}

	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	baseURL := "https://" + domain
	actorURL := baseURL + "/users/" + username

	// Build actor profile
	actor := Actor{
		Context: []string{
			"https://www.w3.org/ns/activitystreams",
			"https://w3id.org/security/v1",
		},
		ID:                actorURL,
		Type:              "Person", //nolint:goconst
		Following:         actorURL + "/following",
		Followers:         actorURL + "/followers",
		Inbox:             actorURL + "/inbox",
		Outbox:            actorURL + "/outbox",
		PreferredUsername: account.Username,
		Name:              account.DisplayName,
		Summary:           account.Note,
		URL:               baseURL + "/@" + username,
		Endpoints: &Endpoints{
			SharedInbox: baseURL + "/inbox",
		},
	}

	if account.Avatar != "" {
		actor.Icon = &Image{
			Type: "Image", //nolint:goconst
			URL:  account.Avatar,
		}
	}

	if account.Header != "" {
		actor.Image = &Image{
			Type: "Image",
			URL:  account.Header,
		}
	}

	// Add public key if the account has one stored
	var accountKey models.AccountKey
	err = db.GetDB().Where("account_id = ?", account.Id).First(&accountKey).Error
	if err == nil {
		actor.PublicKey = &PublicKey{
			ID:           actorURL + "#main-key",
			Owner:        actorURL,
			PublicKeyPem: accountKey.PublicKey,
		}
	}

	// Bot/Service accounts use "Service" actor type
	if account.Bot {
		actor.Type = "Service" //nolint:goconst
	}

	// Add profile fields
	if account.Extra != nil {
		for _, field := range serializer.ToFields(account.Extra.Fields) {
			actor.Attachments = append(actor.Attachments, Attachment{
				Type:  "PropertyValue",
				Name:  field.Name,
				Value: field.Val,
			})
		}
	}

	c.JSON(200, actor)
}
