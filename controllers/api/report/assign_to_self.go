package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func AssignToSelf(c *gin.Context) {
	report := controllers.GetIdFromContext(&models.Report{}, c, db.GetDB())
	currentAccount := controllers.MustGetAccount(c)

	report.AssignedAccountId = currentAccount.Id

	err := db.GetDB().Select("assigned_account_id").Save(report).Error
	controllers.AbortIfError(c, err)
	ret, err := serializer.ToReport(report, currentAccount)
	controllers.AbortIfError(c, err)

	c.JSON(200, ret)
}
