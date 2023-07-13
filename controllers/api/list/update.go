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
	Title         *string `json:"title" binding:"min:1;max:64"`
	RepliesPolicy *string `json:"replies_policy" binding:"in:followed,list,none"`
}

func UpdateList(c *gin.Context) {
	listId, err := strconv.ParseUint(c.Param("id"), 10, 64)

	if err != nil || listId == 0 {
		c.AbortWithStatusJSON(400, gin.H{
			"code": 400,
			"msg":  "invalid list id",
		})
		return
	}

	req := UpdateListRequest{}

	if err = c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{
			"error": err.Error(),
		})
		return
	}

	list := &models.List{}

	err = db.GetDB().Where("id = ?", listId).First(list).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(400, gin.H{
				"code": 404,
				"msg":  "list not found",
			})
			return
		}

		err = errors.Wrap(err, "UpdateList error")
		_ = c.Error(err)
		return
	}

	if list.AccountId != c.MustGet("account").(*models.Account).Id {
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

	err = db.GetDB().Save(list).Error

	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(200, serializer.ToList(list))
}
