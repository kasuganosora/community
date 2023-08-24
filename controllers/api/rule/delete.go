package rule

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

func Delete(c *gin.Context) {
	rule := controllers.GetIdFromContext(&models.Rule{}, c, db.GetDB())
	err := db.GetDB().Delete(rule).Error
	controllers.AbortIfError(c, err)

	c.JSON(200, gin.H{})
}
