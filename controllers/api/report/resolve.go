package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"time"
)

func Resolve(c *gin.Context) {
	report := controllers.GetIdFromContext(&models.Report{}, c, db.GetDB())
	currentAccount := controllers.MustGetAccount(c)

	report.AssignedAccountId = currentAccount.Id
	now := time.Now()
	report.ActionTakenAt = &now

	err := db.GetDB().Save(report).Error
	controllers.AbortIfError(c, err)

	ret, err := serializer.ToReport(report, currentAccount)
	controllers.AbortIfError(c, err)

	c.JSON(200, ret)
}
