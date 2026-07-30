package list

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

type UpdateListRequest struct {
	Title         *string `binding:"omitempty,min=1,max=64"                json:"title"`
	RepliesPolicy *string `binding:"omitempty,oneof=followed list none"       json:"replies_policy"`
	Visibility    *string `binding:"omitempty,oneof=public private followers" json:"visibility"`
}

func UpdateList(c *gin.Context) {
	listId, err := strconv.ParseUint(c.Param("id"), 10, 64)

	if err != nil || listId == 0 {
		c.AbortWithStatusJSON(400, gin.H{
			"code": 400,               //nolint:goconst
			"msg":  "invalid list id", //nolint:goconst
		})

		return
	}

	req := UpdateListRequest{}

	if err = c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{
			"error": err.Error(), //nolint:goconst
		})

		return
	}

	list := &models.List{}

	err = db.GetDB().Where("id = ?", listId).First(list).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(400, gin.H{
				"code": 404,
				"msg":  "list not found", //nolint:goconst
			})

			return
		}

		err = errors.Wrap(err, "UpdateList error")
		_ = c.Error(err)

		return
	}

	if list.AccountId != c.MustGet("account").(*models.Account).Id { //nolint:forcetypeassert
		c.AbortWithStatusJSON(403, gin.H{
			"code": 403,
			"msg":  "permission denied",
		})

		return
	}

	if req.Title != nil {
		list.Title = *req.Title
	}

	if req.RepliesPolicy != nil {
		list.SetRepliesPolicy(*req.RepliesPolicy)
	}

	if req.Visibility != nil {
		switch *req.Visibility {
		case "public": //nolint:goconst
			list.Visibility = models.ListVisibilityPublic
		case "private": //nolint:goconst
			list.Visibility = models.ListVisibilityPrivate
		case "followers": //nolint:goconst
			list.Visibility = models.ListVisibilityFollowers
		}
	}

	err = db.GetDB().Save(list).Error
	if err != nil {
		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToList(list))
}
