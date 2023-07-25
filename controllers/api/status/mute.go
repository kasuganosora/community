package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

func MuteStatus(c *gin.Context) {
	status, abort := getRealStatus(c)

	if abort {
		return
	}

	currentAccount := c.MustGet("account").(*models.Account)
	// 只能自己 mute 自己的 status
	if status.AccountId != currentAccount.Id {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}

	dbConn := db.GetDB()
	// 是否已经 mute
	isMuted, err := status.IsMuted(dbConn, currentAccount)
	if err != nil {
		_ = c.Error(err)
		return
	}

	if isMuted {
		c.JSON(400, gin.H{"error": "already muted"})
		return
	}

	mute := &models.MuteStatus{}
	mute.AccountId = currentAccount.Id
	mute.StatusId = status.Id

	err = dbConn.Create(mute).Error

	if err != nil {
		err = errors.Wrap(err, "mute status failed")
		_ = c.Error(err)
		return
	}

	returnFullSingleStatus(c, status, currentAccount)
}

func UnmuteStatus(c *gin.Context) {
	status, abort := getRealStatus(c)

	if abort {
		return
	}

	currentAccount := c.MustGet("account").(*models.Account)
	dbConn := db.GetDB()
	//  只能 自己 mute 自己的
	if status.AccountId != currentAccount.Id {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}

	// 是否已经 mute
	isMuted, err := status.IsMuted(dbConn, currentAccount)
	if err != nil {
		err = errors.Wrap(err, "check mute status failed")
		_ = c.Error(err)
		return
	}

	if !isMuted {
		c.JSON(400, gin.H{"error": "not muted"})
		return
	}

	err = dbConn.Delete(&models.MuteStatus{}, "account_id = ? AND status_id = ?", currentAccount.Id, status.Id).Error
	if err != nil {
		err = errors.Wrap(err, "unmute status failed")
		_ = c.Error(err)
		return
	}

	// 同时删除对这个账号的mute
	err = dbConn.Delete(&models.MuteAccount{}, "account_id = ? AND target_id = ?", currentAccount.Id, status.AccountId).Error
	if err != nil {
		err = errors.Wrap(err, "unmute account failed")
		_ = c.Error(err)
		return
	}

	returnFullSingleStatus(c, status, currentAccount)
}
