package list

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/serializer"
	"strconv"
)

func GetList(c *gin.Context) {
	listId, err := strconv.ParseUint(c.Param("id"), 10, 64)

	if err != nil || listId == 0 {
		c.AbortWithStatusJSON(400, gin.H{
			"code": 400,
			"msg":  "invalid list id",
		})
		return
	}

	list, abort := GetListByParam(c)
	if abort {
		return
	}

	c.JSON(200, serializer.ToList(list))
}
