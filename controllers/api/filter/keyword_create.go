package filter

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

type CreateKeywordRequest struct {
	models.FilterKeyword
}

func KeywordCreate(c *gin.Context) {
	filterId, err := strconv.ParseUint(c.Param("filter_id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "filter_id must be a number"}) //nolint:goconst

		return
	}

	if filterId == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "filter_id must be a number"})

		return
	}

	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	filter := &models.Filter{}

	err = db.GetDB().Where("id = ? and account_id = ?", filterId, account.Id).First(filter).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "filter not found"}) //nolint:goconst

			return
		}

		_ = c.Error(err)

		return
	}

	req := &CreateKeywordRequest{FilterKeyword: models.FilterKeyword{WholeWord: true}}
	if err = controllers.BindBody(c, req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})

		return
	}

	if len(filter.Keywords) >= maxKeywordsPerList {
		fail(c, 422, ErrCodeFilterTooManyKeywords, "Too many keywords")
		return
	}
	text, invalid := validKeyword(req.Keyword)
	if invalid != nil {
		fail(c, invalid.status, invalid.code, invalid.msg)
		return
	}
	keyword := req.FilterKeyword
	keyword.Keyword = text
	keyword.Uuid = uuid.NewString()

	filter.Keywords = append(filter.Keywords, &keyword)

	if err = db.GetDB().Select("keywords").Save(filter).Error; err != nil {
		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToFilterKeyword(filter, &keyword))
}
