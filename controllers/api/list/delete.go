package list

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

func DeleteList(c *gin.Context) {
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

		err = errors.Wrap(err, "DeleteList error")
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

	err = db.GetDB().Delete(list).Error

	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(200, gin.H{})
}
