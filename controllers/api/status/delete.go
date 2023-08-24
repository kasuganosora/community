package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

func Delete(c *gin.Context) {
	currentAccount := controllers.MustGetAccount(c)

	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB())

	// check permission
	controllers.AbortIf(c, status.AccountId != currentAccount.Id, 403, gin.H{"error": "has no permission"})
	controllers.AbortIf(c, status.ReblogId != nil, 400, gin.H{"error": "can't delete reblog status"})

	err := db.GetDB().Delete(status).Error
	controllers.AbortIfError(c, err)

	c.Data(204, "application/json", nil)
}
