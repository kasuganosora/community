package rule

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

type UpdateRequest struct {
	Text     *string `json:"text" binding:"min=1,max=255"`
	Priority *uint   `json:"priority"`
}

func Update(c *gin.Context) {
	rule := controllers.GetIdFromContext(&models.Rule{}, c, db.GetDB())

	req := &UpdateRequest{}
	err := c.ShouldBindJSON(req)
	controllers.AbortIf(c, err != nil, 422, gin.H{"message": err})

	if req.Text != nil {
		rule.Text = *req.Text
	}

	if req.Priority != nil {
		rule.Priority = *req.Priority
	}

	err = db.GetDB().Save(rule).Error

	controllers.AbortIfError(c, err)

	ret := serializer.ToRule(rule)

	c.JSON(200, ret)
}
