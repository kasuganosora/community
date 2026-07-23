package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func GetSource(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB())

	dbConn := db.GetDB()

	var err error

	if status.ReblogId != nil && *status.ReblogId > 0 {
		status.Reblog = &models.Status{}
		err = dbConn.Model(status).Association("Reblog").Find(status.Reblog)
		controllers.AbortIfError(c, err)
		controllers.AbortIf(c, status.Reblog == nil || status.Reblog.Id == 0, 404, gin.H{"error": "status not found"}) //nolint:goconst
	}

	currentAccount := controllers.MustGetAccount(c)

	canView, err := status.CanViewEx(dbConn, currentAccount)
	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, !canView, 403, gin.H{"error": "forbidden"}) //nolint:goconst

	content := &models.StatusContent{}
	err = dbConn.Model(status).Association("Content").Find(content)
	controllers.AbortIfError(c, err)

	c.JSON(200, serializer.ToSource(content))
}
