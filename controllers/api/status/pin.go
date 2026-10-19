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
	if status.AccountId != currentAccount.Id {
		abortUnlessVisible(c, db.GetDB(), status, currentAccount)
	}
	// 只能自己置顶自己的 status
	controllers.AbortIf(c, status.AccountId != currentAccount.Id, 400, gin.H{"error": "forbidden"}) //nolint:goconst

	// 是否已经置顶
	isPinned, err := status.IsPinned(dbConn, currentAccount)
	controllers.AbortIfError(c, err)

	// Mastodon: pinning an already pinned status is a no-op returning it.
	if isPinned {
		returnFullSingleStatus(c, status, currentAccount)

		return
	}

	pin := &models.PinStatus{}
	pin.AccountId = currentAccount.Id
	pin.StatusId = status.Id

	err = dbConn.Create(pin).Error
	controllers.AbortIfError(c, err)

	returnFullSingleStatus(c, status, currentAccount)
}

func UnPin(c *gin.Context) {
	status := getRealStatus(c)
	currentAccount := controllers.MustGetAccount(c)
	dbConn := db.GetDB()
	if status.AccountId != currentAccount.Id {
		abortUnlessVisible(c, db.GetDB(), status, currentAccount)
	}
	//  只能 自己置顶自己的
	controllers.AbortIf(c, status.AccountId != currentAccount.Id, 400, gin.H{"error": "forbidden"})

	// 是否已经置顶
	isPinned, err := status.IsPinned(dbConn, currentAccount)
	controllers.AbortIfError(c, err)

	// Mastodon: unpinning a status that is not pinned is a no-op returning it.
	if !isPinned {
		returnFullSingleStatus(c, status, currentAccount)

		return
	}

	err = dbConn.Where("account_id = ? AND status_id = ?", currentAccount.Id, status.Id).Delete(&models.PinStatus{}).Error
	controllers.AbortIfError(c, err)

	returnFullSingleStatus(c, status, currentAccount)
}
