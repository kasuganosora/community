package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/utils"
)

type UpdateRequest struct {
	Category *string   `json:"category" binding:"in:spam,other,legal,violation"`
	RuleIds  *[]string `json:"rule_ids"`
}

func Update(c *gin.Context) {
	report := controllers.GetIdFromContext(&models.Report{}, c, db.GetDB())
	var err error

	req := &UpdateRequest{}
	err = c.ShouldBindJSON(req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": err})

	if req.Category != nil {
		report.SetCategory(*req.Category)
	}

	if req.RuleIds != nil {
		var ruleIds []uint64
		ruleIds, err = utils.StringArrayToUint64Array(utils.FilterDuplicateAndEmptyString(*req.RuleIds))
		controllers.AbortIf(c, err != nil, 422, gin.H{"error": "invalid rule ids"})

		// 检查 rule 是否存在
		if len(ruleIds) > 0 {
			rulesId := make([]uint64, 0)
			err = db.GetDB().Model(&models.Rule{}).Where("id IN ?", ruleIds).Pluck("id", &rulesId).Error
			controllers.AbortIfError(c, err)

			controllers.AbortIf(c, len(rulesId) != len(ruleIds), 422, gin.H{"error": "any rule not found"})
		}

		report.RuleIds = ruleIds
	}

	err = db.GetDB().Save(report).Error
	controllers.AbortIfError(c, err)

	currentAccount := controllers.MustGetAccount(c)
	ret, err := serializer.ToReport(report, currentAccount)
	controllers.AbortIfError(c, err)

	c.JSON(200, ret)
}
