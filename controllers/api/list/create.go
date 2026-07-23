package list

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

type CreateListRequest struct {
	Title         string `binding:"required;min:1;max:64"       json:"title"`
	RepliesPolicy string `binding:"in:followed,list,none"       json:"replies_policy"`
	Visibility    string `binding:"in:public,private,followers" json:"visibility"`
}

func CreateList(c *gin.Context) {
	req := CreateListRequest{}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{
			"error": err.Error(), //nolint:goconst
		})

		return
	}

	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	list := &models.List{
		Title:     req.Title,
		AccountId: account.Id,
	}

	switch req.Visibility {
	case "public": //nolint:goconst
		list.Visibility = models.ListVisibilityPublic
	case "private": //nolint:goconst
		list.Visibility = models.ListVisibilityPrivate
	case "followers": //nolint:goconst
		list.Visibility = models.ListVisibilityFollowers
	}

	list.SetRepliesPolicy(req.RepliesPolicy)

	err := db.GetDB().Create(list).Error
	if err != nil {
		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToList(list))
}
