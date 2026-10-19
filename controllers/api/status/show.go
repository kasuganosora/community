package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

func Show(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)
	account := controllers.GetAccount(c)

	var err error

	if status.ReblogId != nil {
		reblog := &models.Status{}
		query := db.GetDB().Model(&models.Status{})
		query = models.StatusLoadAllQuery(query)
		err = query.First(reblog, *status.ReblogId).Error
		controllers.AbortIfError(c, err)

		status.Reblog = reblog
	}

	abortUnlessVisible(c, db.GetDB(), status, account)

	returnFullSingleStatus(c, status, account)
}
