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

type KeywordUpdateRequest struct {
	models.FilterKeyword
}

func KeywordUpdate(c *gin.Context) {
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

	var keyword *models.FilterKeyword

	for _, k := range filter.Keywords {
		if k.Keyword == keywordId {
			keyword = k

			break
		}
	}

	if keyword == nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "keyword not found"})

		return
	}

	keywordUpdateRequest := &KeywordUpdateRequest{}
	if err = c.ShouldBindJSON(keywordUpdateRequest); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})

		return
	}

	keyword.Keyword = keywordUpdateRequest.Keyword
	keyword.WholeWord = keywordUpdateRequest.WholeWord

	if err = db.GetDB().Select("keywords").Save(filter).Error; err != nil {
		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToFilterKeyword(filter, keyword))
}
