package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func Reopen(c *gin.Context) {
	report := controllers.GetIdFromContext(&models.Report{}, c, db.GetDB())

	currentAccount := controllers.MustGetAccount(c)
	report.ActionTakenAt = nil
	report.ActionTakenByAccountId = 0
	report.ActionTakenByAccount = nil

	err := db.GetDB().Save(report).Error
	controllers.AbortIfError(c, err)

	ret, err := serializer.ToReport(report, currentAccount)
	controllers.AbortIfError(c, err)

	c.JSON(200, ret)
}
