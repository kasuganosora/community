package filter

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
	"time"
)

type UpdateFilterRequest struct {
	Title     *string                  `json:"title"`
	ExpiresIn *uint64                  `json:"expires_in"`
	Context   *[]string                `json:"context"`
	Keywords  *[]*models.FilterKeyword `json:"keywords"`
}

func Update(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "id must be a number"}) //nolint:goconst

		return
	}

	var req UpdateFilterRequest
	if err = c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})

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

	if req.Title != nil {
		filter.Title = *req.Title
	}

	if req.ExpiresIn != nil {
		if *req.ExpiresIn == 0 {
			filter.ExpiresAt = nil
		} else {
			expiresAt := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second) //nolint:gosec // G115: reviewed and safe
			filter.ExpiresAt = &expiresAt
		}
	}

	if req.Context != nil {
		filter.Context = *req.Context
	}

	if req.Keywords != nil {
		filter.Keywords = updateKeywordsFields(filter.Keywords, *req.Keywords)
	}

	if err = db.GetDB().Save(filter).Error; err != nil {
		_ = c.Error(err)

		return
	}

	c.JSON(200, serializer.ToFilter(filter))
}

func updateKeywordsFields(oldKeywords, newKeywords []*models.FilterKeyword) (ret []*models.FilterKeyword) {
	// 新的关键词
	oldKeywordsCheckMap := make(map[string]*models.FilterKeyword)
	for _, keyword := range oldKeywords {
		oldKeywordsCheckMap[keyword.Keyword] = keyword
	}

	ret = make([]*models.FilterKeyword, 0, len(newKeywords))

	for _, keyword := range newKeywords {
		if oldKeyword, ok := oldKeywordsCheckMap[keyword.Keyword]; ok {
			// 旧的关键词
			keyword.Uuid = oldKeyword.Uuid
		} else {
			// 新的关键词
			keyword.Uuid = uuid.NewString()
		}

		ret = append(ret, keyword)
	}

	return
}
