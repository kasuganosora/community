package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

func Pin(c *gin.Context) {
	status := getRealStatus(c)

	currentAccount := controllers.MustGetAccount(c)
	dbConn := db.GetDB()
	// 只能自己置顶自己的 status
	controllers.AbortIf(c, status.AccountId != currentAccount.Id, 400, gin.H{"error": "forbidden"})

	// 是否已经置顶
	isPinned, err := status.IsPinned(dbConn, currentAccount)
	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, isPinned, 400, gin.H{"error": "already pinned"})

	pin := &models.PinStatus{}
	pin.AccountId = currentAccount.Id
	pin.StatusId = status.Id

	err = dbConn.Create(pin).Error
	controllers.AbortIfError(c, err)

	returnFullSingleStatus(c, status, currentAccount)
}

func UnPin(c *gin.Context) {
	status := getRealStatus(c)
	currentAccount := c.MustGet("account").(*models.Account)
	dbConn := db.GetDB()
	//  只能 自己置顶自己的
	controllers.AbortIf(c, status.AccountId != currentAccount.Id, 400, gin.H{"error": "forbidden"})

	// 是否已经置顶
	isPinned, err := status.IsPinned(dbConn, currentAccount)
	controllers.AbortIfError(c, err)
	controllers.AbortIf(c, !isPinned, 400, gin.H{"error": "not pinned"})

	pin := &models.PinStatus{}
	pin.AccountId = currentAccount.Id
	pin.StatusId = status.Id

	err = dbConn.Delete(pin).Error
	controllers.AbortIfError(c, err)

	returnFullSingleStatus(c, status, currentAccount)
}
