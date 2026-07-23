package search

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/search"
	"github.com/kasuganosora/community/serializer"
	"strconv"
	"strings"
)

// SearchRequest represents the search query parameters for Mastodon API v2.
type SearchRequest struct {
	Q         string `binding:"required,min=1,max=500" form:"q"`
	Type      string `form:"type"`       // accounts, statuses, hashtags
	Limit     int    `form:"limit"`      // max 40, default 20
	Offset    int    `form:"offset"`     // pagination offset
	Following bool   `form:"following"`  // only search following accounts
	AccountId string `form:"account_id"` // filter by account
	MaxId     string `form:"max_id"`     // pagination
	MinId     string `form:"min_id"`     // pagination
	Resolve   bool   `form:"resolve"`    // WebFinger resolve (non-local accounts)
}

// SearchResult represents the response for search API.
type SearchResult struct {
	Accounts []*serializer.Account `json:"accounts"`
	Statuses []*serializer.Status  `json:"statuses"`
	Hashtags []*serializer.Tag     `json:"hashtags"`
}

// Search handles GET /api/v2/search.
func Search(c *gin.Context) {
	var req SearchRequest
	err := c.ShouldBindQuery(&req)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{
			"error": "Invalid query parameters: " + err.Error(),
		})

		return
	}

	// Set defaults
	if req.Limit == 0 {
		req.Limit = 20
	} else if req.Limit > 40 {
		req.Limit = 40
	}

	// Clean up query
	req.Q = strings.TrimSpace(req.Q)
	if req.Q == "" {
		c.AbortWithStatusJSON(422, gin.H{
			"error": "Query cannot be empty",
		})

		return
	}

	result := &SearchResult{
		Accounts: make([]*serializer.Account, 0),
		Statuses: make([]*serializer.Status, 0),
		Hashtags: make([]*serializer.Tag, 0),
	}

	// Get current account if authenticated
	var currentAccount *models.Account
	if c.GetBool("authenticated") {
		currentAccount = c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	}

	// Search based on type
	searchType := strings.ToLower(req.Type)
	if searchType == "" {
		searchType = "all"
	}

	switch searchType {
	case "accounts":
		result.Accounts = searchAccounts(req, currentAccount)
	case "statuses":
		result.Statuses = searchStatuses(req, currentAccount)
	case "hashtags":
		result.Hashtags = searchHashtags(req)
	default:
		// Search all types
		result.Accounts = searchAccounts(req, currentAccount)
		result.Statuses = searchStatuses(req, currentAccount)
		result.Hashtags = searchHashtags(req)
	}

	c.JSON(200, result)
}

// searchAccounts searches for accounts by username or display name.
func searchAccounts(req SearchRequest, currentAccount *models.Account) []*serializer.Account {
	accounts := make([]*models.Account, 0)

	// Try Meilisearch first
	meiliClient := search.GetMeilisearchClient()
	if meiliClient.IsEnabled() {
		// Parse advanced search query
		keyword, _ := search.ParseSearchQuery(req.Q)
		if keyword == "" {
			keyword = req.Q
		}

		// Get following IDs if needed
		var followingIDs []string
		if req.Following && currentAccount != nil {
			followingIDs = getFollowingIDs(currentAccount.Id)
		}

		// Search with Meilisearch
		ids, err := meiliClient.SearchAccounts(keyword, int64(req.Limit), int64(req.Offset), followingIDs)
		if err == nil && len(ids) > 0 {
			// Fetch full account data from database
			err := db.GetDB().Model(&models.Account{}).
				Preload("Emojis").Preload("Extra").
				Where("id IN (?)", ids).
				Find(&accounts).Error
			if err == nil {
				// Sort by Meilisearch result order
				accountMap := make(map[uint64]*models.Account)
				for _, acc := range accounts {
					accountMap[acc.Id] = acc
				}

				sortedAccounts := make([]*models.Account, 0, len(ids))

				for _, idStr := range ids {
					id := search.StringToUint64(idStr)
					if acc, ok := accountMap[id]; ok {
						sortedAccounts = append(sortedAccounts, acc)
					}
				}

				return serializer.ToAccounts(sortedAccounts)
			}
		}
	}

	// Fallback to database search
	query := db.GetDB().Model(&models.Account{}).Preload("Emojis").Preload("Extra")

	keyword := strings.Trim(req.Q, " \t\n@")

	// Parse username@domain format
	var usernameKeyword, domain string

	parts := strings.Split(keyword, "@")
	if len(parts) == 1 {
		usernameKeyword = parts[0]
	} else {
		usernameKeyword = parts[0]
		domain = parts[1]
	}

	if usernameKeyword == "" {
		return []*serializer.Account{}
	}

	// Search by username or display name (fuzzy matching)
	query = query.Where("username LIKE ? OR display_name LIKE ?",
		"%"+usernameKeyword+"%", "%"+usernameKeyword+"%")

	// Filter by domain if specified
	if domain != "" {
		query = query.Where("domain = ?", domain)
	}

	// Filter by following if requested
	if req.Following && currentAccount != nil {
		query = query.Where("id IN (?)",
			db.GetDB().Model(&models.Follow{}).
				Select("follow_id").
				Where("account_id = ?", currentAccount.Id))
	}

	// Filter by account_id if specified
	if req.AccountId != "" {
		query = query.Where("id = ?", req.AccountId)
	}

	// Order by relevance (exact match first, then partial match)
	// Use parameterized query to prevent SQL injection
	query = query.Order(db.GetDB().Raw(
		"CASE WHEN username = ? THEN 0 WHEN username LIKE ? THEN 1 ELSE 2 END",
		usernameKeyword, usernameKeyword+"%",
	))

	err := query.Limit(req.Limit).Offset(req.Offset).Find(&accounts).Error
	if err != nil {
		return []*serializer.Account{}
	}

	return serializer.ToAccounts(accounts)
}

// getFollowingIDs 获取关注用户ID列表.
func getFollowingIDs(accountId uint64) []string {
	var followIDs []uint64
	err := db.GetDB().Model(&models.Follow{}).
		Where("account_id = ?", accountId).
		Pluck("follow_id", &followIDs).Error
	if err != nil {
		return nil
	}

	ids := make([]string, len(followIDs))
	for i, id := range followIDs {
		ids[i] = strconv.FormatUint(id, 10)
	}

	return ids
}

// searchStatuses searches for statuses by content.
func searchStatuses(req SearchRequest, currentAccount *models.Account) []*serializer.Status {
	statuses := make([]*models.Status, 0)

	// Only authenticated users can search statuses
	if currentAccount == nil {
		return []*serializer.Status{}
	}

	// Try Meilisearch first
	meiliClient := search.GetMeilisearchClient()
	if meiliClient.IsEnabled() {
		// Parse advanced search query
		keyword, filters := search.ParseSearchQuery(req.Q)
		if keyword == "" {
			keyword = req.Q
		}

		// Build visibility filter
		visibilities := []string{strconv.Itoa(int(models.StatusVisibilityPublic))}
		if currentAccount != nil {
			visibilities = append(visibilities,
				strconv.Itoa(int(models.StatusVisibilityUnlisted)),
				strconv.Itoa(int(models.StatusVisibilityPrivate)))
		}

		// Handle from: filter
		accountID := req.AccountId

		if fromUser, ok := filters["from"]; ok && fromUser != "" {
			// Find account by username
			var acc models.Account
			err := db.GetDB().Model(&models.Account{}).
				Where("username = ?", fromUser).
				First(&acc).Error
			if err == nil {
				accountID = strconv.FormatUint(acc.Id, 10)
			}
		}

		// Search with Meilisearch
		ids, err := meiliClient.SearchStatuses(keyword, int64(req.Limit), int64(req.Offset), accountID, visibilities)
		if err == nil && len(ids) > 0 {
			// Fetch full status data from database
			query := db.GetDB().Model(&models.Status{}).
				Where("id IN (?)", ids)
			query = models.StatusLoadAllQuery(query)

			err := query.Find(&statuses).Error
			if err == nil {
				// Sort by Meilisearch result order
				statusMap := make(map[uint64]*models.Status)
				for _, s := range statuses {
					statusMap[s.Id] = s
				}

				sortedStatuses := make([]*models.Status, 0, len(ids))

				for _, idStr := range ids {
					id := search.StringToUint64(idStr)
					if s, ok := statusMap[id]; ok {
						sortedStatuses = append(sortedStatuses, s)
					}
				}

				statuses = sortedStatuses
			}

			// Filter by view permission
			filteredStatuses := make(models.Statuses, 0)

			for _, status := range statuses {
				canView, err := status.CanViewEx(db.GetDB(), currentAccount)
				if err != nil || !canView {
					continue
				}

				filteredStatuses = append(filteredStatuses, status)
			}

			serializedStatuses, _ := serializer.ToStatuses(filteredStatuses, currentAccount)

			return serializedStatuses
		}
	}

	// Fallback to database search
	query := db.GetDB().Model(&models.Status{}).
		Joins("LEFT JOIN status_contents ON statuses.id = status_contents.status_id").
		Where("status_contents.content LIKE ? OR status_contents.spoiler_text LIKE ?",
			"%"+req.Q+"%", "%"+req.Q+"%")

	// Filter by public visibility only for non-own statuses
	query = query.Where("visibility = ? OR account_id = ?",
		models.StatusVisibilityPublic, currentAccount.Id)

	// Filter by account_id if specified
	if req.AccountId != "" {
		query = query.Where("account_id = ?", req.AccountId)
	}

	// Exclude scheduled statuses
	query = query.Where("scheduled_at IS NULL")

	// Load all related data
	query = models.StatusLoadAllQuery(query)

	// Order by created_at desc
	query = query.Order("statuses.created_at DESC")

	err := query.Limit(req.Limit).Offset(req.Offset).Find(&statuses).Error
	if err != nil {
		return []*serializer.Status{}
	}

	// Filter by view permission
	filteredStatuses := make(models.Statuses, 0)

	for _, status := range statuses {
		canView, err := status.CanViewEx(db.GetDB(), currentAccount)
		if err != nil || !canView {
			continue
		}

		filteredStatuses = append(filteredStatuses, status)
	}

	// Convert to serializer
	serializedStatuses, _ := serializer.ToStatuses(filteredStatuses, currentAccount)

	return serializedStatuses
}

// searchHashtags searches for hashtags by name.
func searchHashtags(req SearchRequest) []*serializer.Tag {
	tags := make([]*models.Tag, 0)

	keyword := strings.TrimPrefix(req.Q, "#")

	// Try Meilisearch first
	meiliClient := search.GetMeilisearchClient()
	if meiliClient.IsEnabled() {
		// Search with Meilisearch
		names, err := meiliClient.SearchHashtags(keyword, int64(req.Limit), int64(req.Offset))
		if err == nil && len(names) > 0 {
			// Fetch full tag data from database
			err := db.GetDB().Model(&models.Tag{}).
				Where("name IN (?)", names).
				Find(&tags).Error
			if err == nil {
				// Sort by Meilisearch result order
				tagMap := make(map[string]*models.Tag)
				for _, t := range tags {
					tagMap[t.Name] = t
				}

				sortedTags := make([]*models.Tag, 0, len(names))

				for _, name := range names {
					if t, ok := tagMap[name]; ok {
						sortedTags = append(sortedTags, t)
					}
				}

				return serializer.ToTags(sortedTags)
			}
		}
	}

	// Fallback to database search
	query := db.GetDB().Model(&models.Tag{}).
		Where("name LIKE ?", "#"+keyword+"%").
		Order("status_count DESC, name ASC")

	err := query.Limit(req.Limit).Offset(req.Offset).Find(&tags).Error
	if err != nil {
		return []*serializer.Tag{}
	}

	return serializer.ToTags(tags)
}
