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
}

func Update(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "id must be a number"}) //nolint:goconst

		return
	}

	var req UpdateFilterRequest
	if err = controllers.BindBody(c, &req); err != nil {
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
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			c.AbortWithStatusJSON(422, gin.H{"error": "标题不能为空"})

			return
		}
		filter.Title = title
	}

	if req.FilterAction != nil {
		action, ok := models.ParseFilterAction(*req.FilterAction)
		if !ok || strings.TrimSpace(*req.FilterAction) == "" {
			c.AbortWithStatusJSON(422, gin.H{"error": "不支持的过滤动作"})

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
			c.AbortWithStatusJSON(422, gin.H{"error": "不支持的过滤范围"})

			return
		}
		filter.Context = *req.Context
	}

	if req.Keywords != nil {
		if len(*req.Keywords) == 0 {
			c.AbortWithStatusJSON(422, gin.H{"error": "至少需要一个关键词"})

			return
		}
		for _, keyword := range *req.Keywords {
			if keyword == nil || strings.TrimSpace(keyword.Keyword) == "" {
				c.AbortWithStatusJSON(422, gin.H{"error": "关键词不能为空"})

				return
			}
			keyword.Keyword = strings.TrimSpace(keyword.Keyword)
		}
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
