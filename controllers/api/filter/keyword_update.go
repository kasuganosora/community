package filter

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
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
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	err = db.GetDB().Where("id = ? and account_id = ?", filterId, account.Id).First(filter).Error
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
		if k != nil && (k.KeyID() == keywordId || k.Keyword == keywordId) {
			keyword = k

			break
		}
	}

	if keyword == nil {
		c.AbortWithStatusJSON(404, gin.H{"error": "keyword not found"})

		return
	}

	keywordUpdateRequest := &KeywordUpdateRequest{FilterKeyword: *keyword}
	if err = controllers.BindBody(c, keywordUpdateRequest); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})

		return
	}

	text, invalid := validKeyword(keywordUpdateRequest.Keyword)
	if invalid != nil {
		fail(c, invalid.status, invalid.code, invalid.msg)
		return
	}
	keyword.Uuid = keyword.KeyID()
	keyword.Keyword = text
	keyword.WholeWord = keywordUpdateRequest.WholeWord

	if err = db.GetDB().Select("keywords").Save(filter).Error; err != nil {
		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToFilterKeyword(filter, keyword))
}
