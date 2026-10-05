package filter

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

type CreateFilterRequest struct {
	Title        string                  `binding:"required" json:"title"`
	ExpiresIn    uint64                  `json:"expires_in"`
	Context      []string                `binding:"required" json:"context"`
	FilterAction string                  `json:"filter_action"`
	Keywords     []*models.FilterKeyword `binding:"required" json:"keywords"`
}

func Create(c *gin.Context) {
	var req CreateFilterRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	action, ok := models.ParseFilterAction(req.FilterAction)
	if !ok {
		c.AbortWithStatusJSON(422, gin.H{"error": "不支持的过滤动作"})

		return
	}
	if !models.ValidFilterContexts(req.Context) {
		c.AbortWithStatusJSON(422, gin.H{"error": "不支持的过滤范围"})

		return
	}
	if len(req.Keywords) == 0 {
		c.AbortWithStatusJSON(422, gin.H{"error": "至少需要一个关键词"})

		return
	}
	for _, keyword := range req.Keywords {
		if keyword == nil || strings.TrimSpace(keyword.Keyword) == "" {
			c.AbortWithStatusJSON(422, gin.H{"error": "关键词不能为空"})

			return
		}
		keyword.Keyword = strings.TrimSpace(keyword.Keyword)
		keyword.Uuid = uuid.NewString()
	}

	currentAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	filter := &models.Filter{
		Title:        strings.TrimSpace(req.Title),
		Context:      req.Context,
		Keywords:     req.Keywords,
		AccountId:    currentAccount.Id,
		FilterAction: action,
	}
	if filter.Title == "" {
		c.AbortWithStatusJSON(422, gin.H{"error": "标题不能为空"})

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
