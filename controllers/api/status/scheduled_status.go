package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
	"time"
)

type ListScheduledStatusesRequest struct {
	MaxId   *uint64 `query:"max_id"`
	SinceId *uint64 `query:"since_id"`
	Limit   *uint64 `query:"limit"`
	Offset  *uint64 `query:"offset"`
}

// ListScheduledStatuses 获取自己的定时发送的状态列表
func ListScheduledStatuses(c *gin.Context) {
	req := &ListScheduledStatusesRequest{}
	if err := c.ShouldBindQuery(req); err != nil {
		c.JSON(422, gin.H{"error": err.Error()})
		return
	}

	currentAccount := controllers.MustGetAccount(c)

	query := db.GetDB().Model(&models.Status{}).Where("account_id = ?", currentAccount.Id).
		Where("scheduled_at IS NOT NULL")

	query = models.StatusLoadAllQuery(query)

	if req.MaxId != nil {
		query = query.Where("id <= ?", req.MaxId)
	}

	if req.SinceId != nil {
		query = query.Where("id >= ?", req.SinceId)
	}

	if req.Limit != nil {
		query = query.Limit(int(*req.Limit))
	}

	if req.Offset != nil {
		query = query.Offset(int(*req.Offset))
	}

	var statuses []*models.Status
	if err := query.Find(&statuses).Error; err != nil {
		_ = c.Error(err)
		return
	}

	ret := serializer.ToScheduledStatuses(statuses)
	c.JSON(200, ret)
}

func ViewSingleScheduledStatus(c *gin.Context) {
	idStr := c.Param("id")
	if idStr == "" {
		c.JSON(422, gin.H{"error": "id is required"})
		return
	}

	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(422, gin.H{"error": "id is required"})
		return
	}

	currentAccount := controllers.MustGetAccount(c)

	status := &models.Status{}
	query := db.GetDB().Model(&models.Status{}).Where("id = ?", id).
		Where("account_id = ?", currentAccount.Id).
		Where("scheduled_at IS NOT NULL")

	query = models.StatusLoadAllQuery(query)

	if err = query.First(status).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "status not found"})
			return
		}
		_ = c.Error(err)
		return
	}

	ret := serializer.ToScheduledStatus(status)
	c.JSON(200, ret)
}

type UpdateStatusScheduledAtRequest struct {
	ScheduledAt string `json:"scheduled_at" binding:"required"`
}

func UpdateStatusScheduledAt(c *gin.Context) {
	req := &UpdateStatusScheduledAtRequest{}
	if err := c.ShouldBindJSON(req); err != nil {
		c.JSON(422, gin.H{"error": err.Error()})
		return
	}

	idStr := c.Param("id")
	if idStr == "" {
		c.JSON(422, gin.H{"error": "id is required"})
		return
	}

	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		c.JSON(422, gin.H{"error": "id is required"})
		return
	}

	currentAccount := c.MustGet("account").(*models.Account)

	newSheduledAt, err := time.Parse(time.RFC3339, req.ScheduledAt)
	if err != nil {
		c.JSON(422, gin.H{"error": "scheduled_at is invalid"})
		return
	}

	status := &models.Status{}
	query := db.GetDB().Model(&models.Status{}).Where("id = ?", id).
		Where("account_id = ?", currentAccount.Id).
		Where("scheduled_at IS NOT NULL")

	query = models.StatusLoadAllQuery(query)

	if err = query.First(status).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(404, gin.H{"error": "status not found"})
			return
		}
		_ = c.Error(err)
		return
	}

	status.ScheduledAt = &newSheduledAt

	if err = db.GetDB().Select("scheduled_at").Save(status).Error; err != nil {
		_ = c.Error(err)
		return
	}

	ret := serializer.ToScheduledStatus(status)
	c.JSON(200, ret)
}
