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

func KeywordsList(c *gin.Context) {
	filterId, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "filter_id must be a number"}) //nolint:goconst

		return
	}

	if filterId == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "filter_id must be a number"})

		return
	}

	currentUser := c.MustGet("user").(*models.User) //nolint:forcetypeassert
	filter := &models.Filter{}

	err = db.GetDB().Where("id = ? and account_id = ?", filterId, currentUser.Id).First(filter).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "filter not found"}) //nolint:goconst

			return
		}

		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToFilterKeywords(filter))
}
