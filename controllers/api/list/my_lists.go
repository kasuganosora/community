package list

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func GetMyLists(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	lists := make([]*models.List, 0)

	err := db.GetDB().Where("account_id = ?", account.Id).Find(&lists).Error
	if err != nil {
		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToLists(lists))
}
