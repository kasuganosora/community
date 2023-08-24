package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/controllers/apierrors"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/utils"
	"strconv"
)

type CreateRequest struct {
	AccountId string   `json:"account_id" binding:"required"`
	StatusIds []string `json:"status_ids"`
	Comment   string   `json:"comment"`
	Forward   bool     `json:"forward"`
	Category  string   `json:"category" binding:"in:spam,other,legal,violation"`
	RuleIds   []string `json:"rule_ids"`
}

func Create(c *gin.Context) {
	req := &CreateRequest{}
	err := c.ShouldBindJSON(req)
	controllers.AbortIfError(c, apierrors.WarpToHttpError(err, 422))

	currentAccount := controllers.MustGetAccount(c)
	targetAccountId, err := strconv.ParseUint(req.AccountId, 10, 64)
	controllers.AbortIfError(c, apierrors.WarpToHttpError(err, 422))

	targetAccount := &models.Account{}
	err = db.GetDB().Where("id = ?", targetAccountId).First(targetAccount).Error

	statusIds, err := utils.StringArrayToUint64Array(utils.FilterDuplicateAndEmptyString(req.StatusIds))
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": "invalid status ids"})

	// 检查 status 是否存在
	if len(statusIds) > 0 {
		statusesId := make([]uint64, 0)
		err = db.GetDB().Model(&models.Status{}).Where("id IN ?", statusIds).Pluck("id", &statusesId).Error
		controllers.AbortIfError(c, err)
		controllers.AbortIf(c, len(statusesId) != len(statusIds), 422, gin.H{"error": "any statuses not found"})
	}

	ruleIds, err := utils.StringArrayToUint64Array(utils.FilterDuplicateAndEmptyString(req.RuleIds))
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": "invalid rule ids"})

	// 检查 rule 是否存在
	if len(ruleIds) > 0 {
		rulesId := make([]uint64, 0)
		err = db.GetDB().Model(&models.Rule{}).Where("id IN ?", ruleIds).Pluck("id", &rulesId).Error
		controllers.AbortIfError(c, err)
		controllers.AbortIf(c, len(rulesId) != len(ruleIds), 422, gin.H{"error": "any rule not found"})
	}

	report := &models.Report{
		AccountId:       currentAccount.Id,
		TargetAccountId: targetAccount.Id,
		Comment:         req.Comment,
		Forward:         req.Forward,
		StatusIds:       statusIds,
		RuleIds:         ruleIds,
	}

	report.SetCategory(req.Category)

	err = db.GetDB().Create(report).Error
	controllers.AbortIfError(c, err)

	ret, err := serializer.ToReport(report, currentAccount)
	controllers.AbortIfError(c, err)

	c.JSON(200, ret)
}
