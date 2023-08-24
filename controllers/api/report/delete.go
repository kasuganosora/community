package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

func Delete(c *gin.Context) {
	report := controllers.GetIdFromContext(&models.Report{}, c, db.GetDB())
	err := db.GetDB().Delete(report).Error
	controllers.AbortIfError(c, err)

	c.JSON(200, gin.H{})
}
