package filter

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func List(c *gin.Context) {
	currentAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	// 获取所有的过滤器
	filters, err := models.GetFilters(db.GetDB(), currentAccount.Id)
	if err != nil {
		_ = c.Error(err)

		return
	}

	ret := make([]*serializer.Filter, len(filters))

	for i, filter := range filters {
		ret[i] = serializer.ToFilter(filter)
	}

	c.JSON(200, ret)
}
