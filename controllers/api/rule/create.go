package rule

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

type CreateRequest struct {
	Text     string `binding:"required" json:"text"`
	Priority uint   `json:"priority"`
}

func Create(c *gin.Context) {
	var req CreateRequest

	err := c.ShouldBindJSON(&req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"message": err})

	rule := &models.Rule{
		Text:     req.Text,
		Priority: req.Priority,
	}

	err = db.GetDB().Create(rule).Error
	controllers.AbortIfError(c, err)

	ret := serializer.ToRule(rule)

	c.JSON(200, ret)
}
