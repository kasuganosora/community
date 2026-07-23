package filter

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

func Show(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "id must be a number"}) //nolint:goconst

		return
	}

	currentAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	filter := &models.Filter{}

	err = db.GetDB().Where("id = ? and account_id = ?", id, currentAccount.Id).First(filter).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "filter not found"}) //nolint:goconst

			return
		}

		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToFilter(filter))
}
