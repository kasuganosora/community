package marker

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
	"time"
)

type SaveRequest map[string]SaveRequestMarkerItem

type SaveRequestMarkerItem struct {
	LastReadId string `json:"last_read_id"`
}

func Save(c *gin.Context) {
	req := make(SaveRequest)
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, err)
		return
	}

	currentAccount := c.MustGet("account").(*models.Account)

	marker := &models.Marker{}

	err := db.GetDB().Where("account_id = ?", currentAccount.Id).First(marker).Error
	isNew := false
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			isNew = true
		} else {
			_ = c.Error(err)
			return
		}
	}

	if marker.Timelines == nil {
		marker.Timelines = make(map[string]*models.MarkerTimelineItem)
	}

	for timeline, item := range req {
		var lastReadId uint64
		lastReadId, err = strconv.ParseUint(item.LastReadId, 10, 64)
		if err != nil {
			c.JSON(400, gin.H{"message": "invalid last_read_id"})
			return
		}

		if _, ok := marker.Timelines[timeline]; !ok {
			marker.Timelines[timeline] = &models.MarkerTimelineItem{}
		}

		marker.Timelines[timeline].LastReadId = lastReadId
		now := time.Now()
		marker.Timelines[timeline].UpdatedAt = &now
		marker.Timelines[timeline].Version++
	}

	if isNew {
		marker.AccountId = currentAccount.Id
		if err = db.GetDB().Create(marker).Error; err != nil {
			_ = c.Error(err)
			return
		}
	} else {
		if err = db.GetDB().Save(marker).Error; err != nil {
			_ = c.Error(err)
			return
		}
	}

	ret := make(map[string]*models.MarkerTimelineItem)

	for timeline := range req {
		ret[timeline] = marker.Timelines[timeline]
	}

	c.JSON(200, ret)
}
