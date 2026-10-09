package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
	"strconv"
)

type NotificationTimelineRequest struct {
	ExcludeTypes []string `form:"exclude_types[]"`
	Types        []string `form:"types[]"`
	MaxId        uint64   `form:"max_id"`
	MinId        uint64   `form:"min_id"`
	SinceId      uint64   `form:"since_id"`
	Limit        int      `form:"limit"`
	AccountId    string   `form:"account_id"`
}

func NotificationTimeline(c *gin.Context) {
	req := NotificationTimelineRequest{}
	err := c.ShouldBindQuery(&req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": err}) //nolint:goconst

	account := controllers.MustGetAccount(c)

	query := db.GetDB().Model(&models.Notification{}).Where("account_id = ?", account.Id)

	if len(req.ExcludeTypes) > 0 {
		typeIds := make([]int, 0, len(req.ExcludeTypes))
		for _, t := range req.ExcludeTypes {
			if id := models.NotificationTypeFromString(t); id >= 0 {
				typeIds = append(typeIds, id)
			}
		}
		if len(typeIds) > 0 {
			query = query.Where("type not in ?", typeIds)
		}
	}

	if len(req.Types) > 0 {
		typeIds := make([]int, 0, len(req.Types))
		for _, t := range req.Types {
			if id := models.NotificationTypeFromString(t); id >= 0 {
				typeIds = append(typeIds, id)
			}
		}
		if len(typeIds) > 0 {
			query = query.Where("type in ?", typeIds)
		}
	}

	if req.AccountId != "" {
		filterAccountId, err := strconv.ParseUint(req.AccountId, 10, 64)
		controllers.AbortIf(c, err != nil, 422, gin.H{"error": "account_id is invalid"})

		query = query.Where("from_account_id = ?", filterAccountId)
	}

	if req.MaxId > 0 {
		query = query.Where("id < ?", req.MaxId)
	}

	if req.MinId > 0 {
		query = query.Where("id > ?", req.MinId)
	}

	if req.SinceId > 0 {
		query = query.Where("id >= ?", req.SinceId)
	}

	if req.Limit > 100 {
		req.Limit = 100
	}

	if req.Limit <= 0 {
		req.Limit = 20
	}

	// Authorization can hide an entire batch after a block or recipient change.
	// Keep scanning older rows until the visible page is full or history ends;
	// an empty filtered batch must not masquerade as the end of pagination.
	ret := make([]*serializer.Notification, 0, req.Limit)
	var cursor uint64
	for len(ret) < req.Limit {
		batchQuery := query.Session(&gorm.Session{})
		if cursor != 0 {
			batchQuery = batchQuery.Where("id < ?", cursor)
		}
		batchSize := req.Limit
		if batchSize < 20 {
			batchSize = 20
		}
		nfs := make(models.Notifications, 0, batchSize)
		err = batchQuery.Order("id DESC").Limit(batchSize).Find(&nfs).Error
		controllers.AbortIfError(c, err)
		if len(nfs) == 0 {
			break
		}
		visible, err := serializer.ToNotifications(nfs, account)
		controllers.AbortIfError(c, err)
		remaining := req.Limit - len(ret)
		if len(visible) > remaining {
			visible = visible[:remaining]
		}
		ret = append(ret, visible...)
		cursor = nfs[len(nfs)-1].Id
		if len(nfs) < batchSize {
			break
		}
	}
	common.SetListLinks(c, ret, func(n *serializer.Notification) string { return n.Id })
	c.JSON(200, ret)
}
