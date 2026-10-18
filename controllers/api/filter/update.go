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
	"strings"
	"time"
)

type UpdateFilterRequest struct {
	Title        *string                  `json:"title"`
	ExpiresIn    *uint64                  `json:"expires_in"`
	Context      *[]string                `json:"context"`
	FilterAction *string                  `json:"filter_action"`
	Keywords     *[]*models.FilterKeyword `json:"keywords"`
	// KeywordsAttributes (Mastodon v2) adds, edits or (_destroy) removes
	// individual keywords; Keywords replaces the whole list.
	KeywordsAttributes []KeywordAttribute `json:"keywords_attributes"`
}

func Update(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "id must be a number"}) //nolint:goconst

		return
	}

	var req UpdateFilterRequest
	if err = controllers.BindBody(c, &req); err != nil {
		fail(c, 400, ErrCodeFilterInvalidRequest, err.Error())

		return
	}

	currentAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	filter := &models.Filter{}

	err = db.GetDB().Where("id = ? and account_id = ?", id, currentAccount.Id).First(filter).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, 404, ErrCodeFilterNotFound, "filter not found")

			return
		}

		_ = c.Error(err)

		return
	}

	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			fail(c, 422, ErrCodeFilterTitleRequired, "Title cannot be empty")

			return
		}
		filter.Title = title
	}

	if req.FilterAction != nil {
		action, ok := models.ParseFilterAction(*req.FilterAction)
		if !ok || strings.TrimSpace(*req.FilterAction) == "" {
			fail(c, 422, ErrCodeFilterActionUnsupported, "Unsupported filter action")

			return
		}
		filter.FilterAction = action
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
		if !models.ValidFilterContexts(*req.Context) {
			fail(c, 422, ErrCodeFilterContextUnsupported, "Unsupported filter context")

			return
		}
		filter.Context = *req.Context
	}

	if req.Keywords != nil {
		if len(*req.Keywords) == 0 {
			fail(c, 422, ErrCodeFilterKeywordsRequired, "At least one keyword is required")

			return
		}
		for _, keyword := range *req.Keywords {
			if keyword == nil {
				fail(c, 422, ErrCodeFilterKeywordEmpty, "Keyword cannot be empty")

				return
			}
			text, kerr := validKeyword(keyword.Keyword)
			if kerr != nil {
				fail(c, kerr.status, kerr.code, kerr.msg)

				return
			}
			keyword.Keyword = text
		}
		filter.Keywords = updateKeywordsFields(filter.Keywords, *req.Keywords)
	}

	if len(req.KeywordsAttributes) > 0 {
		keywords, kerr := applyKeywordAttributes(strconv.FormatUint(filter.Id, 10), filter.Keywords, req.KeywordsAttributes)
		if kerr != nil {
			fail(c, kerr.status, kerr.code, kerr.msg)

			return
		}
		if len(keywords) == 0 {
			fail(c, 422, ErrCodeFilterKeywordsRequired, "At least one keyword is required")

			return
		}
		filter.Keywords = keywords
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
