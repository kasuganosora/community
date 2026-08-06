package marker

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
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

	ret := make(map[string]*models.MarkerTimelineItem)

	for _, timeline := range req.Timelines {
		if item, ok := marker.Timelines[timeline]; ok {
			ret[timeline] = item
		} else {
			ret[timeline] = &models.MarkerTimelineItem{}
		}
	}

	c.JSON(200, ret)
}
