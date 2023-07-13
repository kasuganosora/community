package list

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

func GetListByParam(c *gin.Context) (list *models.List, abort bool) {
	listId, err := strconv.ParseUint(c.Param("id"), 10, 64)
	abort = true

	if err != nil || listId == 0 {
		c.AbortWithStatusJSON(400, gin.H{
			"errors": "invalid list id",
		})
		return
	}

	list = &models.List{}
	err = db.GetDB().Where("id = ?", listId).First(list).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{
				"errors": "list not found",
			})
			return
		}

		err = errors.Wrap(err, "GetListByParam error")
		_ = c.Error(err)
		return
	}

	abort = false
	return
}
