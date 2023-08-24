package timelines

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
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
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": err})

	account := controllers.MustGetAccount(c)

	nfs := make(models.Notifications, 0)
	query := db.GetDB().Model(&models.Notification{}).Where("account_id = ?", account.Id)

	if len(req.ExcludeTypes) > 0 {
		query = query.Where("type not in ?", req.ExcludeTypes)
	}

	if len(req.Types) > 0 {
		query = query.Where("type in ?", req.Types)
	}

	if req.AccountId != "" {
		filterAccountId, err := strconv.ParseUint(req.AccountId, 10, 64)
		controllers.AbortIf(c, err != nil, 422, gin.H{"error": "account_id is invalid"})
		query = query.Where("account_id = ?", filterAccountId)
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

	if req.Limit > 0 && req.Limit > 100 {
		req.Limit = 100
	}

	if req.Limit <= 0 {
		req.Limit = 20
	}

	err = query.Order("id DESC").Limit(req.Limit).Find(&nfs).Error
	controllers.AbortIfError(c, err)

	ret, err := serializer.ToNotifications(nfs, account)
	controllers.AbortIfError(c, err)

	c.JSON(200, ret)

}
