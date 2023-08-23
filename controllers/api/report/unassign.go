package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func Unassign(c *gin.Context) {
	report := controllers.GetIdFromContext(&models.Report{}, c, db.GetDB())
	currentAccount := controllers.MustGetAccount(c)

	report.AssignedAccountId = 0

	if err := db.GetDB().Select("assigned_account_id").Save(report).Error; err != nil {
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
