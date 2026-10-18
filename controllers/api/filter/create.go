package filter

import (
	"github.com/kasuganosora/community/controllers"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

type CreateFilterRequest struct {
	Title        string                  `binding:"required" json:"title"`
	ExpiresIn    uint64                  `json:"expires_in"`
	Context      []string                `binding:"required" json:"context"`
	FilterAction string                  `json:"filter_action"`
	Keywords     []*models.FilterKeyword `json:"keywords"`
	// KeywordsAttributes is the Mastodon v2 spelling; both are accepted.
	KeywordsAttributes []KeywordAttribute `json:"keywords_attributes"`
}

func Create(c *gin.Context) {
	var req CreateFilterRequest
	err := controllers.BindBody(c, &req)
	if err != nil {
		fail(c, 400, ErrCodeFilterInvalidRequest, err.Error())

		return
	}

	action, ok := models.ParseFilterAction(req.FilterAction)
	if !ok {
		fail(c, 422, ErrCodeFilterActionUnsupported, "Unsupported filter action")

		return
	}
	if !models.ValidFilterContexts(req.Context) {
		fail(c, 422, ErrCodeFilterContextUnsupported, "Unsupported filter context")

		return
	}
	attrs := make([]KeywordAttribute, 0, len(req.Keywords)+len(req.KeywordsAttributes))
	for _, keyword := range req.Keywords {
		if keyword == nil {
			fail(c, 422, ErrCodeFilterKeywordEmpty, "Keyword cannot be empty")

			return
		}
		whole := keyword.WholeWord
		attrs = append(attrs, KeywordAttribute{Keyword: keyword.Keyword, WholeWord: &whole})
	}
	for _, attr := range req.KeywordsAttributes {
		attr.Id = "" // nothing to update yet
		attrs = append(attrs, attr)
	}
	keywords, kerr := applyKeywordAttributes("", nil, attrs)
	if kerr != nil {
		fail(c, kerr.status, kerr.code, kerr.msg)

		return
	}
	if len(keywords) == 0 {
		fail(c, 422, ErrCodeFilterKeywordsRequired, "At least one keyword is required")

		return
	}

	currentAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	filter := &models.Filter{
		Title:        strings.TrimSpace(req.Title),
		Context:      req.Context,
		Keywords:     keywords,
		AccountId:    currentAccount.Id,
		FilterAction: action,
	}
	if filter.Title == "" {
		fail(c, 422, ErrCodeFilterTitleRequired, "Title cannot be empty")

		return
	}

	if req.ExpiresIn != 0 {
		expiresAt := time.Now().Add(time.Duration(req.ExpiresIn) * time.Second) //nolint:gosec // G115: reviewed and safe
		filter.ExpiresAt = &expiresAt
	}

	err = db.GetDB().Create(filter).Error
	if err != nil {
		_ = c.Error(err)

		return
	}

	ret := serializer.ToFilter(filter)
	c.JSON(200, ret)
}
