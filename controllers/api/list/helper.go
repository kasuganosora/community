package list

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func GetListByParam(c *gin.Context) (list *models.List, abort bool) {
	listId, err := strconv.ParseUint(c.Param("id"), 10, 64)

	if err != nil || listId == 0 {
		c.AbortWithStatusJSON(400, gin.H{
			"apierrors": "invalid list id", //nolint:goconst
		})

		return nil, true
	}

	list = &models.List{}

	err = db.GetDB().Where("id = ?", listId).First(list).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{
				"apierrors": "list not found", //nolint:goconst
			})

			return nil, true
		}

		err = errors.Wrap(err, "GetListByParam error")
		_ = c.Error(err)

		return nil, true
	}

	return list, false
}
