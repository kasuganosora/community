package rule

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func Get(c *gin.Context) {
	rule := controllers.GetIdFromContext(&models.Rule{}, c, db.GetDB())

	c.JSON(200, serializer.ToRule(rule))
}
