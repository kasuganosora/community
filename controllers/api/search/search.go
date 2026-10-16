package search

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/kasuganosora/community/search"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm/clause"
	"net/url"
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

	wantAccounts := searchType == "accounts" || searchType == "all"
	wantStatuses := searchType == "statuses" || searchType == "all"

	// Pasted URLs (profile pages, actor ids, note URLs) are looked up exactly,
	// never full-text searched. Like Mastodon, only signed-in users may make
	// the server fetch unknown remote objects.
	if activitypub.IsURLQuery(req.Q) {
		if req.Offset == 0 {
			accounts, statuses := searchByURL(c, req, currentAccount)
			if wantAccounts {
				result.Accounts = accounts
			}
			if wantStatuses {
				result.Statuses = statuses
			}
		}
		c.JSON(200, result)
		return
	}

	// Full account addresses (@user@domain) match username AND domain. The
	// full-text status and hashtag searches are skipped for them, as in
	// Mastodon, because "@" terms are not meaningful there.
	if acct, ok := activitypub.ParseAcctQuery(req.Q); ok {
		if wantAccounts {
			if req.Resolve && currentAccount != nil && acct.Domain != "" && req.Offset == 0 {
				if _, err := activitypub.ResolveSearchAccount(c.Request.Context(), req.Q); err != nil {
					log.Warn("search: resolving %q failed: %v", req.Q, err)
				}
			}
			result.Accounts = searchAccountsByAcct(req, acct, currentAccount)
		}
		c.JSON(200, result)
		return
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

// searchAccountsByAcct returns accounts whose username starts with
// acct.Username on exactly acct.Domain (local when empty), exact names first.
// It deliberately bypasses Meilisearch, whose fuzzy matching ignores the
// domain and returned a local "luna" for "@luna@maid.lat".
func searchAccountsByAcct(req SearchRequest, acct activitypub.AcctQuery, currentAccount *models.Account) []*serializer.Account {
	accounts := make([]*models.Account, 0)
	query := models.WhereAcct(db.GetDB().Model(&models.Account{}).Preload("Emojis").Preload("Extra"), acct.Username, acct.Domain, true)
	if req.Following && currentAccount != nil {
		query = query.Where("accounts.id IN (?)",
			db.GetDB().Model(&models.Follow{}).Select("follow_id").Where("account_id = ?", currentAccount.Id))
	}
	if req.AccountId != "" {
		query = query.Where("accounts.id = ?", req.AccountId)
	}
	query = models.OrderAcctExactFirst(query, acct.Username)
	if err := query.Limit(req.Limit).Offset(req.Offset).Find(&accounts).Error; err != nil {
		return []*serializer.Account{}
	}
	kept, err := models.FilterAccountsByDomainPolicy(db.GetDB(), currentAccount, accounts)
	if err != nil {
		return []*serializer.Account{}
	}
	return serializer.ToAccounts(kept)
}

// searchByURL finds the account or status a URL names in the local cache and,
// for signed-in users with resolve=true, fetches it from the remote server.
func searchByURL(c *gin.Context, req SearchRequest, currentAccount *models.Account) ([]*serializer.Account, []*serializer.Status) {
	account, status := lookupCachedURL(req.Q)
	if account == nil && status == nil && req.Resolve && currentAccount != nil {
		var err error
		account, status, err = activitypub.ResolveSearchURL(c.Request.Context(), req.Q)
		if err != nil {
			log.Warn("search: resolving URL %q failed: %v", req.Q, err)
		}
	}

	accounts := []*serializer.Account{}
	statuses := []*serializer.Status{}
	if account != nil {
		var loaded models.Account
		if err := db.GetDB().Preload("Emojis").Preload("Extra").First(&loaded, account.Id).Error; err == nil {
			if kept, err := models.FilterAccountsByDomainPolicy(db.GetDB(), currentAccount, []*models.Account{&loaded}); err == nil {
				accounts = serializer.ToAccounts(kept)
			}
		}
	}
	if status != nil {
		var loaded models.Status
		if err := models.StatusLoadAllQuery(db.GetDB().Model(&models.Status{})).Where("statuses.id = ?", status.Id).First(&loaded).Error; err == nil {
			if ok, err := loaded.CanViewEx(db.GetDB(), currentAccount); err == nil && ok {
				if serialized, err := serializer.ToStatuses(models.Statuses{&loaded}, currentAccount); err == nil {
					statuses = serialized
				}
			}
		}
	}
	return accounts, statuses
}

// lookupCachedURL matches a URL against stored accounts and statuses: remote
// actor ids, note ids/URLs, local profile/status URLs and the common
// https://host/@user profile URL form.
func lookupCachedURL(rawURL string) (*models.Account, *models.Status) {
	conn := db.GetDB()
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return nil, nil
	}
	uri := strings.TrimSpace(rawURL)
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")

	if activitypub.IsLocalDomain(parsed.Host) {
		// /@user, /users/user, /@user/123, /users/user/statuses/123
		var username, statusID string
		switch {
		case len(segments) >= 1 && strings.HasPrefix(segments[0], "@") && !strings.Contains(segments[0][1:], "@"):
			username = segments[0][1:]
			if len(segments) == 2 {
				statusID = segments[1]
			}
		case len(segments) >= 2 && segments[0] == "users":
			username = segments[1]
			if len(segments) == 4 && segments[2] == "statuses" {
				statusID = segments[3]
			}
		}
		if statusID != "" {
			if id, err := strconv.ParseUint(statusID, 10, 64); err == nil {
				var status models.Status
				if conn.Where("id = ? AND domain IS NULL", id).First(&status).Error == nil {
					return nil, &status
				}
			}
			return nil, nil
		}
		if username != "" {
			var account models.Account
			if models.WhereAcct(conn.Model(&models.Account{}), username, "", false).First(&account).Error == nil {
				return &account, nil
			}
		}
		return nil, nil
	}

	var status models.Status
	if conn.Where("origin_uri = ? OR origin_url = ?", uri, uri).First(&status).Error == nil {
		return nil, &status
	}
	var account models.Account
	if conn.Where("origin_id = ? AND domain IS NOT NULL", uri).First(&account).Error == nil {
		return &account, nil
	}
	// https://host/@user is the profile page of user@host on Mastodon and
	// Misskey; the stored actor id is a different URL.
	if len(segments) == 1 && strings.HasPrefix(segments[0], "@") && len(segments[0]) > 1 {
		name := segments[0][1:]
		domain := parsed.Host
		if user, host, ok := strings.Cut(name, "@"); ok {
			name, domain = user, host
		}
		if !activitypub.IsLocalDomain(domain) {
			if models.WhereAcct(conn.Model(&models.Account{}), name, domain, false).First(&account).Error == nil {
				return &account, nil
			}
		}
	}
	return nil, nil
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
		if err != nil {
			meiliClient.NoteFailure()
		} else if len(ids) == 0 {
			// A successful empty answer is final (e.g. offset past the last
			// hit); falling back would run an unindexed ILIKE scan.
			return []*serializer.Account{}
		} else {
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

				kept, keepErr := models.FilterAccountsByDomainPolicy(db.GetDB(), currentAccount, sortedAccounts)
				if keepErr != nil {
					return []*serializer.Account{}
				}
				return serializer.ToAccounts(kept)
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

	escapedUser := escapeLike(usernameKeyword)
	like, esc := dialect.ILike(query), dialect.LikeEscape(query)
	query = query.Where("(username "+like+" ? "+esc+" OR display_name "+like+" ? "+esc+")",
		"%"+escapedUser+"%", "%"+escapedUser+"%")

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
	query = query.Order(clause.OrderBy{Expression: clause.Expr{
		SQL:                "CASE WHEN LOWER(username) = ? THEN 0 WHEN username " + like + " ? " + esc + " THEN 1 ELSE 2 END, id ASC",
		Vars:               []any{strings.ToLower(usernameKeyword), escapeLike(usernameKeyword) + "%"},
		WithoutParentheses: true,
	}})

	err := query.Limit(req.Limit).Offset(req.Offset).Find(&accounts).Error
	if err != nil {
		return []*serializer.Account{}
	}

	kept, keepErr := models.FilterAccountsByDomainPolicy(db.GetDB(), currentAccount, accounts)
	if keepErr != nil {
		return []*serializer.Account{}
	}
	return serializer.ToAccounts(kept)
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
		if keyword == "" && len(filters) == 0 {
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
		if err != nil {
			meiliClient.NoteFailure()
		} else if len(ids) == 0 {
			// A successful empty answer is final (e.g. offset past the last
			// hit); falling back would run an unindexed ILIKE scan.
			return []*serializer.Status{}
		} else {
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

	// Fallback to database search. Operators such as from: still apply when
	// Meilisearch is down; unknown operators are dropped instead of being
	// searched as literal text.
	text, filters := search.ParseSearchQuery(req.Q)
	if text == "" && filters["from"] == "" && req.AccountId == "" {
		return []*serializer.Status{}
	}
	query := db.GetDB().Model(&models.Status{}).
		Joins("LEFT JOIN status_contents ON statuses.id = status_contents.status_id")
	if text != "" {
		pattern := "%" + escapeLike(text) + "%"
		like, esc := dialect.ILike(query), dialect.LikeEscape(query)
		query = query.Where("(status_contents.content "+like+" ? "+esc+" OR status_contents.spoiler_text "+like+" ? "+esc+")",
			pattern, pattern)
	}

	// Filter by public visibility only for non-own statuses
	query = query.Where("visibility = ? OR account_id = ?",
		models.StatusVisibilityPublic, currentAccount.Id)

	// Filter by account_id if specified
	if req.AccountId != "" {
		query = query.Where("account_id = ?", req.AccountId)
	}
	if fromUser := strings.TrimPrefix(filters["from"], "@"); fromUser != "" {
		query = query.Where("statuses.account_id IN (?)",
			db.GetDB().Model(&models.Account{}).Select("id").Where("username = ?", fromUser))
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
		if err != nil {
			meiliClient.NoteFailure()
		} else if len(names) == 0 {
			// A successful empty answer is final (e.g. offset past the last
			// hit); falling back would run an unindexed ILIKE scan.
			return []*serializer.Tag{}
		} else {
			// Fetch full tag data from database (case-insensitive key;
			// stale documents of merged case variants collapse into one).
			tagMap, err := models.FindTagsByNames(db.GetDB(), names)
			if err == nil {
				// Sort by Meilisearch result order
				sortedTags := make([]*models.Tag, 0, len(names))
				seen := make(map[uint64]bool, len(names))

				for _, name := range names {
					if t, ok := tagMap[models.TagKey(name)]; ok && !seen[t.Id] {
						seen[t.Id] = true
						sortedTags = append(sortedTags, t)
					}
				}

				return serializer.ToTags(withoutHiddenTags(sortedTags))
			}
		}
	}

	// Fallback to database search
	// name_key is the lowercased name, so a plain prefix LIKE on the
	// lowercased keyword is case-insensitive on every database.
	escaped := escapeLike(models.TagKey(keyword))
	conn := db.GetDB()
	query := conn.Model(&models.Tag{}).
		Where("name_key LIKE ? "+dialect.LikeEscape(conn), escaped+"%").
		Order("status_count DESC, name_key ASC")

	err := query.Limit(req.Limit).Offset(req.Offset).Find(&tags).Error
	if err != nil {
		return []*serializer.Tag{}
	}

	return serializer.ToTags(withoutHiddenTags(tags))
}

// withoutHiddenTags drops the instance's hidden tags from suggestions.
func withoutHiddenTags(tags []*models.Tag) []*models.Tag {
	snap := moderation.Current()
	out := tags[:0]
	for _, t := range tags {
		if t != nil && !snap.IsHiddenTag(t.Name) {
			out = append(out, t)
		}
	}
	return out
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}
