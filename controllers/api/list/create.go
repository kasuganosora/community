package list

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

type CreateListRequest struct {
	Title         string `binding:"required,min=1,max=64"         json:"title"`
	RepliesPolicy string `binding:"omitempty,oneof=followed list none"       json:"replies_policy"`
	Visibility    string `binding:"omitempty,oneof=public private followers" json:"visibility"`
}

func CreateList(c *gin.Context) {
	req := CreateListRequest{}

	if err := controllers.BindBody(c, &req); err != nil {
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

	// Mastodon lists are private. Lists created by Mastodon clients carry no
	// visibility, so default to private instead of the model's zero value
	// (public), which exposed the list and its members to everyone.
	switch req.Visibility {
	case "", "private":
		list.Visibility = models.ListVisibilityPrivate
	case "public": //nolint:goconst
		list.Visibility = models.ListVisibilityPublic
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
