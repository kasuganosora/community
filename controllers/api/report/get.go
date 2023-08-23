package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func GetReport(c *gin.Context) {
	report, abort := GetReportById(c)
	if abort {
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
