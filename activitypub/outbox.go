package activitypub

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// Outbox handles GET /users/:username/outbox
// Returns the user's activity stream.
func Outbox(c *gin.Context) {

	// Verify the local user exists
	var account models.Account
	err := localActorRouteQuery(db.GetDB().Preload("Extra"), c).First(&account).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"}) //nolint:goconst

		return
	}
	if !signedFetchAllowed(c, &account) {
		return
	}

	// Get pagination params
	page := c.Query("page") == "true"
	minID := c.Query("min_id")
	maxID := c.Query("max_id")
	limit := 20

	baseURL := systemsettings.GetBaseURL()
	outboxURL := account.ActorURI(baseURL) + "/outbox"

	if !page {
		// Return collection info
		var totalItems int64
		db.GetDB().Model(&models.Status{}).Where("account_id = ? AND visibility IN ? AND local_only = false AND deleted_at IS NULL AND scheduled_at IS NULL AND reblog_id IS NULL", account.Id, []int{int(models.StatusVisibilityPublic), int(models.StatusVisibilityUnlisted)}).Count(&totalItems)

		c.JSON(200, Collection{
			Context:    []string{"https://www.w3.org/ns/activitystreams"}, //nolint:goconst
			ID:         outboxURL,
			Type:       "OrderedCollection", //nolint:goconst
			TotalItems: int(totalItems),
			First:      outboxURL + "?page=true",
			Last:       outboxURL + "?page=true&min_id=0",
		})

		return
	}

	// Get statuses
	query := db.GetDB().Model(&models.Status{}).Preload("Content").Preload("Attachments").Preload("ToAccounts").Preload("Tags").Preload("Emojis").Preload("ReplyAccount").
		Where("account_id = ?", account.Id).
		Where("visibility IN ?", []int{int(models.StatusVisibilityPublic), int(models.StatusVisibilityUnlisted)}).
		Where("local_only = false AND deleted_at IS NULL AND scheduled_at IS NULL AND reblog_id IS NULL").
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

	c.Header("Content-Type", "application/activity+json")
	c.JSON(200, response)
}

// buildCreateActivity builds a Create activity for a status.
func buildCreateActivity(status *models.Status, account *models.Account, baseURL string) map[string]any {
	note := buildNoteFromStatus(status, account, baseURL)
	return map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "id": note["id"].(string) + "#create", "type": "Create", "actor": note["attributedTo"], "published": status.CreatedAt, "to": note["to"], "cc": note["cc"], "object": note}
}

// Followers handles GET /users/:username/followers.
func Followers(c *gin.Context) {

	var account models.Account
	err := localActorRouteQuery(db.GetDB(), c).First(&account).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"})

		return
	}
	if !signedFetchAllowed(c, &account) {
		return
	}

	page := c.Query("page") == "true"
	baseURL := systemsettings.GetBaseURL()
	followersURL := account.ActorURI(baseURL) + "/followers"

	// Get followers count
	var count int64

	db.GetDB().Model(&models.Follow{}).Where("follow_id = ?", account.Id).Count(&count)

	// A non-public list exposes only its size, as Mastodon does for
	// hide_collections: no first page, and page requests are refused.
	public := account.CanFollowersPublic()
	if !page || !public {
		if page {
			c.JSON(403, gin.H{"error": "collection is not public"})
			return
		}
		collection := Collection{
			Context:    []string{"https://www.w3.org/ns/activitystreams"},
			ID:         followersURL,
			Type:       "OrderedCollection",
			TotalItems: int(count),
		}
		if public {
			collection.First = followersURL + "?page=true"
		}
		c.JSON(200, collection)

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
				items = append(items, follow.Account.ActorURI(baseURL))
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

	var account models.Account
	err := localActorRouteQuery(db.GetDB(), c).First(&account).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"})

		return
	}
	if !signedFetchAllowed(c, &account) {
		return
	}

	page := c.Query("page") == "true"
	baseURL := systemsettings.GetBaseURL()
	followingURL := account.ActorURI(baseURL) + "/following"

	// Get following count
	var count int64

	db.GetDB().Model(&models.Follow{}).Where("account_id = ?", account.Id).Count(&count)

	// A non-public list exposes only its size, as Mastodon does for
	// hide_collections: no first page, and page requests are refused.
	public := account.CanFollowingPublic()
	if !page || !public {
		if page {
			c.JSON(403, gin.H{"error": "collection is not public"})
			return
		}
		collection := Collection{
			Context:    []string{"https://www.w3.org/ns/activitystreams"},
			ID:         followingURL,
			Type:       "OrderedCollection",
			TotalItems: int(count),
		}
		if public {
			collection.First = followingURL + "?page=true"
		}
		c.JSON(200, collection)

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
					items = append(items, targetAccount.ActorURI(baseURL))
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

	var account models.Account
	err := localActorRouteQuery(db.GetDB().Preload("Extra").Preload("Emojis"), c).First(&account).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"})

		return
	}

	if account.DeletionRequestedAt != nil {
		c.JSON(410, gin.H{"@context": "https://www.w3.org/ns/activitystreams", "id": account.ActorURI(systemsettings.GetBaseURL()), "type": "Tombstone"})
		return
	}
	actor, err := buildActorProfile(&account, localAccountEmojiTags(&account, systemsettings.GetBaseURL()))
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to load actor signing key"})
		return
	}

	c.Header("Content-Type", "application/activity+json")
	c.JSON(200, actor)
}
