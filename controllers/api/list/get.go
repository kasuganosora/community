package list

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"strconv"
)

func GetList(c *gin.Context) {
	listId, err := strconv.ParseUint(c.Param("id"), 10, 64)

	if err != nil || listId == 0 {
		c.AbortWithStatusJSON(400, gin.H{
			"code": 400,               //nolint:goconst
			"msg":  "invalid list id", //nolint:goconst
		})

		return
	}

	list, abort := GetListByParam(c)
	if abort {
		return
	}

	var currentAccount *models.Account
	if _currentAccount, ok := c.Get("account"); ok {
		currentAccount = _currentAccount.(*models.Account) //nolint:forcetypeassert
	}

	canView, err := models.ListCanView(list, currentAccount)
	if err != nil {
		_ = c.Error(err)

		return
	}

	if !canView {
		c.AbortWithStatusJSON(403, gin.H{
			"error": "forbidden", //nolint:goconst
		})

		return
	}

	c.JSON(200, serializer.ToList(list))
}
