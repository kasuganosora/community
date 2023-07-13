package list

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

type CreateListRequest struct {
	Title         string `json:"title" binding:"required;min:1;max:64"`
	RepliesPolicy string `json:"replies_policy" binding:"in:followed,list,none"`
}

func CreateList(c *gin.Context) {
	req := CreateListRequest{}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{
			"error": err.Error(),
		})
		return
	}

	account := c.MustGet("account").(*models.Account)

	list := &models.List{
		Title:     req.Title,
		AccountId: account.Id,
	}

	list.SetRepliesPolicy(req.RepliesPolicy)

	err := db.GetDB().Create(list).Error

	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(200, serializer.ToList(list))
}
