package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

func MuteStatus(c *gin.Context) {
	status := getRealStatus(c)

	currentAccount := controllers.MustGetAccount(c)
	if status.AccountId != currentAccount.Id {
		abortUnlessVisible(c, db.GetDB(), status, currentAccount)
	}
	// 只能自己 mute 自己的 status
	controllers.AbortIf(c, status.AccountId != currentAccount.Id, 400, gin.H{"error": "forbidden"}) //nolint:goconst

	dbConn := db.GetDB()
	// 是否已经 mute
	isMuted, err := status.IsMuted(dbConn, currentAccount)
	controllers.AbortIfError(c, err)

	// Mastodon: muting an already muted conversation is a no-op returning
	// the status.
	if isMuted {
		returnFullSingleStatus(c, status, currentAccount)

		return
	}

	mute := &models.MuteStatus{}
	mute.AccountId = currentAccount.Id
	mute.StatusId = status.Id

	err = dbConn.Create(mute).Error

	controllers.AbortIfError(c, err)

	returnFullSingleStatus(c, status, currentAccount)
}

func UnmuteStatus(c *gin.Context) {
	status := getRealStatus(c)

	currentAccount := controllers.MustGetAccount(c)
	dbConn := db.GetDB()

	if status.AccountId != currentAccount.Id {
		abortUnlessVisible(c, db.GetDB(), status, currentAccount)
	}
	//  只能 自己 mute 自己的
	controllers.AbortIf(c, status.AccountId != currentAccount.Id, 400, gin.H{"error": "forbidden"})

	// 是否已经 mute
	isMuted, err := status.IsMuted(dbConn, currentAccount)
	controllers.AbortIfError(c, err)

	// Mastodon: unmuting a conversation that is not muted is a no-op
	// returning the status.
	if !isMuted {
		returnFullSingleStatus(c, status, currentAccount)

		return
	}

	err = dbConn.Delete(&models.MuteStatus{}, "account_id = ? AND status_id = ?", currentAccount.Id, status.Id).Error
	controllers.AbortIfError(c, err)

	// 同时删除对这个账号的mute
	err = dbConn.Delete(&models.MuteAccount{}, "account_id = ? AND target_id = ?", currentAccount.Id, status.AccountId).Error
	controllers.AbortIfError(c, err)

	returnFullSingleStatus(c, status, currentAccount)
}
