package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"strconv"
)

// StatusesRequest represents the request for getting account statuses.
type StatusesRequest struct {
	Limit          int    `binding:"omitempty,min=1,max=80" form:"limit"`
	MaxId          string `form:"max_id"`
	SinceId        string `form:"since_id"`
	MinId          string `form:"min_id"`
	OnlyMedia      bool   `form:"only_media"`
	ExcludeReplies bool   `form:"exclude_replies"`
	Pinned         bool   `form:"pinned"`
}

// AccountStatuses handles GET /api/v1/accounts/:id/statuses.
func AccountStatuses(c *gin.Context) {
	var req StatusesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	if req.Limit == 0 {
		req.Limit = 20
	}

	// Get account ID from URL
	accountId := c.Param("id")
	if accountId == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"}) //nolint:goconst

		return
	}
	authorID, err := strconv.ParseUint(accountId, 10, 64)
	if err != nil || authorID == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})
		return
	}

	var exists int64
	if err := db.GetDB().WithContext(c.Request.Context()).Model(&models.Account{}).Where("id = ?", authorID).Limit(1).Count(&exists).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "AccountStatuses"))

		return
	}
	if exists == 0 {
		c.AbortWithStatusJSON(404, gin.H{"error": "Record not found"})

		return
	}

	// Get current account if authenticated
	var currentAccount *models.Account
	if c.GetBool("authenticated") {
		currentAccount = c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	}

	// Query statuses
	query := db.GetDB().WithContext(c.Request.Context()).Model(&models.Status{}).Where("statuses.account_id = ?", authorID)

	// Exclude scheduled statuses
	query = query.Where("scheduled_at IS NULL AND deleted_at IS NULL")
	// Restrict the audience before LIMIT and association loading. Filtering a
	// loaded page one status at a time both underfills pages and repeats the
	// same follow/block lookups. Anonymous callers must never load private rows.
	if currentAccount == nil {
		query = query.Where("visibility IN ?", []int{models.StatusVisibilityPublic, models.StatusVisibilityUnlisted})
	} else if currentAccount.Id != authorID {
		query = query.Where(`(visibility IN ? OR
			(visibility = ? AND EXISTS (SELECT 1 FROM follows WHERE follows.account_id = ? AND follows.follow_id = ? AND follows.status = ?)) OR
			(visibility IN ? AND EXISTS (SELECT 1 FROM status_to_accounts WHERE status_to_accounts.status_id = statuses.id AND status_to_accounts.account_id = ?)))`,
			[]int{models.StatusVisibilityPublic, models.StatusVisibilityUnlisted}, models.StatusVisibilityPrivate,
			currentAccount.Id, authorID, models.FollowStatusFollowed,
			[]int{models.StatusVisibilityPrivate, models.StatusVisibilityDirect}, currentAccount.Id)
	}

	// Pagination
	if req.MaxId != "" {
		if maxId, err := strconv.ParseUint(req.MaxId, 10, 64); err == nil {
			query = query.Where("id < ?", maxId)
		}
	}

	if req.MinId != "" {
		if minId, err := strconv.ParseUint(req.MinId, 10, 64); err == nil {
			query = query.Where("id > ?", minId)
		}
	}

	if req.SinceId != "" {
		if sinceId, err := strconv.ParseUint(req.SinceId, 10, 64); err == nil {
			query = query.Where("id > ?", sinceId)
		}
	}

	// Only media filter
	if req.OnlyMedia {
		query = query.Where("id IN (SELECT status_id FROM media_attachments WHERE status_id IS NOT NULL)")
	}

	// Exclude replies
	if req.ExcludeReplies {
		query = query.Where("in_reply_to_id IS NULL")
	}

	// Pinned only
	if req.Pinned {
		query = query.Where("id IN (SELECT status_id FROM pin_statuses WHERE account_id = ?)", accountId)
	}

	// Load all related data
	query = models.StatusLoadAllQuery(query)

	// Cursor comparisons use the same ordering key, including imported posts
	// whose published timestamp does not match their local insertion order.
	query = query.Order("statuses.id DESC")

	serializedStatuses := make([]*serializer.Status, 0, req.Limit)
	var cursor uint64
	authorChecked := false
	for len(serializedStatuses) < req.Limit {
		remaining := req.Limit - len(serializedStatuses)
		pageQuery := query.Limit(remaining)
		if cursor > 0 {
			pageQuery = pageQuery.Where("statuses.id < ?", cursor)
		}
		statuses := make(models.Statuses, 0, remaining)
		if err := pageQuery.Find(&statuses).Error; err != nil {
			_ = c.Error(errors.Wrap(err, "get statuses error"))
			return
		}
		if len(statuses) == 0 {
			break
		}
		// A hidden original can remove a public reblog. Continue from the last
		// candidate so a filtered page cannot conceal older visible posts.
		page, err := serializer.ToStatusesContext(c.Request.Context(), statuses, currentAccount)
		if err != nil {
			_ = c.Error(errors.Wrap(err, "serialize statuses error"))
			return
		}
		serializedStatuses = append(serializedStatuses, page...)
		if len(statuses) < remaining || len(serializedStatuses) == req.Limit {
			break
		}
		if !authorChecked {
			// A blocked/banned root author has no eligible history. Check once
			// on the slow path instead of scanning every post they ever wrote.
			roots, err := models.FilterVisibleStatuses(db.GetDB().WithContext(c.Request.Context()), currentAccount, models.Statuses{
				&models.Status{AccountId: authorID, Account: statuses[0].Account, Visibility: models.StatusVisibilityPublic},
			})
			if err != nil {
				_ = c.Error(errors.Wrap(err, "check account visibility error"))
				return
			}
			if len(roots) == 0 {
				break
			}
			authorChecked = true
		}
		cursor = statuses[len(statuses)-1].Id
	}

	common.SetListLinks(c, serializedStatuses, func(s *serializer.Status) string { return s.Id })
	c.JSON(200, serializedStatuses)
}
