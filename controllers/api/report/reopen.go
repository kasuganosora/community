package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func Reopen(c *gin.Context) {
	report, abort := GetReportById(c)
	if abort {
		return
	}

	currentAccount := c.MustGet("account").(*models.Account)
	report.ActionTakenAt = nil

	if err := db.GetDB().Save(report).Error; err != nil {
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
