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
	if status.AccountId != currentAccount.Id {
		c.AbortWithStatusJSON(403, gin.H{"error": "forbidden"})
		return
	}

	// 是否已经置顶
	isPinned, err := status.IsPinned(dbConn, currentAccount)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if isPinned {
		c.AbortWithStatusJSON(400, gin.H{"error": "already pinned"})
		return
	}

	pin := &models.PinStatus{}
	pin.AccountId = currentAccount.Id
	pin.StatusId = status.Id

	err = dbConn.Create(pin).Error
	if err != nil {
		_ = c.Error(err)
		return
	}

	returnFullSingleStatus(c, status, currentAccount)
}

func UnPin(c *gin.Context) {
	status := getRealStatus(c)
	currentAccount := c.MustGet("account").(*models.Account)
	dbConn := db.GetDB()
	//  只能 自己置顶自己的
	if status.AccountId != currentAccount.Id {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}

	// 是否已经置顶
	isPinned, err := status.IsPinned(dbConn, currentAccount)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if !isPinned {
		c.AbortWithStatusJSON(400, gin.H{"error": "not pinned"})
		return
	}

	pin := &models.PinStatus{}
	pin.AccountId = currentAccount.Id
	pin.StatusId = status.Id

	err = dbConn.Delete(pin).Error
	if err != nil {
		_ = c.Error(err)
		return
	}

	returnFullSingleStatus(c, status, currentAccount)
}
