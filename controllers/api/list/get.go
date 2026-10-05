package list

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
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

// ListsContainingAccount is GET /api/v1/accounts/:id/lists: the current user's
// lists that already include this account.
func ListsContainingAccount(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accountID == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid account id"})
		return
	}
	self := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	lists := make([]*models.List, 0)
	err = db.GetDB().Model(&models.List{}).
		Where("account_id = ?", self.Id).
		Where("id IN (?)", db.GetDB().Model(&models.ListAccount{}).Where("account_id = ?", accountID).Select("list_id")).
		Find(&lists).Error
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(200, serializer.ToLists(lists))
}
