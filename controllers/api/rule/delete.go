package rule

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

func Delete(c *gin.Context) {
	rule, abort := controllers.GetIdFromContext(&models.Rule{}, c, db.GetDB())
	if abort {
		return
	}

	err := db.GetDB().Delete(rule).Error

	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(200, gin.H{})
}
