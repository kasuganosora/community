package report

import (
	"github.com/gin-gonic/gin"
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
	report, abort := GetReportById(c)
	if abort {
		return
	}

	var err error

	req := &UpdateRequest{}
	if err = c.ShouldBindJSON(req); err != nil {
		c.JSON(400, gin.H{
			"error": err.Error(),
		})
		return
	}

	if req.Category != nil {
		report.SetCategory(*req.Category)
	}

	if req.RuleIds != nil {
		var ruleIds []uint64
		ruleIds, err = utils.StringArrayToUint64Array(utils.FilterDuplicateAndEmptyString(*req.RuleIds))
		if err != nil {
			c.JSON(400, gin.H{
				"error": "invalid rule ids",
			})
			return
		}

		// 检查 rule 是否存在
		if len(ruleIds) > 0 {
			rulesId := make([]uint64, 0)
			if err = db.GetDB().Model(&models.Rule{}).Where("id IN ?", ruleIds).Pluck("id", &rulesId).Error; err != nil {
				_ = c.Error(err)
				return
			}

			if len(rulesId) != len(ruleIds) {
				c.JSON(400, gin.H{
					"error": "any rule not found",
				})
				return
			}
		}

		report.RuleIds = ruleIds
	}

	if err = db.GetDB().Save(report).Error; err != nil {
		_ = c.Error(err)
		return
	}

	currentAccount := c.MustGet("account").(*models.Account)
	ret, err := serializer.ToReport(report, currentAccount)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(200, ret)
}
