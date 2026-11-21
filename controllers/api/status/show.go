package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	historysvc "github.com/kasuganosora/community/history"
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

	if account != nil && c.Query("background") != "1" {
		targetID := status.Id
		if status.ReblogId != nil && *status.ReblogId > 0 {
			targetID = *status.ReblogId
		}
		historysvc.RecordAsync(account.Id, targetID)
	}

	returnFullSingleStatus(c, status, account)
}
