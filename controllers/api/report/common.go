package report

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

func GetReportById(c *gin.Context) (report *models.Report, abort bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	abort = true
	if err != nil || id == 0 {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid id"})
		return
	}

	report = &models.Report{}
	if err = db.GetDB().Where("id = ?", id).First(report).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.AbortWithStatusJSON(404, gin.H{"error": "report not found"})
			return
		}

		_ = c.Error(err)
		return
	}

	if err = db.GetDB().Delete(report).Error; err != nil {
		_ = c.Error(err)
		return
	}

	abort = false
	return
}
