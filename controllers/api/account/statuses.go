package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"strconv"
)

// StatusesRequest represents the request for getting account statuses.
type StatusesRequest struct {
	Limit          int    `binding:"min=1,max=80" form:"limit"`
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

	// Get current account if authenticated
	var currentAccount *models.Account
	if c.GetBool("authenticated") {
		currentAccount = c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	}

	// Query statuses
	query := db.GetDB().Model(&models.Status{}).Where("account_id = ?", accountId)

	// Exclude scheduled statuses
	query = query.Where("scheduled_at IS NULL")

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
		query = query.Where("id IN (SELECT status_id FROM status_media_attachments)")
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

	// Order by created_at desc
	query = query.Order("created_at DESC")

	statuses := make(models.Statuses, 0)
	if err := query.Limit(req.Limit).Find(&statuses).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get statuses error"))

		return
	}

	// Filter by visibility
	if currentAccount != nil {
		filteredStatuses := make(models.Statuses, 0)

		for _, status := range statuses {
			canView, err := status.CanViewEx(db.GetDB(), currentAccount)
			if err != nil || !canView {
				continue
			}

			filteredStatuses = append(filteredStatuses, status)
		}

		statuses = filteredStatuses
	}

	// Serialize
	serializedStatuses, err := serializer.ToStatuses(statuses, currentAccount)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "serialize statuses error"))

		return
	}

	c.JSON(200, serializedStatuses)
}
