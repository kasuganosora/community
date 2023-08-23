package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

func Delete(c *gin.Context) {
	report := controllers.GetIdFromContext(&models.Report{}, c, db.GetDB())
	if err := db.GetDB().Delete(report).Error; err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(200, gin.H{})
}
