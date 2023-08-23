package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

func GetSource(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB())

	dbConn := db.GetDB()
	var err error

	if status.ReblogId != nil && *status.ReblogId > 0 {
		status.Reblog = &models.Status{}
		err = dbConn.Model(status).Association("Reblog").Find(status.Reblog)
		if err != nil {
			_ = c.Error(err)
			return
		}

		if status.Reblog != nil || status.Reblog.Id == 0 {
			c.JSON(404, gin.H{"error": "status not found"})
			return
		}
	}

	currentAccount := controllers.MustGetAccount(c)

	canView, err := status.CanViewEx(dbConn, currentAccount)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if !canView {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}

	content := &models.StatusContent{}
	err = dbConn.Model(status).Association("Content").Find(content)

	if err != nil {
		err = errors.Wrap(err, "get status content")
		_ = c.Error(err)
		return
	}

	c.JSON(200, serializer.ToSource(content))

}
