package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

func Delete(c *gin.Context) {
	currentAccount := controllers.MustGetAccount(c)

	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), func(q *gorm.DB) *gorm.DB { return q.Preload("ToAccounts") })

	// Someone else's status: hidden ones are 404 like Mastodon, visible ones
	// keep the explicit 403 for the ownership check.
	if status.AccountId != currentAccount.Id {
		abortUnlessVisible(c, db.GetDB(), status, currentAccount)
	}
	controllers.AbortIf(c, status.AccountId != currentAccount.Id, 403, gin.H{"error": "has no permission"}) //nolint:goconst
	controllers.AbortIf(c, status.ReblogId != nil, 400, gin.H{"error": "can't delete reblog status"})

	err := db.GetDB().Delete(status).Error
	controllers.AbortIfError(c, err)

	c.Data(204, "application/json", nil)
}
