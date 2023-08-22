package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
)

func Delete(c *gin.Context) {
	report, abort := GetReportById(c)
	if abort {
		return
	}

	if err := db.GetDB().Delete(report).Error; err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(200, gin.H{})
}
