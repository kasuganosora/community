package marker

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
	"time"
)

type ListRequest struct {
	Timelines []string `form:"timelines[]"`
}

func List(c *gin.Context) {
	var req ListRequest
	err := c.ShouldBindQuery(&req)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})

		return
	}

	currentAccount := c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	marker := &models.Marker{}

	err = db.GetDB().Where("account_id = ?", currentAccount.Id).First(marker).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			_ = c.Error(err)

			return
		}
	}

	if marker.Timelines == nil {
		marker.Timelines = make(map[string]*models.MarkerTimelineItem)
	}

	// Mastodon omits timelines that have no marker; each entry is
	// {last_read_id: "<id>", version: int, updated_at: ISO8601 .000Z}.
	ret := make(map[string]gin.H)

	for _, timeline := range req.Timelines {
		if item, ok := marker.Timelines[timeline]; ok && item != nil {
			ret[timeline] = markerJSON(item)
		}
	}

	c.JSON(200, ret)
}

func markerJSON(item *models.MarkerTimelineItem) gin.H {
	updated := item.UpdatedAt
	if updated == nil {
		now := time.Now()
		updated = &now
	}

	return gin.H{
		"last_read_id": strconv.FormatUint(item.LastReadId, 10),
		"version":      item.Version,
		"updated_at":   serializer.NewAPITime(updated),
	}
}
