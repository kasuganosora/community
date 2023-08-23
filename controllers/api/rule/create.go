package rule

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

type CreateRequest struct {
	Text     string `json:"text" binding:"required"`
	Priority uint   `json:"priority"`
}

func Create(c *gin.Context) {
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(err)
		return
	}

	rule := &models.Rule{
		Text:     req.Text,
		Priority: req.Priority,
	}

	err := db.GetDB().Create(rule).Error
	if err != nil {
		_ = c.Error(err)
		return
	}

	ret := serializer.ToRule(rule)

	c.JSON(200, ret)
}
