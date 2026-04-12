package filter

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"time"
)

type CreateFilterRequest struct {
	Title     string                  `json:"title" binding:"required"`
	ExpiresIn uint64                  `json:"expires_in"`
	Context   []string                `json:"context" binding:"required"`
	Keywords  []*models.FilterKeyword `json:"keywords" binding:"required"`
}

func Create(c *gin.Context) {
	var req CreateFilterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()})
		return
	}

	currentAccount := c.MustGet("account").(*models.Account)

	for i := range req.Keywords {
		req.Keywords[i].Uuid = uuid.NewString()
	}

	filter := &models.Filter{
		Title:     req.Title,
		Context:   req.Context,
		Keywords:  req.Keywords,
		AccountId: currentAccount.Id,
	}

	if req.ExpiresIn != 0 {
		expiresAt := time.Now().Add(time.Duration(req.ExpiresIn) * time.Second)
		filter.ExpiresAt = &expiresAt
	}

	if err := db.GetDB().Create(filter).Error; err != nil {
		_ = c.Error(err)
		return
	}

	ret := serializer.ToFilter(filter)
	c.JSON(200, ret)
}
