package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

type ListRequest struct {
	Resolved        *bool  `form:"resolved"`
	AccountId       uint64 `form:"account_id"`
	TargetAccountId uint64 `form:"target_account_id"`
	MaxId           uint64 `form:"max_id"`
	SinceId         uint64 `form:"since_id"`
	Limit           int    `form:"limit"`
	MinId           uint64 `form:"min_id"`
}

func List(c *gin.Context) {
	req := &ListRequest{}
	err := c.ShouldBindQuery(req)
	controllers.AbortIf(c, err != nil, 400, gin.H{"error": err}) //nolint:goconst

	query := db.GetDB().Model(&models.Report{})

	if req.Resolved != nil {
		if *req.Resolved {
			query = query.Where("action_taken_at is not null")
		} else {
			query = query.Where("action_taken_at is null")
		}
	}

	if req.AccountId != 0 {
		query = query.Where("account_id = ?", req.AccountId)
	}

	if req.TargetAccountId != 0 {
		query = query.Where("target_account_id = ?", req.TargetAccountId)
	}

	if req.MaxId != 0 {
		query = query.Where("id < ?", req.MaxId)
	}

	if req.SinceId != 0 {
		query = query.Where("id > ?", req.SinceId)
	}

	if req.Limit != 0 {
		query = query.Limit(req.Limit)
	}

	if req.MinId != 0 {
		query = query.Where("id > ?", req.MinId)
	}

	reports := make([]*models.Report, 0)
	err = query.Find(&reports).Error

	controllers.AbortIfError(c, err)

	currentAccount := controllers.MustGetAccount(c)

	ret, err := serializer.ToReports(reports, currentAccount)
	controllers.AbortIfError(c, err)

	c.JSON(200, ret)
}
