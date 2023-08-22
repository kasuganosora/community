package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
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
	if err := c.ShouldBindJSON(req); err != nil {
		c.JSON(400, gin.H{
			"error": err.Error(),
		})
		return
	}

	currentAccount := c.MustGet("account").(*models.Account)

	targetAccountId, err := strconv.ParseUint(req.AccountId, 10, 64)
	if err != nil {
		c.JSON(400, gin.H{
			"error": "invalid account id",
		})
		return
	}

	targetAccount := &models.Account{}
	if err = db.GetDB().Where("id = ?", targetAccountId).First(targetAccount).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(400, gin.H{
				"error": "account not found",
			})
			return
		}
		_ = c.Error(err)
		return
	}

	statusIds, err := utils.StringArrayToUint64Array(utils.FilterDuplicateAndEmptyString(req.StatusIds))
	if err != nil {
		c.JSON(400, gin.H{
			"error": "invalid status ids",
		})
		return
	}

	// 检查 status 是否存在
	if len(statusIds) > 0 {
		statusesId := make([]uint64, 0)
		if err = db.GetDB().Model(&models.Status{}).Where("id IN ?", statusIds).Pluck("id", &statusesId).Error; err != nil {
			_ = c.Error(err)
			return
		}

		if len(statusesId) != len(statusIds) {
			c.JSON(400, gin.H{
				"error": "any statuses not found",
			})
			return
		}
	}

	ruleIds, err := utils.StringArrayToUint64Array(utils.FilterDuplicateAndEmptyString(req.RuleIds))
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
				"error": "any rules not found",
			})
			return
		}
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

	if err = db.GetDB().Create(report).Error; err != nil {
		_ = c.Error(err)
		return
	}

	ret, err := serializer.ToReport(report, currentAccount)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(200, ret)
}
