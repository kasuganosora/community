package favourite

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"strconv"
)

// ListRequest represents the request for listing favourites/bookmarks
type ListRequest struct {
	Limit  int    `form:"limit" binding:"min=1,max=80"`
	MaxId  string `form:"max_id"`
	MinId  string `form:"min_id"`
	SinceId string `form:"since_id"`
}

// Favourites handles GET /api/v1/favourites
func Favourites(c *gin.Context) {
	var req ListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})
		return
	}

	if req.Limit == 0 {
		req.Limit = 20
	}

	// Get current account
	currentAccount := c.MustGet("account").(*models.Account)

	// Query favourite statuses
	query := db.GetDB().Model(&models.FavoriteStatus{}).
		Joins("LEFT JOIN statuses ON statuses.id = favorite_statuses.status_id").
		Where("favorite_statuses.account_id = ?", currentAccount.Id).
		Where("statuses.scheduled_at IS NULL").
		Order("favorite_statuses.id DESC")

	// Pagination
	if req.MaxId != "" {
		if maxId, err := strconv.ParseUint(req.MaxId, 10, 64); err == nil {
			query = query.Where("favorite_statuses.id < ?", maxId)
		}
	}
	if req.MinId != "" {
		if minId, err := strconv.ParseUint(req.MinId, 10, 64); err == nil {
			query = query.Where("favorite_statuses.id > ?", minId)
		}
	}
	if req.SinceId != "" {
		if sinceId, err := strconv.ParseUint(req.SinceId, 10, 64); err == nil {
			query = query.Where("favorite_statuses.id > ?", sinceId)
		}
	}

	favoriteStatuses := make([]*models.FavoriteStatus, 0)
	if err := query.Limit(req.Limit).Find(&favoriteStatuses).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get favourites error"))
		return
	}

	// Get status IDs
	statusIds := make([]uint64, 0, len(favoriteStatuses))
	for _, fs := range favoriteStatuses {
		statusIds = append(statusIds, fs.StatusId)
	}

	if len(statusIds) == 0 {
		c.JSON(200, []*serializer.Status{})
		return
	}

	// Query statuses with all related data
	statuses := make(models.Statuses, 0)
	query = db.GetDB().Model(&models.Status{}).
		Where("id IN ?", statusIds)
	query = models.StatusLoadAllQuery(query)

	if err := query.Find(&statuses).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get statuses error"))
		return
	}

	// Convert to serializer
	serializedStatuses, err := serializer.ToStatuses(statuses, currentAccount)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "serialize statuses error"))
		return
	}

	c.JSON(200, serializedStatuses)
}

// Bookmarks handles GET /api/v1/bookmarks
func Bookmarks(c *gin.Context) {
	var req ListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})
		return
	}

	if req.Limit == 0 {
		req.Limit = 20
	}

	// Get current account
	currentAccount := c.MustGet("account").(*models.Account)

	// Query bookmarked statuses
	query := db.GetDB().Model(&models.BookmarkStatus{}).
		Joins("LEFT JOIN statuses ON statuses.id = bookmark_statuses.status_id").
		Where("bookmark_statuses.account_id = ?", currentAccount.Id).
		Where("statuses.scheduled_at IS NULL").
		Order("bookmark_statuses.id DESC")

	// Pagination
	if req.MaxId != "" {
		if maxId, err := strconv.ParseUint(req.MaxId, 10, 64); err == nil {
			query = query.Where("bookmark_statuses.id < ?", maxId)
		}
	}
	if req.MinId != "" {
		if minId, err := strconv.ParseUint(req.MinId, 10, 64); err == nil {
			query = query.Where("bookmark_statuses.id > ?", minId)
		}
	}
	if req.SinceId != "" {
		if sinceId, err := strconv.ParseUint(req.SinceId, 10, 64); err == nil {
			query = query.Where("bookmark_statuses.id > ?", sinceId)
		}
	}

	bookmarkStatuses := make([]*models.BookmarkStatus, 0)
	if err := query.Limit(req.Limit).Find(&bookmarkStatuses).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get bookmarks error"))
		return
	}

	// Get status IDs
	statusIds := make([]uint64, 0, len(bookmarkStatuses))
	for _, bs := range bookmarkStatuses {
		statusIds = append(statusIds, bs.StatusId)
	}

	if len(statusIds) == 0 {
		c.JSON(200, []*serializer.Status{})
		return
	}

	// Query statuses with all related data
	statuses := make(models.Statuses, 0)
	query = db.GetDB().Model(&models.Status{}).
		Where("id IN ?", statusIds)
	query = models.StatusLoadAllQuery(query)

	if err := query.Find(&statuses).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get statuses error"))
		return
	}

	// Convert to serializer
	serializedStatuses, err := serializer.ToStatuses(statuses, currentAccount)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "serialize statuses error"))
		return
	}

	c.JSON(200, serializedStatuses)
}
