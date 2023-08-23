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
	if status.AccountId != currentAccount.Id {
		c.AbortWithStatusJSON(403, gin.H{"error": "has no permission"})
		return
	}

	if status.ReblogId != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "can't delete reblog status"})
		return
	}

	err := db.GetDB().Delete(status).Error
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.Data(204, "application/json", nil)
}
