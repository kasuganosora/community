package filter

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
	"strings"
)

func KeywordDelete(c *gin.Context) {
	keywordId := c.Param("id")
	idParts := strings.Split(keywordId, "-")

	if len(idParts) < 2 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid keyword id"}) //nolint:goconst

		return
	}

	filterId, err := strconv.ParseUint(idParts[0], 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid keyword id"})

		return
	}

	keywordId = strings.Join(idParts[1:], "-")

	filter := &models.Filter{}
	currentUser := c.MustGet("user").(*models.User) //nolint:forcetypeassert

	err = db.GetDB().Where("id = ? and account_id = ?", filterId, currentUser.Id).First(filter).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "filter not found"}) //nolint:goconst

			return
		}

		_ = c.Error(err)

		return
	}

	newKeywords := make([]*models.FilterKeyword, 0, len(filter.Keywords)) // never nil: "null" would be stored as "" (invalid JSON)

	for _, k := range filter.Keywords {
		if k.Keyword == keywordId {
			continue
		}

		newKeywords = append(newKeywords, k)
	}

	filter.Keywords = newKeywords

	if err = db.GetDB().Select("keywords").Save(filter).Error; err != nil {
		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToFilterKeywords(filter))
}
