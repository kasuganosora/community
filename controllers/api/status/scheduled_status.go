package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"strconv"
	"time"
)

type ListScheduledStatusesRequest struct {
	MaxId   *uint64 `query:"max_id"`
	SinceId *uint64 `query:"since_id"`
	Limit   *uint64 `query:"limit"`
	Offset  *uint64 `query:"offset"`
}

// ListScheduledStatuses 获取自己的定时发送的状态列表.
func ListScheduledStatuses(c *gin.Context) {
	req := &ListScheduledStatusesRequest{}
	err := c.ShouldBindQuery(req)
	controllers.AbortIf(c, err != nil, 400, gin.H{"error": err}) //nolint:goconst

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
		query = query.Limit(int(*req.Limit)) //nolint:gosec // G115: reviewed and safe
	}

	if req.Offset != nil {
		query = query.Offset(int(*req.Offset)) //nolint:gosec // G115: reviewed and safe
	}

	var statuses []*models.Status

	err = query.Find(&statuses).Error
	controllers.AbortIfError(c, err)

	ret := serializer.ToScheduledStatuses(statuses)
	c.JSON(200, ret)
}

func ViewSingleScheduledStatus(c *gin.Context) {
	idStr := c.Param("id")
	controllers.AbortIf(c, idStr == "", 422, gin.H{"error": "id is required"})

	id, err := strconv.ParseUint(idStr, 10, 64)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": "id must be a number"}) //nolint:goconst

	currentAccount := controllers.MustGetAccount(c)

	status := &models.Status{}
	query := db.GetDB().Model(&models.Status{}).Where("id = ?", id).
		Where("account_id = ?", currentAccount.Id).
		Where("scheduled_at IS NOT NULL")

	query = models.StatusLoadAllQuery(query)

	err = query.First(status).Error
	controllers.AbortIfError(c, err)

	ret := serializer.ToScheduledStatus(status)
	c.JSON(200, ret)
}

type UpdateStatusScheduledAtRequest struct {
	ScheduledAt string `binding:"required" json:"scheduled_at"`
}

func UpdateStatusScheduledAt(c *gin.Context) {
	req := &UpdateStatusScheduledAtRequest{}
	err := c.ShouldBindJSON(req)
	controllers.AbortIf(c, err != nil, 400, gin.H{"error": err})

	idStr := c.Param("id")
	controllers.AbortIf(c, idStr == "", 422, gin.H{"error": "id is required"})

	id, err := strconv.ParseUint(idStr, 10, 64)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": "id must be a number"})

	currentAccount := controllers.MustGetAccount(c)

	newSheduledAt, err := time.Parse(time.RFC3339, req.ScheduledAt)
	controllers.AbortIf(c, err != nil, 422, gin.H{"error": "invalid scheduled_at"})

	status := &models.Status{}
	query := db.GetDB().Model(&models.Status{}).Where("id = ?", id).
		Where("account_id = ?", currentAccount.Id).
		Where("scheduled_at IS NOT NULL")

	query = models.StatusLoadAllQuery(query)

	err = query.First(status).Error
	controllers.AbortIfError(c, err)

	status.ScheduledAt = &newSheduledAt

	err = db.GetDB().Select("scheduled_at").Save(status).Error
	controllers.AbortIfError(c, err)

	ret := serializer.ToScheduledStatus(status)
	c.JSON(200, ret)
}
