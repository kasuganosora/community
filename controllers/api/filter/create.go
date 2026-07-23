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
	Title     string                  `binding:"required" json:"title"`
	ExpiresIn uint64                  `json:"expires_in"`
	Context   []string                `binding:"required" json:"context"`
	Keywords  []*models.FilterKeyword `binding:"required" json:"keywords"`
}

func Create(c *gin.Context) {
	var req CreateFilterRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": err.Error()}) //nolint:goconst

		return
	}

	currentAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

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
